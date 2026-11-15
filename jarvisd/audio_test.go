package main

import (
	"math"
	"testing"
)

// Ограничитель: тихое усиливается как есть, громкое не выходит за шкалу и не «рвётся».
func TestLimiter(t *testing.T) {
	l := newLimiter(MicRate)
	var peak float64
	var prev int16
	maxStep := 0.0
	for i := 0; i < MicRate; i++ {
		amp := 3000.0 // тихий участок
		if i > MicRate/2 {
			amp = 80000 // после усиления был бы далеко за шкалой
		}
		y := l.next(amp * math.Sin(2*math.Pi*220*float64(i)/MicRate))
		peak = math.Max(peak, math.Abs(float64(y)))
		if i > MicRate/2+400 {
			maxStep = math.Max(maxStep, math.Abs(float64(y)-float64(prev)))
		}
		prev = y
	}
	if peak > 32767 || peak < 26000 {
		t.Errorf("пик после ограничителя %.0f — должен быть у потолка, но в шкале", peak)
	}
	if maxStep > 4000 { // синус 220 Гц у потолка меняется не больше ~2600 за отсчёт; рывок — признак среза
		t.Errorf("скачок между отсчётами %.0f — звук рвётся", maxStep)
	}
	q := newLimiter(MicRate)
	var qp float64
	for i := 0; i < 2000; i++ {
		qp = math.Max(qp, math.Abs(float64(q.next(3000*math.Sin(2*math.Pi*220*float64(i)/MicRate)))))
	}
	if qp < 2900 || qp > 3001 {
		t.Errorf("тихий звук должен проходить без изменений, пик %.0f", qp)
	}
}
