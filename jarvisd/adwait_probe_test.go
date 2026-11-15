package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

// Начало песни без ожидания рекламы: VR-ссылка, затем переход на основную.
// JARVIS_NET=1 go test -run TestAdSkipStart -v
func TestAdSkipStart(t *testing.T) {
	if os.Getenv("JARVIS_NET") == "" {
		t.Skip("нужна сеть")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	m := NewMusic(func() *http.Client { return client }, func() float64 { return 1 })
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	for _, q := range []string{"Imagine Dragons Believer", "Макс Корж Свой дом"} {
		tracks, err := m.search(ctx, q)
		if err != nil || len(tracks) == 0 {
			t.Fatal(err)
		}
		st, _, err := m.audioURL(ctx, tracks[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		t0 := time.Now()
		ad := time.Until(st.ready)
		src, w, err := m.openAudio(ctx, tracks[0].ID, st, 0)
		if err != nil {
			t.Fatal(err)
		}
		usedHead := src.main != nil
		first := time.Duration(0)
		var last int64
		for {
			_, ms, err := w.next()
			if err == io.EOF || errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			if err != nil {
				t.Fatalf("%s: на %d мс: %v", q, last, err)
			}
			if first == 0 {
				first = time.Since(t0)
			}
			last = ms
		}
		src.Close()
		t.Logf("%s: реклама %.1f с, первый кадр через %.2f с, прочитано до %d с из %.0f, перешёл на основную: %v",
			q, ad.Seconds(), first.Seconds(), last/1000, st.dur, src.main == nil)
		if last/1000 < int64(st.dur)-3 {
			t.Errorf("%s: дорожка не дочитана", q)
		}
		if usedHead && ad > 2*time.Second && first >= ad {
			t.Errorf("%s: первый кадр пришёл только после рекламного таймера", q)
		}
		if usedHead && src.main != nil {
			t.Errorf("%s: не перешёл на основную ссылку", q)
		}
	}
}
