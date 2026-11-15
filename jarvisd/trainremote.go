package main

// Записи для обучения — на колонке только свежие (лимит в настройках, например 5 МБ). Когда лимит заполнен,
// самые старые уходят на Google Диск пачкой по 10 (папка «Jarvis — обучение/pos|false|near|bg») и
// удаляются с колонки — так Диск дёргается редко, а запас записей растёт без ограничений.
// В «Проверке» и списке видны все записи; с Диска колонка докачивает файл и сразу отдаёт его в браузер,
// а переразметка переносит файл в нужную папку прямо на Диске.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const offloadBatch = 10

// clipCloud — куда уходят старые записи (Google Диск; в тестах — подделка).
type clipCloud interface {
	ready() bool
	mkdir(ctx context.Context, name, parent string) (string, error)
	put(ctx context.Context, parent, name string, data []byte) (string, error)
	get(ctx context.Context, id string) ([]byte, error)
	move(ctx context.Context, id, name, from, to string) error
	del(ctx context.Context, id string) error
}

type remoteClip struct {
	Kind string    `json:"kind"`
	Name string    `json:"name"`
	Size int64     `json:"size"`
	Time time.Time `json:"time"`
	ID   string    `json:"id"`
}

type remoteIndex struct {
	Root    string            `json:"root"`
	Folders map[string]string `json:"folders"`
	Clips   []remoteClip      `json:"clips"`
}

// trainRemote — состояние выгрузки (живёт в Trainer).
type trainRemote struct {
	mu         sync.Mutex
	idx        *remoteIndex
	offloading bool
	lastErr    string
	cache      map[string][]byte // последние скачанные с Диска записи: проверка играет их по кругу
	cacheOrder []string
}

func (t *Trainer) indexPath() string { return filepath.Join(t.dataDir, "train", "remote.json") }

// index — загрузить индекс записей на Диске (под t.rm.mu).
func (t *Trainer) index() *remoteIndex {
	if t.rm.idx == nil {
		t.rm.idx = &remoteIndex{Folders: map[string]string{}}
		if b, err := os.ReadFile(t.indexPath()); err == nil {
			_ = json.Unmarshal(b, t.rm.idx)
		}
		if t.rm.idx.Folders == nil {
			t.rm.idx.Folders = map[string]string{}
		}
	}
	return t.rm.idx
}

func (t *Trainer) saveIndex() {
	b, _ := json.Marshal(t.rm.idx)
	_ = os.MkdirAll(filepath.Dir(t.indexPath()), 0o700)
	if err := writeFileAtomic(t.indexPath(), b); err != nil {
		log.Printf("обучение: индекс Диска: %v", err)
	}
}

func (t *Trainer) cloudReady() bool { return t.cloud != nil && t.cloud.ready() }

func (t *Trainer) remoteClips() []trainClip {
	t.rm.mu.Lock()
	defer t.rm.mu.Unlock()
	var out []trainClip
	for _, c := range t.index().Clips {
		out = append(out, trainClip{Kind: c.Kind, Name: c.Name, Reviewed: strings.HasSuffix(c.Name, "_r.wav"), Size: c.Size, Time: c.Time, Remote: true, id: c.ID})
	}
	return out
}

func (t *Trainer) findRemote(kind, name string) (remoteClip, bool) {
	t.rm.mu.Lock()
	defer t.rm.mu.Unlock()
	for _, c := range t.index().Clips {
		if c.Kind == kind && c.Name == name {
			return c, true
		}
	}
	return remoteClip{}, false
}

// kindFolder — папка вида записи на Диске (создаётся при первой выгрузке).
func (t *Trainer) kindFolder(ctx context.Context, kind string) (string, error) {
	t.rm.mu.Lock()
	idx := t.index()
	root, id := idx.Root, idx.Folders[kind]
	t.rm.mu.Unlock()
	if id != "" {
		return id, nil
	}
	var err error
	if root == "" {
		if root, err = t.cloud.mkdir(ctx, "Jarvis — обучение", ""); err != nil {
			return "", err
		}
	}
	if id, err = t.cloud.mkdir(ctx, kind, root); err != nil {
		return "", err
	}
	t.rm.mu.Lock()
	idx.Root, idx.Folders[kind] = root, id
	t.saveIndex()
	t.rm.mu.Unlock()
	return id, nil
}

