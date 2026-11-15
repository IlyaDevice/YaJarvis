package main

import (
	"context"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// Видео 18+ с куками аккаунта: JARVIS_NET=1 JARVIS_COOKIES=путь JARVIS_QUERY=… go test -run TestMusicAgeLive -v
func TestMusicAgeLive(t *testing.T) {
	if os.Getenv("JARVIS_NET") == "" || os.Getenv("JARVIS_QUERY") == "" {
		t.Skip("нужны JARVIS_NET и JARVIS_QUERY")
	}
	client := &http.Client{Timeout: 60 * time.Second}
	m := NewMusic(func() *http.Client { return client }, func() float64 { return 1 })
	m.CookieFile = os.Getenv("JARVIS_COOKIES")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var tracks []Track
	var err error
	if ids := os.Getenv("JARVIS_IDS"); ids != "" {
		for _, id := range strings.Split(ids, ",") {
			tracks = append(tracks, Track{ID: id})
		}
	} else if tracks, err = m.search(ctx, os.Getenv("JARVIS_QUERY")); err != nil || len(tracks) == 0 {
		t.Fatalf("поиск: %v", err)
	}
	for _, tr := range tracks[:min(3, len(tracks))] {
		st, _, err := m.audioURL(ctx, tr.ID)
		if err != nil {
			t.Logf("%s «%s»: %v", tr.ID, tr.Title, err)
			continue
		}
		_ = sleepCtx(ctx, time.Until(st.ready))
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, st.url, nil)
		req.Header.Set("User-Agent", st.ua)
		req.Header.Set("Range", "bytes=1500000-1565535")
		resp, err := client.Do(req)
		code := 0
		if err == nil {
			code = resp.StatusCode
			resp.Body.Close()
		}
		t.Logf("%s «%s»: клиент %s, %d байт, кусок после 1,5 МБ: %d %v", tr.ID, tr.Title, st.client, st.length, code, err)
	}
}
