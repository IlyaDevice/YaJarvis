package main

import "testing"

func TestParseVolumeLine(t *testing.T) {
	log := []byte("[glagol] [info] [glagol.cpp:1159] Changed volume to 0.7\n[x] other\n[glagol] [info] [glagol.cpp:1159] Changed volume to 0.65\n")
	if v := parseVolumeLine(log); v != 7 { // 0.65 → 6.5 → 7 (округление к ближайшему, как в шкале Алисы)
		t.Fatalf("последняя строка 0.65 → 7, получили %d", v)
	}
	if v := parseVolumeLine([]byte("Changed volume to 0.3")); v != 3 {
		t.Fatalf("0.3 → 3, получили %d", v)
	}
	if v := parseVolumeLine([]byte("нет строки")); v != -1 {
		t.Fatalf("нет строки → -1, получили %d", v)
	}
}
