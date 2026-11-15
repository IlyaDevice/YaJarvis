package main

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Совпадение с PyTorch: JARVIS_KWS_DIR=…/kws/out go test -run KWS -v
func TestKWSMatchesPython(t *testing.T) {
	dir := os.Getenv("JARVIS_KWS_DIR")
	if dir == "" {
		t.Skip("нет JARVIS_KWS_DIR")
	}
	bin, err := os.ReadFile(filepath.Join(dir, "wake.bin"))
	if err != nil {
		t.Fatal(err)
	}
	var tv struct {
		PCM   []int16   `json:"pcm"`
		Probs []float64 `json:"probs"`
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "testvec.json"))
	if err := json.Unmarshal(raw, &tv); err != nil {
		t.Fatal(err)
	}
	k, err := LoadKWS(bin, func() int { return 5 })
	if err != nil {
		t.Fatal(err)
	}
	k.cooldown = 0
	var got []float64
	t0 := time.Now()
	for i := 0; i < len(tv.PCM); i += 320 {
		end := min(i+320, len(tv.PCM))
		k.mu.Lock()
		for _, s := range tv.PCM[i:end] {
			k.buf = append(k.buf, float64(s)/32768)
		}
		for len(k.buf) >= k.h.Win {
			k.frame(k.buf[:k.h.Win])
			got = append(got, float64(k.smooth[(k.si-1)%3]))
			k.buf = k.buf[k.h.Hop:]
		}
		k.mu.Unlock()
	}
	el := time.Since(t0)
	if len(got) != len(tv.Probs) {
		t.Fatalf("кадров: Go %d, Python %d", len(got), len(tv.Probs))
	}
	maxd, peakGo, peakPy := 0.0, 0.0, 0.0
	for i := range got {
		maxd = math.Max(maxd, math.Abs(got[i]-tv.Probs[i]))
		peakGo, peakPy = math.Max(peakGo, got[i]), math.Max(peakPy, tv.Probs[i])
	}
	audio := float64(len(tv.PCM)) / 16000
	t.Logf("кадров %d, макс. расхождение %.5f, пик Go %.3f / Python %.3f; %.1f с звука за %v (%.1f%% реального времени на ПК)",
		len(got), maxd, peakGo, peakPy, audio, el.Round(time.Millisecond), 100*el.Seconds()/audio)
	if maxd > 0.01 {
		t.Fatalf("Go и Python расходятся: %.4f", maxd)
	}
}
