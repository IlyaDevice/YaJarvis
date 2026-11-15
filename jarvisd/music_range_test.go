package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

// Как запросить кусок дальше первого мегабайта: JARVIS_NET=1 go test -run TestMusicRangeStyles -v
func TestMusicRangeStyles(t *testing.T) {
	if os.Getenv("JARVIS_NET") == "" {
		t.Skip("нужна сеть: JARVIS_NET=1")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	m := NewMusic(func() *http.Client { return client }, func() float64 { return 1 })
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	st, _, err := m.audioURL(ctx, "Kx7B-XvmFtE")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("клиент %s, размер %d", st.client, st.length)
	try := func(name, url string, hdr string) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		req.Header.Set("User-Agent", st.ua)
		if hdr != "" {
			req.Header.Set("Range", hdr)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Logf("%s: %v", name, err)
			return
		}
		n, _ := io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		t.Logf("%s: %d, %d байт", name, resp.StatusCode, n)
	}
	cpn := "aB3dE6gH9jK2mN5p"
	for _, off := range []int64{0, 1500000} {
		try(fmt.Sprintf("cpn @%d", off), fmt.Sprintf("%s&cpn=%s&range=%d-%d&rn=1&rbuf=0", st.url, cpn, off, off+65535), "")
		try(fmt.Sprintf("cpn+cver @%d", off), fmt.Sprintf("%s&cpn=%s&cver=%s&range=%d-%d", st.url, cpn, ytVRVersion, off, off+65535), "")
		try(fmt.Sprintf("POST ump @%d", off), fmt.Sprintf("%s&cpn=%s&range=%d-%d&ump=1&srfvp=1", st.url, cpn, off, off+65535), "")
	}
}
