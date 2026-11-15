package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestYouTubeProblemFormatsLive(t *testing.T) {
	if os.Getenv("JARVIS_FORMATS") == "" {
		t.Skip("JARVIS_FORMATS required")
	}
	client := &http.Client{Timeout: 30 * time.Second, Transport: youtubeTransport(func(r *http.Request) (*http.Response, error) {
		res, err := http.DefaultTransport.RoundTrip(r)
		if err != nil {
			return res, err
		}
		if r.URL.Path == "/youtubei/v1/player" {
			raw, _ := io.ReadAll(res.Body)
			_ = res.Body.Close()
			res.Body = io.NopCloser(bytes.NewReader(raw))
			var v map[string]any
			_ = json.Unmarshal(raw, &v)
			t.Logf("playability: %v", v["playabilityStatus"])
			sd, _ := v["streamingData"].(map[string]any)
			formats, _ := sd["adaptiveFormats"].([]any)
			for _, f := range formats {
				o, _ := f.(map[string]any)
				t.Logf("format itag=%v mime=%v length=%v", o["itag"], o["mimeType"], o["contentLength"])
			}
		}
		return res, nil
	})}
	m := NewMusic(func() *http.Client { return client }, func() float64 { return 1 })
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	tracks, err := m.search(ctx, "гимн твича")
	if err != nil || len(tracks) == 0 {
		t.Fatal(err)
	}
	for _, tr := range tracks[:min(3, len(tracks))] {
		t.Logf("video %+v", tr)
		st, _, err := m.audioURL(ctx, tr.ID)
		if st != nil {
			t.Logf("selected %s %.0f sec", st.client, st.dur)
		}
		t.Logf("result: %v", err)
	}
}
