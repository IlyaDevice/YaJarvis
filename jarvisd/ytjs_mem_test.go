package main

import (
	"os"
	"runtime"
	"runtime/debug"
	"sync/atomic"
	"testing"
	"time"
)

// Пик памяти при сборке расшифровщика: JARVIS_PLAYER=… JARVIS_MEM=pre|build go test -run TestYtJSMem -v
func TestYtJSMem(t *testing.T) {
	path, mode := os.Getenv("JARVIS_PLAYER"), os.Getenv("JARVIS_MEM")
	if path == "" || mode == "" {
		t.Skip("нужны JARVIS_PLAYER и JARVIS_MEM")
	}
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	src = nil
	runtime.GC()
	var peak atomic.Uint64
	stop := make(chan struct{})
	go func() {
		var m runtime.MemStats
		for {
			select {
			case <-stop:
				return
			case <-time.After(5 * time.Millisecond):
			}
			runtime.ReadMemStats(&m)
			if m.HeapInuse > peak.Load() {
				peak.Store(m.HeapInuse)
			}
		}
	}()
	if mode == "pre" {
		defer debug.SetGCPercent(debug.SetGCPercent(20))
		c, err := ytPreprocess(s)
		if err != nil {
			t.Fatal(err)
		}
		_ = c
	} else {
		_, _, err := ytBuildSolver(s)
		if err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	t.Logf("%s: пик кучи %d МБ, взято у системы %d МБ", mode, peak.Load()>>20, m.Sys>>20)
}
