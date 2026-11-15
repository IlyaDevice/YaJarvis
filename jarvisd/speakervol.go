package main

// Громкость самой колонки (её слышно у Алисы, музыки и Джарвиса). Ставим через Алису —
// «громкость N» беззвучным запросом, чтобы её шкала и кольцо остались в согласии; читаем из ALSA (Master).

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"time"
)

var (
	volPctRe = regexp.MustCompile(`\[(\d+)%\]`)
	volMu    sync.Mutex
	volSet   = -1 // что попросили последним — пока шагаем к нему, панель показывает его
	volSetAt time.Time
	volBusy  bool // фоновый шагатель идёт к volSet

	// Уровень, который насчитали сами. На Мини 3 журнал Алисы пишет уровень не на каждую смену,
	// поэтому помним: какой уровень при каком проценте Master (учимся на каждом шаге и каждой свежей строке журнала).
	volLastStamp string // последняя увиденная строка журнала с уровнем — новая строка значит свежую смену
	volByPct     = map[int]float64{}
)

// speakerVolume — громкость колонки 0..10 (-1 — неизвестна).
func speakerVolume() int {
	volMu.Lock()
	if volSet >= 0 && (volBusy || time.Since(volSetAt) < 3*time.Second) {
		v := volSet
		volMu.Unlock()
		return v
	}
	volMu.Unlock()
	v := currentVolume()
	if v < 0 {
		return -1
	}
	return int(math.Round(v))
}

// currentVolume — уровень Алисы 0..10 с половинками: свежая строка журнала, иначе то, что знаем
// про текущий процент Master, иначе последняя строка журнала, иначе грубо по Master.
func currentVolume() float64 {
	v, stamp := aliceVolumeLog()
	pct := masterPercent()
	volMu.Lock()
	defer volMu.Unlock()
	if v >= 0 && stamp != volLastStamp { // Алиса только что записала смену — ей и верим
		volLastStamp = stamp
		if pct >= 0 {
			volByPct[pct] = v
		}
		return v
	}
	if known, ok := volByPct[pct]; ok && pct >= 0 {
		return known
	}
	if v >= 0 {
		if pct >= 0 && len(volByPct) == 0 { // первое чтение: считаем журнал верным для текущего Master
			volByPct[pct] = v
		}
		return v
	}
	if pct < 0 {
		return -1
	}
	return math.Round(float64(pct) / 10)
}

// Уровень громкости самой Алисы: её журнал пишет «glagol … Changed volume to 0.7».
// На Лайт строка в maind.log на каждую смену; на Мини 3 у glagold свой журнал, и пишется не всегда.
// Журналы маленькие (ротация на ~110 КБ) — смотрим и предыдущий файл.
var aliceVolLogs = []string{"/data/quasar/daemons_logs/glagold.log", "/data/quasar/daemons_logs/maind.log",
	"/data/quasar/daemons_logs/glagold.log.1", "/data/quasar/daemons_logs/maind.log.1"}

var volStampRe = regexp.MustCompile(`\[(\d{4}-\d\d-\d\d \d\d:\d\d:\d\d[.\d]*)\]`)

const volMarker = "Changed volume to "

func aliceVolume() int {
	v := aliceVolumeF()
	if v < 0 {
		return -1
	}
	return int(math.Round(v))
}

// aliceVolumeF — уровень из журнала Алисы (0..10 с половинками), -1 — не прочитать.
func aliceVolumeF() float64 {
	v, _ := aliceVolumeLog()
	return v
}

// aliceVolumeLog — самая свежая по времени строка с уровнем из всех журналов: уровень и её метка.
func aliceVolumeLog() (float64, string) {
	best, bestStamp := -1.0, ""
	for _, p := range aliceVolLogs {
		if v, stamp := lastVolumeIn(p); v >= 0 && stamp >= bestStamp {
			best, bestStamp = v, stamp
		}
	}
	return best, bestStamp
}

// lastVolumeIn — последний уровень 0..10 из хвоста журнала (64 КБ) и время строки; -1 — строки нет.
func lastVolumeIn(path string) (float64, string) {
	f, err := os.Open(path)
	if err != nil {
		return -1, ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return -1, ""
	}
	off := max(0, st.Size()-64<<10)
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return -1, ""
	}
	return parseVolumeLine(buf)
}

// parseVolumeLine — последняя строка «Changed volume to X»: ищем с конца, без регулярки по всему хвосту
// (на процессоре колонки разбор 256 КБ журналов регуляркой занимал сотни миллисекунд на каждый опрос).
func parseVolumeLine(b []byte) (float64, string) {
	i := bytes.LastIndex(b, []byte(volMarker))
	if i < 0 {
		return -1, ""
	}
	num := b[i+len(volMarker):]
	if j := bytes.IndexAny(num, "\r\n "); j >= 0 {
		num = num[:j]
	}
	v, err := strconv.ParseFloat(string(num), 64)
	if err != nil || v < 0 || v > 1.001 {
		return -1, ""
	}
	line := b[bytes.LastIndexByte(b[:i], '\n')+1 : i]
	stamp := ""
	if m := volStampRe.FindSubmatch(line); m != nil {
		stamp = string(m[1])
	}
	return math.Round(v*20) / 2, stamp // 0.55 → 5.5
}

// masterPercent — регулятор Master в процентах (-1 — не прочитать).
func masterPercent() int {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// -c 0 — сразу карта колонки: без него amixer разбирает весь конфиг ALSA и во время музыки думает 2+ с
	out, err := exec.CommandContext(ctx, "amixer", "-c", "0", "sget", "Master").Output()
	if err != nil {
		out, err = exec.CommandContext(ctx, "amixer", "sget", "Master").Output()
		if err != nil {
			return -1
		}
	}
	m := volPctRe.FindSubmatch(out)
	if m == nil {
		return -1
	}
	pct, _ := strconv.Atoi(string(m[1]))
	return pct
}

