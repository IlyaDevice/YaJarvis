package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"
)

// Какие ссылки отдают звук дальше мегабайта: JARVIS_NET=1 go test -run TestMusicURLAB -v
func TestMusicURLAB(t *testing.T) {
	if os.Getenv("JARVIS_NET") == "" {
		t.Skip("нужна сеть: JARVIS_NET=1")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	m := NewMusic(func() *http.Client { return client }, func() float64 { return 1 })
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	for i := 0; i < 10; i++ {
		st, _, err := m.audioURL(ctx, "Kx7B-XvmFtE")
		if err != nil {
			t.Logf("%d: %v", i, err)
			continue
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, st.url, nil)
		req.Header.Set("User-Agent", st.ua)
		req.Header.Set("Range", "bytes=1500000-1565535")
		resp, err := client.Do(req)
		code := 0
		if err == nil {
			code = resp.StatusCode
			resp.Body.Close()
		}
		u, _ := url.Parse(st.url)
		q := u.Query()
		var keys []string
		for k := range q {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		t.Logf("%d: %s → %d; c=%s n=%d sparams=%s ключи=%s", i, st.client, code, q.Get("c"), len(q.Get("n")), q.Get("sparams"), strings.Join(keys, ","))
		_ = fmt.Sprint()
		time.Sleep(time.Second)
	}
}
