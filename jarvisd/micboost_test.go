package main

import "testing"

func TestBoostPCM(t *testing.T) {
	in := []int16{1000, -1000, 30000}
	out := boostPCM(in, 6)
	if out[0] < 1990 || out[0] > 2000 || out[1] > -1990 || out[2] != 32767 {
		t.Fatalf("+6 дБ ≈ ×2 с ограничением: %v", out)
	}
	if in[0] != 1000 {
		t.Fatal("исходный звук не трогаем — он идёт слову активации")
	}
	if &boostPCM(in, 0)[0] != &in[0] {
		t.Fatal("0 дБ — без копии")
	}
}