func (w *Web) setSpeakerVolume(rw http.ResponseWriter, r *http.Request) {
	var req struct{ Level int }
	if readJSON(r, &req) != nil {
		bad(rw, "пустой запрос")
		return
	}
	lvl := max(0, min(10, req.Level))
	if err := setAliceVolume(lvl); err != nil {
		bad(rw, err.Error())
		return
	}
	writeJSON(rw, 200, map[string]any{"volume": lvl})
}

// setAliceVolume — поставить уровень Алисы lvl (0..10). Сразу возвращается: шагает фоновый обработчик, а
// частые нажатия в панели просто меняют цель. Шаги — «громче»/«тише»: эти две команды Алиса выполняет сама
// на колонке (быстрые локальные команды), без облака и без ответа голосом — работает и там, где Алисе закрыт
// выход в интернет. Шаг засчитываем по свежей строке журнала или по сдвигу Master.
func setAliceVolume(lvl int) error {
	c, err := net.DialTimeout("tcp", "127.0.0.1:9875", time.Second)
	if err != nil {
		return fmt.Errorf("Алиса недоступна: %w", err)
	}
	c.Close()
	volMu.Lock()
	volSet, volSetAt = lvl, time.Now()
	start := !volBusy
	volBusy = true
	volMu.Unlock()
	if start {
		go volumeWorker()
	}
	return nil
}

func volumeWorker() {
	cur := currentVolume()
	if cur < 0 {
		volMu.Lock()
		lvl := volSet
		volBusy, volSetAt = false, time.Now()
		volMu.Unlock()
		_ = AliceText(fmt.Sprintf("громкость %d", lvl), true)
		return
	}
	for i := 0; i < 30; i++ {
		volMu.Lock()
		lvl := volSet
		if int(math.Round(cur)) == lvl {
			volBusy, volSetAt = false, time.Now()
			volMu.Unlock()
			return
		}
		volMu.Unlock()
		dir, word := 1.0, "громче"
		if cur > float64(lvl) {
			dir, word = -1, "тише"
		}
		_, stamp0 := aliceVolumeLog()
		pct0 := masterPercent()
		if err := AliceText(word, true); err != nil {
			log.Printf("громкость: %v", err)
			break
		}
		changed := false
		for j := 0; j < 40 && !changed; j++ { // до 2 с: журнал — каждые 50 мс, Master — раз в 250 мс, если журнал молчит
			time.Sleep(50 * time.Millisecond)
			if v, stamp := aliceVolumeLog(); v >= 0 && stamp != stamp0 {
				cur, changed = v, true
			} else if j%5 == 4 {
				if p := masterPercent(); p >= 0 && pct0 >= 0 && p != pct0 {
					cur, changed = max(0, min(10, math.Round(cur)+dir)), true
				}
			}
		}
		if !changed {
			volMu.Lock()
			volBusy, volSetAt = false, time.Now()
			volMu.Unlock()
			log.Printf("громкость: шаг «%s» не сработал — прошу у Алисы «громкость %d»", word, lvl)
			_ = AliceText(fmt.Sprintf("громкость %d", lvl), true)
			return
		}
		if p := masterPercent(); p >= 0 {
			volMu.Lock()
			volByPct[p] = cur
			if _, stamp := aliceVolumeLog(); stamp != "" {
				volLastStamp = stamp
			}
			volMu.Unlock()
		}
	}
	volMu.Lock()
	volBusy, volSetAt = false, time.Now()
	volMu.Unlock()
}

// Голос Джарвиса идёт через тот же регулятор Master, что и Алиса: громкость колонки и так делает его тише или
// громче (уровень Алисы = 5 дБ). Но сам голос ещё усилен, чтобы его было слышно на тихой колонке, — на громкой
// это усиление лишнее, и Джарвис орёт громче Алисы. Поэтому выше уровня 3 каждый уровень колонки снимает
// voiceTrimStep дБ усиления: голос всё так же громче с ростом громкости, но мягче, и на 10 — примерно как Алиса.
const (
	voiceTrimFrom = 3.0 // до этого уровня колонки — полное усиление
	voiceTrimStep = 2.0 // дБ на уровень выше
)

// voiceTrim — множитель громкости голоса при громкости колонки level (0..10).
func voiceTrim(level float64) float64 {
	cut := max(0, level-voiceTrimFrom) * voiceTrimStep
	return math.Pow(10, -cut/20)
}

var lvlCache struct {
	sync.Mutex
	v    float64
	at   time.Time
	busy bool
}

// speakerLevel — громкость колонки 0..10 (с дробью). amixer — отдельный процесс, поэтому не чаще раза в 3 с и
// в фоне: звук голоса читает это на каждом куске и ждать не должен.
func speakerLevel() float64 {
	volMu.Lock()
	if volSet >= 0 && time.Since(volSetAt) < 4*time.Second {
		v := volSet
		volMu.Unlock()
		return float64(v)
	}
	volMu.Unlock()
	lvlCache.Lock()
	defer lvlCache.Unlock()
	if time.Since(lvlCache.at) > 3*time.Second && !lvlCache.busy {
		lvlCache.busy = true
		go func() {
			pct := masterPercent()
			lvlCache.Lock()
			if pct >= 0 {
				lvlCache.v = float64(pct) / 10
			}
			lvlCache.at, lvlCache.busy = time.Now(), false
			lvlCache.Unlock()
		}()
	}
	if lvlCache.at.IsZero() {
		return voiceTrimFrom // ещё не знаем — без поправки
	}
	return lvlCache.v
}
