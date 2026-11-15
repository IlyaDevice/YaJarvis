package main

// Громкость самой колонки (её слышно у Алисы, музыки и Джарвиса). Ставим через Алису —
// «громкость N» беззвучным запросом, чтобы её шкала и кольцо остались в согласии; читаем из ALSA (Master).

import (
	"context"
	"fmt"
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
	return (pct + 5) / 10
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
