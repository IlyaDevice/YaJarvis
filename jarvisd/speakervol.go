package main

// Громкость самой колонки (её слышно у Алисы, музыки и Джарвиса). Ставим через Алису —
// «громкость N» беззвучным запросом, чтобы её шкала и кольцо остались в согласии; читаем из ALSA (Master).

import (
	"context"
	"fmt"
	"io"
	"log"
	"math"
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
	volSet   = -1 // что попросили последним — Алисе нужно время, чтобы применить
	volSetAt time.Time
)

// speakerVolume — громкость колонки 0..10 (-1 — неизвестна).
func speakerVolume() int {
	volMu.Lock()
	if volSet >= 0 && time.Since(volSetAt) < 4*time.Second {
		v := volSet
		volMu.Unlock()
		return v
	}
	volMu.Unlock()
	if v := aliceVolume(); v >= 0 {
		return v
	}
	pct := masterPercent()
	if pct < 0 {
		return -1
	}
	return (pct + 5) / 10
}

// Уровень громкости самой Алисы: её журнал на каждую смену пишет «glagol … Changed volume to 0.7».
// Это точнее микшера: на Мини 3 проценты Master не совпадают с уровнями (уровень 4 — 36 %).
// На Лайт строка в maind.log, на Мини 3 у glagold свой журнал.
var aliceVolLogs = []string{"/data/quasar/daemons_logs/glagold.log", "/data/quasar/daemons_logs/maind.log"}

var volChangedRe = regexp.MustCompile(`Changed volume to ([0-9.]+)`)

func aliceVolume() int {
	v := aliceVolumeF()
	if v < 0 {
		return -1
	}
	return int(math.Round(v))
}

// aliceVolumeF — уровень Алисы 0..10 с половинками (у Лайт 20 шагов), -1 — не прочитать.
func aliceVolumeF() float64 {
	for _, p := range aliceVolLogs {
		if v := lastVolumeIn(p); v >= 0 {
			return v
		}
	}
	return -1
}

// lastVolumeIn — последний уровень 0..10 из хвоста журнала (64 КБ), -1 — строки нет.
func lastVolumeIn(path string) float64 {
	f, err := os.Open(path)
	if err != nil {
		return -1
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return -1
	}
	off := max(0, st.Size()-64<<10)
	buf := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return -1
	}
	return parseVolumeLine(buf)
}

func parseVolumeLine(b []byte) float64 {
	m := volChangedRe.FindAllSubmatch(b, -1)
	if len(m) == 0 {
		return -1
	}
	v, err := strconv.ParseFloat(string(m[len(m)-1][1]), 64)
	if err != nil || v < 0 || v > 1.001 {
		return -1
	}
	return math.Round(v*20) / 2 // 0.55 → 5.5
}

// masterPercent — регулятор Master в процентах (-1 — не прочитать).
func masterPercent() int {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "amixer", "sget", "Master").Output()
	if err != nil {
		return -1
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

// setAliceVolume — поставить уровень Алисы lvl (0..10). Шагами «громче»/«тише»: эти две команды Алиса
// выполняет сама на колонке (быстрые локальные команды), без облака и без ответа голосом — работает и там,
// где Алисе закрыт выход в интернет (у тестера на Мини 3 «громкость N» уходила в облако и не применялась).
// Каждый шаг ждём по её журналу. Уровень не читается или шаг не сработал — просим «громкость N» у облака.
func setAliceVolume(lvl int) error {
	volMu.Lock()
	volSet, volSetAt = lvl, time.Now()
	volMu.Unlock()
	for i := 0; i < 12; i++ {
		cur := aliceVolumeF()
		if cur < 0 {
			break
		}
		if int(math.Round(cur)) == lvl {
			return nil
		}
		word := "громче"
		if cur > float64(lvl) {
			word = "тише"
		}
		if err := AliceText(word, true); err != nil {
			return err
		}
		changed := false
		for j := 0; j < 16 && !changed; j++ {
			time.Sleep(50 * time.Millisecond)
			changed = aliceVolumeF() != cur
		}
		if !changed {
			log.Printf("громкость: шаг «%s» не сработал — прошу у Алисы «громкость %d»", word, lvl)
			break
		}
	}
	return AliceText(fmt.Sprintf("громкость %d", lvl), true)
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
