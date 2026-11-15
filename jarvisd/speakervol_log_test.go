package main

import "testing"

func TestParseVolumeLine(t *testing.T) {
	log := []byte("[1750:1769] [2026-11-15 16:46:47.692] [glagol] [info] [glagol.cpp:1159] Changed volume to 0.7\n[x] other\n" +
		"[1750:1769] [2026-11-15 16:46:48.007] [glagol] [info] [glagol.cpp:1159] Changed volume to 0.55\n")
	v, stamp := parseVolumeLine(log)
	if v != 5.5 || stamp != "2026-11-15 16:46:48.007" {
		t.Fatalf("последняя строка 0.55 → 5.5 и её время, получили %v %q", v, stamp)
	}
	if v, _ := parseVolumeLine([]byte("нет строки")); v != -1 {
		t.Fatalf("нет строки → -1, получили %v", v)
	}
}
