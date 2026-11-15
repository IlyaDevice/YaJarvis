package main

// Громкость самой колонки (её слышно у Алисы, музыки и Джарвиса). Ставим через Алису —
// «громкость N» беззвучным запросом, чтобы её шкала и кольцо остались в согласии; читаем из ALSA (Master).

import (
	"context"
	"fmt"
	"math"
	"net/http"
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
	pct := masterPercent()
	if pct < 0 {
		return -1
	}
	return (pct + 5) / 10
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
	if err := AliceText(fmt.Sprintf("громкость %d", lvl), true); err != nil {
		bad(rw, err.Error())
		return
	}
	volMu.Lock()
	volSet, volSetAt = lvl, time.Now()
	volMu.Unlock()
	writeJSON(rw, 200, map[string]any{"volume": lvl})
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
