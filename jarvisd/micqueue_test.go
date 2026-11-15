package main

import "testing"

// тихая (на этом микрофоне обычная) речь из очереди не выкидывается, а пауза ≥0,3 с — выкидывается
func TestQuietRunKeepsSoftSpeech(t *testing.T) {
	const frame = MicRate / 50
	pcm := make([]int16, 40*frame)
	for i := 0; i < 10*frame; i++ { // 0,2 с речи с пиком 300
		pcm[i] = int16(300 * (1 - 2*(i/8%2)))
	}
	pending := pcm
	for skipped := 0; skipped < 25 && quietRun(pending, frame, 15); skipped++ {
		pending = pending[frame:]
	}
	if len(pending) != len(pcm) {
		t.Fatalf("речь выкинута: осталось %d из %d", len(pending), len(pcm))
	}
	pending = pcm[10*frame:] // дальше 30 кадров тишины
	skipped := 0
	for ; skipped < 25 && quietRun(pending, frame, 15); skipped++ {
		pending = pending[frame:]
	}
	if skipped != 15 {
		t.Fatalf("из паузы пропущено %d кадров, ждали 15", skipped)
	}
}
