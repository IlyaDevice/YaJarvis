package main

// Режим обучения: колонка сама копит записи для дообучения слова «Джарвис».
//   pos   — настоящие оклики (имя подтвердилось расшифровкой);
//   false — ложные срабатывания (копятся всегда, как раньше);
//   near  — «почти оклики»: детектор насторожился, но порог не прошёл (бывает и пропущенный «Джарвис»);
//   bg    — фон комнаты: речь, ТВ, музыка — чего слово не должно путать.
// В настройках записи можно прослушать, переразметить, скачать архивом и обучить на компьютере
// (kws/from_speaker.py), а готовую модель загрузить обратно.

import (
	"archive/zip"
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var trainKinds = []string{"pos", "false", "near", "bg"}

type Trainer struct {
	dataDir string
	store   *Store
	cloud   clipCloud // куда уходят старые записи (Google Диск)
	rm      trainRemote

	mu       sync.Mutex
	nearPeak float32
	nearClip []int16
	lastNear time.Time
	lastBg   time.Time
}

func (t *Trainer) dir(kind string) string {
	if kind == "false" {
		return filepath.Join(t.dataDir, "false") // там же, где и раньше
	}
	return filepath.Join(t.dataDir, "train", kind)
}

func validKind(k string) bool {
	for _, x := range trainKinds {
		if x == k {
			return true
		}
	}
	return false
}

type trainClip struct {
	Kind     string    `json:"kind"`
	Name     string    `json:"name"`
	Reviewed bool      `json:"reviewed"` // разметку подтвердил человек (в имени «_r»)
	Size     int64     `json:"size"`
	Time     time.Time `json:"time"`
	Remote   bool      `json:"remote,omitempty"` // лежит на Google Диске
	path     string
	id       string
}

// clips — все записи: на колонке и на Google Диске, новые сначала.
func (t *Trainer) clips() []trainClip {
	out := append(t.localClips(), t.remoteClips()...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out
}

// localClips — записи на колонке, новые сначала.
func (t *Trainer) localClips() []trainClip {
	var out []trainClip
	for _, k := range trainKinds {
		entries, _ := os.ReadDir(t.dir(k))
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".wav") {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			out = append(out, trainClip{Kind: k, Name: e.Name(), Reviewed: strings.HasSuffix(e.Name(), "_r.wav"), Size: info.Size(),
				Time: info.ModTime(), path: filepath.Join(t.dir(k), e.Name())})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Time.After(out[j].Time) })
	return out
}

// Save — записать клип. Не в режиме обучения пишутся только ложные срабатывания.
func (t *Trainer) Save(kind string, pcm []int16, note string) {
	if t == nil || len(pcm) == 0 {
		return
	}
	s := t.store.Get()
	if kind != "false" && !s.TrainMode {
		return
	}
	if free, _ := diskSpace(t.dataDir); free > 0 && free < 4 {
		return // место на /data нужно самой колонке
	}
	limit := int64(max(1, s.TrainLimitMB)) << 20
	clips := t.localClips()
	var used int64
	for _, c := range clips {
		used += c.Size
	}
	need := int64(44 + 2*len(pcm))
	ceiling := limit
	if used+need > limit && t.cloudReady() {
		// старые записи уходят на Google Диск пачкой; пока отправляются — небольшой запас сверх лимита
		t.offloadSoon()
		ceiling = limit + 1<<20
	}
	// не хватает места — сначала удаляем самые старые «почти» и фон, потом остальное
	for _, drop := range [][]string{{"bg", "near"}, {"false"}, {"pos"}} {
		for i := len(clips) - 1; i >= 0 && used+need > ceiling; i-- {
			c := clips[i]
			for _, k := range drop {
				if c.Kind == k && c.path != "" {
					if os.Remove(c.path) == nil {
						used -= c.Size
					}
					clips[i].path = ""
				}
			}
		}
	}
	if used+need > ceiling {
		return
	}
	dir := t.dir(kind)
	_ = os.MkdirAll(dir, 0o700)
	name := time.Now().Format("20060102-150405.000")
	if note != "" {
		name += "_" + note
	}
	_ = writeWAV(filepath.Join(dir, name+".wav"), pcm)
}

// Observe — каждый кадр микрофона вне разговора: ловим «почти оклики» и изредка пишем фон.
func (t *Trainer) Observe(prob float32, ring []int16, loudNow bool) {
	if t == nil || !t.store.Flags().TrainMode {
		return
	}
	t.mu.Lock()
	var save []int16
	var peak float32
	switch {
	case prob >= 0.35 && prob > t.nearPeak:
		t.nearPeak, t.nearClip = prob, nil // запомним клип, когда вероятность начнёт падать (слово целиком)
	case t.nearPeak > 0 && t.nearClip == nil && prob < t.nearPeak*0.8:
		t.nearClip = append([]int16(nil), ring...)
	case t.nearPeak > 0 && prob < 0.15:
		if t.nearClip != nil && time.Since(t.lastNear) > 20*time.Second {
			save, peak, t.lastNear = t.nearClip, t.nearPeak, time.Now()
		}
		t.nearPeak, t.nearClip = 0, nil
	}
	bg := loudNow && time.Since(t.lastBg) > 7*time.Minute && len(ring) >= 3*MicRate
	if bg {
		t.lastBg = time.Now()
	}
	t.mu.Unlock()
	if save != nil {
		go t.Save("near", save, probNote(peak))
	}
	if bg {
		clip := append([]int16(nil), ring...)
		go t.Save("bg", clip, "")
	}
}

// Reset — сработало слово: «почти оклик» оказался настоящим, не пишем его.
func (t *Trainer) Reset() {
	if t == nil {
		return
	}
	t.mu.Lock()
	t.nearPeak, t.nearClip = 0, nil
	t.mu.Unlock()
}

func probNote(p float32) string {
	return "p" + itoa(int(p*100))
}

// ---- веб

func (w *Web) getTrain(rw http.ResponseWriter, r *http.Request) {
	t := w.engine.train
	s := w.store.Get()
	clips := t.clips()
	counts := map[string]int{}
	var used int64
	remote := 0
	for _, c := range clips {
		counts[c.Kind]++
		if c.Remote {
			remote++
		} else {
			used += c.Size
		}
	}
	offloading, cloudErr := t.remoteState()
	free, _ := diskSpace(w.dataDir)
	if len(clips) > 400 {
		clips = clips[:400]
	}
	_, prevErr := os.Stat(filepath.Join(w.dataDir, "wake.bin.prev"))
	writeJSON(rw, 200, map[string]any{"mode": s.TrainMode, "limit_mb": s.TrainLimitMB, "used_kb": used >> 10, "free_mb": free,
		"counts": counts, "clips": clips, "prev_model": prevErr == nil, "remote": remote, "cloud": t.cloudReady(),
		"offloading": offloading, "cloud_error": cloudErr})
}

func (w *Web) trainPath(kind, name string) (string, bool) {
	if !validKind(kind) || name != filepath.Base(name) || !strings.HasSuffix(name, ".wav") || strings.HasPrefix(name, ".") {
		return "", false
	}
	return filepath.Join(w.engine.train.dir(kind), name), true
}

func (w *Web) getTrainClip(rw http.ResponseWriter, r *http.Request) {
	p, ok := w.trainPath(r.URL.Query().Get("kind"), r.URL.Query().Get("name"))
	if !ok {
		http.NotFound(rw, r)
		return
	}
	rw.Header().Set("Content-Type", "audio/wav")
	if _, err := os.Stat(p); err != nil {
		c, ok := w.engine.train.findRemote(r.URL.Query().Get("kind"), r.URL.Query().Get("name"))
		if !ok {
			http.NotFound(rw, r)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		data, err := w.engine.train.remoteData(ctx, c)
		if err != nil {
			http.Error(rw, err.Error(), http.StatusBadGateway)
			return
		}
		rw.Header().Set("Cache-Control", "private, max-age=600")
		http.ServeContent(rw, r, c.Name, c.Time, bytes.NewReader(data))
		return
	}
	http.ServeFile(rw, r, p)
}

func (w *Web) moveTrainClip(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind, Name, To string
		Reviewed       bool // человек прослушал и подтвердил
	}
	if readJSON(r, &req) != nil {
		bad(rw, "пустой запрос")
		return
	}
	p, ok := w.trainPath(req.Kind, req.Name)
	if !ok {
		bad(rw, "нет такой записи")
		return
	}
	if _, err := os.Stat(p); err != nil {
		c, found := w.engine.train.findRemote(req.Kind, req.Name)
		if !found || (req.To != "delete" && !validKind(req.To)) {
			bad(rw, "нет такой записи")
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := w.engine.train.moveRemote(ctx, c, req.To, req.Reviewed); err != nil {
			bad(rw, err.Error())
			return
		}
		writeJSON(rw, 200, map[string]any{"ok": true})
		return
	}
	if req.To == "delete" {
		_ = os.Remove(p)
	} else if validKind(req.To) {
		dir := w.engine.train.dir(req.To)
		_ = os.MkdirAll(dir, 0o700)
		name := req.Name
		if req.Reviewed && !strings.HasSuffix(name, "_r.wav") {
			name = strings.TrimSuffix(name, ".wav") + "_r.wav"
		}
		if err := os.Rename(p, filepath.Join(dir, name)); err != nil {
			bad(rw, err.Error())
			return
		}
	} else {
		bad(rw, "куда переложить?")
		return
	}
	writeJSON(rw, 200, map[string]any{"ok": true})
}

func (w *Web) clearTrain(rw http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	for _, c := range w.engine.train.localClips() {
		if kind == "" || c.Kind == kind {
			_ = os.Remove(c.path)
		}
	}
	w.engine.train.clearRemote(kind)
	writeJSON(rw, 200, map[string]any{"ok": true})
}

//go:embed trainkit/finetune.py
var finetunePy []byte

const trainReadme = `Записи с колонки для дообучения слова «Джарвис» (16 кГц, моно, слово — в конце клипа).
  pos/   — настоящие «Джарвис»
  false/ — ложные срабатывания
  near/  — «почти оклики» (детектор насторожился, но не сработал); число после p — уверенность в процентах
  bg/    — фон комнаты
  wake.bin — модель, которая сейчас стоит на колонке
Записи с «_r» в имени проверены человеком в настройках. near/ при обучении не используется.

Быстрое дообучение текущей модели (видеокарта не нужна, пара минут):
  pip install torch numpy
  python finetune.py
Получится wake_new.bin — загрузите его в настройках: «Обучение» → «Загрузить модель».

Полное обучение с нуля (видеокарта, синтетика и исходные записи):  python kws/from_speaker.py jarvis-train.zip
`

// downloadTrain — архив записей: с колонки и с Google Диска (Диск качаем параллельно, в архив пишем по порядку),
// плюс текущая модель и скрипт дообучения. WAV почти не сжимается — кладём без сжатия, процессор колонки не тратим.
func (w *Web) downloadTrain(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Content-Type", "application/zip")
	rw.Header().Set("Content-Disposition", `attachment; filename="jarvis-train-`+time.Now().Format("20060102-1504")+`.zip"`)
	z := zip.NewWriter(rw)
	put := func(name string, data []byte, method uint16, mod time.Time) {
		if f, err := z.CreateHeader(&zip.FileHeader{Name: name, Method: method, Modified: mod}); err == nil {
			_, _ = f.Write(data)
		}
	}
	now := time.Now()
	put("README.txt", []byte(trainReadme), zip.Deflate, now)
	put("finetune.py", finetunePy, zip.Deflate, now)
	if b, err := os.ReadFile(filepath.Join(w.dataDir, "wake.bin")); err == nil {
		put("wake.bin", b, zip.Deflate, now)
	}
	t := w.engine.train
	if t.store != nil { // чувствительность с колонки — finetune.py проверяет модель по её порогу
		b, _ := json.Marshal(map[string]any{"sensitivity": t.store.Get().WakeSensitivity})
		put("wake.json", b, zip.Deflate, now)
	}
	clips := t.clips()
	// записи с Диска — восемью потоками заранее; каждая ждёт своей очереди в архиве
	type got struct {
		data []byte
		err  error
	}
	ready := make([]chan got, len(clips))
	sem := make(chan struct{}, 8)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	for i, c := range clips {
		if c.Remote {
			ready[i] = make(chan got, 1)
		}
	}
	go func() {
		for i, c := range clips {
			if ready[i] == nil {
				continue
			}
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			go func(i int, c trainClip) {
				defer func() { <-sem }()
				cctx, ccancel := context.WithTimeout(ctx, 30*time.Second)
				defer ccancel()
				var b []byte
				var err error
				for try := 0; try < 2; try++ {
					if b, err = t.remoteBytes(cctx, remoteClip{Kind: c.Kind, Name: c.Name, ID: c.id}); err == nil || cctx.Err() != nil {
						break
					}
				}
				ready[i] <- got{b, err}
			}(i, c)
		}
	}()
	failed := 0
	for i, c := range clips {
		if ctx.Err() != nil {
			return
		}
		var data []byte
		if c.Remote {
			var g got
			select {
			case g = <-ready[i]:
			case <-ctx.Done():
				return
			}
			if g.err != nil {
				failed++
				continue
			}
			data = g.data
		} else {
			b, err := os.ReadFile(c.path)
			if err != nil {
				continue
			}
			data = b
		}
		put(c.Kind+"/"+c.Name, data, zip.Store, c.Time)
	}
	if failed > 0 {
		log.Printf("обучение: в архив не попало %d записей с Google Диска", failed)
	}
	_ = z.Close()
}

// putWakeModel — новая модель слова с компьютера: проверяем, что она загружается, прежнюю сохраняем.
func (w *Web) putWakeModel(rw http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, 4<<20))
	if err != nil || len(data) == 0 {
		bad(rw, "пустой файл")
		return
	}
	if _, err := LoadKWS(data, func() int { return 5 }); err != nil {
		bad(rw, "это не модель слова: "+err.Error())
		return
	}
	path := filepath.Join(w.dataDir, "wake.bin")
	if old, err := os.ReadFile(path); err == nil {
		_ = os.WriteFile(path+".prev", old, 0o644)
	}
	if err := writeFileAtomic(path, data); err != nil {
		bad(rw, err.Error())
		return
	}
	log.Printf("слово активации: загружена новая модель (%d КБ) — перезапуск", len(data)>>10)
	writeJSON(rw, 200, map[string]any{"ok": true})
	restartSoon()
}

func (w *Web) restoreWakeModel(rw http.ResponseWriter, r *http.Request) {
	path := filepath.Join(w.dataDir, "wake.bin")
	prev, err := os.ReadFile(path + ".prev")
	if err != nil {
		bad(rw, "прежней модели нет")
		return
	}
	cur, _ := os.ReadFile(path)
	if err := writeFileAtomic(path, prev); err != nil {
		bad(rw, err.Error())
		return
	}
	if cur != nil {
		_ = os.WriteFile(path+".prev", cur, 0o644)
	}
	writeJSON(rw, 200, map[string]any{"ok": true})
	restartSoon()
}

func writeFileAtomic(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// restartSoon — выйти через секунду: jarvisd.sh loop запустит программу заново (с новой моделью/версией).
func restartSoon() {
	time.AfterFunc(time.Second, func() {
		log.Printf("перезапуск")
		os.Exit(0)
	})
}
