package main

import (
	"math"
	"math/cmplx"
	"testing"
)

// Синус 1 кГц из 24 кГц в 48 кГц: амплитуда та же, «зеркало» на 23 кГц подавлено, стыки кусков не слышны.
func TestUpsampler(t *testing.T) {
	in := make([]int16, 2400)
	for i := range in {
		in[i] = int16(10000 * math.Sin(2*math.Pi*1000*float64(i)/24000))
	}
	whole := newUpsampler(2).run(in)
	u := newUpsampler(2)
	var parts []int16
	for off := 0; off < len(in); off += 333 {
		parts = append(parts, u.run(in[off:min(len(in), off+333)])...)
	}
	if len(whole) != 4800 || len(parts) != 4800 {
		t.Fatalf("длина %d/%d, ждали 4800", len(whole), len(parts))
	}
	for i := range whole {
		if whole[i] != parts[i] {
			t.Fatalf("кусками и целиком расходятся на отсчёте %d", i)
		}
	}
	tone := func(f float64) float64 { // амплитуда частоты f на установившемся участке
		var c complex128
		seg := whole[480:4320]
		for i, v := range seg {
			c += complex(float64(v), 0) * cmplx.Exp(complex(0, -2*math.Pi*f*float64(i)/48000))
		}
		return 2 * cmplx.Abs(c) / float64(len(seg))
	}
	if a := tone(1000); a < 9700 || a > 10300 {
		t.Fatalf("амплитуда 1 кГц %.0f, ждали ~10000", a)
	}
	if img := tone(23000); img > 10 {
		t.Fatalf("зеркало на 23 кГц %.1f — фильтр не подавляет", img)
	}
	if len(newUpsampler(3).run(make([]int16, 160))) != 480 {
		t.Fatal("16 → 48 кГц: втрое больше отсчётов")
	}
}

// Общее усиление микрофона: тихое усиливается точно, громкое не переворачивается через край.
func TestGainPCM(t *testing.T) {
	pcm := []int16{100, -100, 20000, -20000, 0}
	gainPCM(pcm, 12)
	if pcm[0] < 395 || pcm[0] > 400 || pcm[1] > -395 {
		t.Fatalf("+12 дБ из 100: %d/%d", pcm[0], pcm[1])
	}
	if pcm[2] < 29000 || pcm[3] > -29000 || pcm[4] != 0 {
		t.Fatalf("громкое должно прижаться к потолку: %v", pcm)
	}
	same := []int16{123}
	gainPCM(same, 0)
	if same[0] != 123 {
		t.Fatal("0 дБ — без изменений")
	}
}
