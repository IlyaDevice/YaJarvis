package main

import (
	"context"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

// Какие запросы googlevideo отдаёт, а какие нет: JARVIS_NET=1 go test -run TestMusicDownloadVariants -v
func TestMusicDownloadVariants(t *testing.T) {
	if os.Getenv("JARVIS_NET") == "" {
		t.Skip("нужна сеть: JARVIS_NET=1")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	m := NewMusic(func() *http.Client { return client }, func() float64 { return 1 })
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for _, id := range []string{"Kx7B-XvmFtE", "7wtfhZwyrcc"} {
		st, _, err := m.audioURL(ctx, id)
		if err != nil {
			t.Logf("%s: %v", id, err)
			continue
		}
		for _, v := range []struct {
			name, rng, enc string
		}{{"64К", "bytes=0-65535", ""}, {"1М", "bytes=0-1048575", ""}, {"1М identity", "bytes=0-1048575", "identity"}, {"64К identity", "bytes=0-65535", "identity"}} {
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, st.url, nil)
			req.Header.Set("User-Agent", st.ua)
			req.Header.Set("Range", v.rng)
			if v.enc != "" {
				req.Header.Set("Accept-Encoding", v.enc)
			}
			resp, err := client.Do(req)
			if err != nil {
				t.Logf("%s %s: %v", id, v.name, err)
				continue
			}
			n, _ := io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			t.Logf("%s %s: %d, %d байт", id, v.name, resp.StatusCode, n)
		}
	}
}