// offloadSoon — в фоне выгрузить самые старые записи (одна выгрузка за раз).
func (t *Trainer) offloadSoon() {
	t.rm.mu.Lock()
	if t.rm.offloading {
		t.rm.mu.Unlock()
		return
	}
	t.rm.offloading = true
	t.rm.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		err := t.offload(ctx, offloadBatch)
		t.rm.mu.Lock()
		t.rm.offloading = false
		t.rm.lastErr = ""
		if err != nil {
			t.rm.lastErr = err.Error()
		}
		t.rm.mu.Unlock()
		if err != nil {
			log.Printf("обучение: выгрузка на Google Диск: %v", err)
		}
	}()
}

// offload — n самых старых записей с колонки → Диск; удаляются с колонки только после успешной отправки.
func (t *Trainer) offload(ctx context.Context, n int) error {
	local := t.localClips() // новые сначала
	if len(local) == 0 {
		return nil
	}
	batch := local[max(0, len(local)-n):]
	sent := 0
	for i := len(batch) - 1; i >= 0; i-- { // от самой старой
		c := batch[i]
		data, err := os.ReadFile(c.path)
		if err != nil {
			continue
		}
		folder, err := t.kindFolder(ctx, c.Kind)
		if err != nil {
			return err
		}
		id, err := t.cloud.put(ctx, folder, c.Name, data)
		if err != nil && strings.Contains(err.Error(), " 404") { // папку удалили на Диске — заведём заново
			t.rm.mu.Lock()
			idx := t.index()
			idx.Root, idx.Folders = "", map[string]string{}
			t.saveIndex()
			t.rm.mu.Unlock()
			if folder, err = t.kindFolder(ctx, c.Kind); err == nil {
				id, err = t.cloud.put(ctx, folder, c.Name, data)
			}
		}
		if err != nil {
			return err
		}
		t.rm.mu.Lock()
		idx := t.index()
		idx.Clips = append(idx.Clips, remoteClip{Kind: c.Kind, Name: c.Name, Size: c.Size, Time: c.Time, ID: id})
		t.saveIndex()
		t.rm.mu.Unlock()
		_ = os.Remove(c.path)
		sent++
	}
	if sent > 0 {
		log.Printf("обучение: %d записей ушло на Google Диск", sent)
	}
	return nil
}

