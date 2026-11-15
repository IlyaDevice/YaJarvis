package main

import (
	"encoding/json"
	"math"
	"os"
	"testing"
	"time"
)

// Сверка с обучением на Python (kws/train_oww.py пишет testvec.json): те же вероятности на тот же звук.
func TestOWWMatchesPython(t *testing.T) {
	feat, err1 := os.ReadFile("../models/oww.bin")
	head, err2 := os.ReadFile("../models/wake_oww.bin")
	raw, err3 := os.ReadFile("../kws/out_oww/testvec.json")
	if err1 != nil || err2 != nil || err3 != nil {
		t.Skip("нет файлов модели — сначала train_oww.py")
	}
	var tv struct {
		PCM   []int     `json:"pcm"`
		Probs []float64 `json:"probs"`
	}
	if err := json.Unmarshal(raw, &tv); err != nil {
		t.Fatal(err)
	}
	o, err := LoadOWW(feat, head, func() int { return 5 })
	if err != nil {
		t.Fatal(err)
	}
	o.sensitivity = func() int { return 1 }
	var got []float64
	pcm := make([]int16, len(tv.PCM))
	for i, v := range tv.PCM {
		pcm[i] = int16(v)
	}
	t0 := time.Now()
	steps := 0
	for i := 0; i+owwStep <= len(pcm); i += owwStep {
		before := len(o.ring)
		last := o.LastProb
		o.Feed(pcm[i : i+owwStep])
		steps++
		if len(o.ring) == o.seq && (before == o.seq || o.LastProb != last) {
			got = append(got, float64(o.LastProb))
		}
	}
	el := time.Since(t0)
	t.Logf("%d шагов по 80 мс за %v (%.1f%% реального времени на этом компьютере)", steps, el, 100*el.Seconds()/(float64(steps)*0.08))
	n := min(len(got), len(tv.Probs))
	if n == 0 || abs(len(got)-len(tv.Probs)) > 1 {
		t.Fatalf("число вероятностей: Go %d, Python %d", len(got), len(tv.Probs))
	}
	worst := 0.0
	for i := 0; i < n; i++ {
		worst = math.Max(worst, math.Abs(got[len(got)-n+i]-tv.Probs[len(tv.Probs)-n+i]))
	}
	t.Logf("Go %d, Python %d вероятностей, наибольшее расхождение %.5f, максимум Python %.3f", len(got), len(tv.Probs), worst, maxf(tv.Probs))
	if worst > 0.01 {
		t.Errorf("расчёт на Go расходится с обучением: %.4f", worst)
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func maxf(v []float64) float64 {
	m := 0.0
	for _, x := range v {
		m = math.Max(m, x)
	}
	return m
}
