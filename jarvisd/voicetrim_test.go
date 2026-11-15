package main

import (
	"math"
	"testing"
)

func TestVoiceTrim(t *testing.T) {
	db := func(x float64) float64 { return 20 * math.Log10(x) }
	if voiceTrim(0) != 1 || voiceTrim(3) != 1 {
		t.Fatal("на тихой колонке усиление не трогаем")
	}
	if d := db(voiceTrim(10)); math.Abs(d+14) > 0.01 {
		t.Fatalf("на 10 снимаем 14 дБ, а не %.1f", d)
	}
	// голос всё равно громче с каждым уровнем: Master +5.1 дБ, поправка −2 дБ
	for l := 3.0; l < 10; l++ {
		if 5.1+db(voiceTrim(l+1))-db(voiceTrim(l)) <= 0 {
			t.Fatalf("на уровне %.0f голос стал тише", l+1)
		}
	}
}