// remoteData — содержимое записи с Диска (последние несколько держим в памяти).
func (t *Trainer) remoteData(ctx context.Context, c remoteClip) ([]byte, error) {
	t.rm.mu.Lock()
	if b, ok := t.rm.cache[c.ID]; ok {
		t.rm.mu.Unlock()
		return b, nil
	}
	t.rm.mu.Unlock()
	if !t.cloudReady() {
		return nil, errors.New("Google Диск не подключён")
	}
	b, err := t.cloud.get(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	t.rm.mu.Lock()
	if t.rm.cache == nil {
		t.rm.cache = map[string][]byte{}
	}
	t.rm.cache[c.ID] = b
	t.rm.cacheOrder = append(t.rm.cacheOrder, c.ID)
	for len(t.rm.cacheOrder) > 6 {
		delete(t.rm.cache, t.rm.cacheOrder[0])
		t.rm.cacheOrder = t.rm.cacheOrder[1:]
	}
	t.rm.mu.Unlock()
	return b, nil
}

// remoteBytes — запись с Диска для архива: из кэша, если есть, но кэш прослушивания не засоряем.
func (t *Trainer) remoteBytes(ctx context.Context, c remoteClip) ([]byte, error) {
	t.rm.mu.Lock()
	b, ok := t.rm.cache[c.ID]
	t.rm.mu.Unlock()
	if ok {
		return b, nil
	}
	if !t.cloudReady() {
		return nil, errors.New("Google Диск не подключён")
	}
	return t.cloud.get(ctx, c.ID)
}

// moveRemote — переразметить или удалить запись на Диске.
func (t *Trainer) moveRemote(ctx context.Context, c remoteClip, to string, reviewed bool) error {
	if !t.cloudReady() {
		return errors.New("Google Диск не подключён")
	}
	if to == "delete" {
		if err := t.cloud.del(ctx, c.ID); err != nil && !strings.Contains(err.Error(), " 404") {
			return err
		}
		t.dropRemote(c.ID)
		return nil
	}
	name := c.Name
	if reviewed && !strings.HasSuffix(name, "_r.wav") {
		name = strings.TrimSuffix(name, ".wav") + "_r.wav"
	}
	from, err := t.kindFolder(ctx, c.Kind)
	if err != nil {
		return err
	}
	dest, err := t.kindFolder(ctx, to)
	if err != nil {
		return err
	}
	if err := t.cloud.move(ctx, c.ID, name, from, dest); err != nil {
		return err
	}
	t.rm.mu.Lock()
	for i, x := range t.index().Clips {
		if x.ID == c.ID {
			t.rm.idx.Clips[i].Kind, t.rm.idx.Clips[i].Name = to, name
		}
	}
	t.saveIndex()
	t.rm.mu.Unlock()
	return nil
}

func (t *Trainer) dropRemote(id string) {
	t.rm.mu.Lock()
	defer t.rm.mu.Unlock()
	idx := t.index()
	out := idx.Clips[:0]
	for _, x := range idx.Clips {
		if x.ID != id {
			out = append(out, x)
		}
	}
	idx.Clips = out
	t.saveIndex()
}

// clearRemote — удалить записи вида kind ("" — все) и с Диска.
func (t *Trainer) clearRemote(kind string) {
	var del []remoteClip
	t.rm.mu.Lock()
	idx := t.index()
	keep := idx.Clips[:0]
	for _, x := range idx.Clips {
		if kind == "" || x.Kind == kind {
			del = append(del, x)
		} else {
			keep = append(keep, x)
		}
	}
	idx.Clips = keep
	t.saveIndex()
	t.rm.mu.Unlock()
	if len(del) == 0 || !t.cloudReady() {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		for _, x := range del {
			_ = t.cloud.del(ctx, x.ID)
		}
	}()
}

func (t *Trainer) remoteState() (offloading bool, lastErr string) {
	t.rm.mu.Lock()
	defer t.rm.mu.Unlock()
	return t.rm.offloading, t.rm.lastErr
}

// ---- Google Диск как хранилище записей

func (g *GDrive) ready() bool { return g.store.Get().GDriveRefresh != "" }

func (g *GDrive) mkdir(ctx context.Context, name, parent string) (string, error) {
	meta := map[string]any{"name": name, "mimeType": "application/vnd.google-apps.folder"}
	if parent != "" {
		meta["parents"] = []string{parent}
	}
	b, _ := json.Marshal(meta)
	out, err := g.api(ctx, http.MethodPost, "https://www.googleapis.com/drive/v3/files?fields=id", "application/json", bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	return str(out["id"]), nil
}

func (g *GDrive) put(ctx context.Context, parent, name string, data []byte) (string, error) {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Type", "application/json; charset=UTF-8")
	pw, _ := mw.CreatePart(h)
	_ = json.NewEncoder(pw).Encode(map[string]any{"name": name, "parents": []string{parent}})
	h = textproto.MIMEHeader{}
	h.Set("Content-Type", "audio/wav")
	pw, _ = mw.CreatePart(h)
	_, _ = pw.Write(data)
	_ = mw.Close()
	out, err := g.api(ctx, http.MethodPost, "https://www.googleapis.com/upload/drive/v3/files?uploadType=multipart&fields=id",
		"multipart/related; boundary="+mw.Boundary(), &buf)
	if err != nil {
		return "", err
	}
	return str(out["id"]), nil
}

func (g *GDrive) get(ctx context.Context, id string) ([]byte, error) {
	tok, err := g.token(ctx)
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/drive/v3/files/"+id+"?alt=media", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := g.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Google Диск ответил %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

func (g *GDrive) move(ctx context.Context, id, name, from, to string) error {
	u := "https://www.googleapis.com/drive/v3/files/" + id + "?fields=id"
	if from != to {
		u += "&addParents=" + to + "&removeParents=" + from
	}
	b, _ := json.Marshal(map[string]any{"name": name})
	_, err := g.api(ctx, http.MethodPatch, u, "application/json", bytes.NewReader(b))
	return err
}

func (g *GDrive) del(ctx context.Context, id string) error {
	_, err := g.api(ctx, http.MethodDelete, "https://www.googleapis.com/drive/v3/files/"+id, "", nil)
	return err
}
