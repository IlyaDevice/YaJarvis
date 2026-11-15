//go:build !windows

package main

// Профиль процессора работающего Джарвиса: kill -USR1 <pid> — 20 секунд записи в /tmp/jarvisd/cpu.prof.
// kill -USR2 <pid> — память в журнал и профиль выделений в /tmp/jarvisd/heap.prof.

import (
	"log"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"syscall"
	"time"
)

func init() {
	mem := make(chan os.Signal, 1)
	signal.Notify(mem, syscall.SIGUSR2)
	go func() {
		for range mem {
			runtime.GC()
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			log.Printf("память: живая куча %d МБ, всего у Go %d МБ, сборок %d, процессор на сборку %.0f%%, выделено всего %d МБ",
				m.HeapAlloc>>20, m.Sys>>20, m.NumGC, m.GCCPUFraction*100, m.TotalAlloc>>20)
			if f, err := os.Create("/tmp/jarvisd/heap.prof"); err == nil { // кто выделяет память: go tool pprof -sample_index=alloc_space
				_ = pprof.Lookup("allocs").WriteTo(f, 0)
				f.Close()
			}
		}
	}()
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGUSR1)
	go func() {
		for range ch {
			if os.Remove("/tmp/jarvisd/voicetest") == nil && voiceTestHook != nil { // замер громкости голосов (voicetest.go)
				go voiceTestHook()
				continue
			}
			f, err := os.Create("/tmp/jarvisd/cpu.prof")
			if err != nil {
				continue
			}
			if pprof.StartCPUProfile(f) == nil {
				log.Printf("профиль процессора: 20 с")
				time.Sleep(20 * time.Second)
				pprof.StopCPUProfile()
			}
			f.Close()
		}
	}()
}
