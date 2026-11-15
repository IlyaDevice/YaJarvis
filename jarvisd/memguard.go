package main

// Предел памяти Go — не жёсткие 40 МБ, а «живые данные × 1,6 + 16 МБ» (от 48 до 128 МБ).
// Жёсткий предел ниже живой кучи (разобранный плеер YouTube держит десятки МБ) заставлял сборщик
// мусора работать без остановки — Джарвис ел два ядра из четырёх.

import (
	"log"
	"runtime/debug"
	"runtime/metrics"
	"time"
)

func memLimitFor(live uint64) int64 {
	l := int64(live)*8/5 + 16<<20
	return max(48<<20, min(l, 128<<20))
}

func memGuard(idle ...func() bool) {
	sample := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
	last := int64(0)
	for {
		metrics.Read(sample)
		if sample[0].Value.Kind() == metrics.KindUint64 {
			lim := memLimitFor(sample[0].Value.Uint64())
			if d := lim - last; d > 4<<20 || d < -4<<20 {
				debug.SetMemoryLimit(lim)
				log.Printf("память: живых данных %d МБ — предел %d МБ", sample[0].Value.Uint64()>>20, lim>>20)
				last = lim
			}
		}
		if len(idle) > 0 && idle[0]() {
			debug.FreeOSMemory()
		}
		time.Sleep(time.Minute)
	}
}
