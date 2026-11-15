package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type youtubeTransport func(*http.Request) (*http.Response, error)

func (f youtubeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestYouTubeGeneralSearch(t *testing.T) {
	video := func(id, title string) any {
		return map[string]any{"videoRenderer": map[string]any{"videoId": id,
			"title":     map[string]any{"runs": []any{map[string]any{"text": title}}},
			"ownerText": map[string]any{"simpleText": "Uploader"}}}
	}
	items := []any{
		map[string]any{"adSlotRenderer": video("ad", "Ad")},
		video("first", "Remix uploaded as a video"),
		map[string]any{"channelRenderer": map[string]any{"channelId": "channel"}},
		video("second", "A spoken video"), video("first", "Duplicate"),
	}
	section := map[string]any{"contents": []any{map[string]any{"itemSectionRenderer": map[string]any{"contents": items}}}}
	primary := map[string]any{"sectionListRenderer": section}
	fixture := map[string]any{"contents": map[string]any{"twoColumnSearchResultsRenderer": map[string]any{"primaryContents": primary}}}
	client := &http.Client{Transport: youtubeTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "www.youtube.com" {
			t.Fatalf("search must use regular YouTube, got %s", r.URL.Host)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		ctx := body["context"].(map[string]any)["client"].(map[string]any)
		if ctx["clientName"] != "WEB" || body["params"] != nil { // как на сайте, без фильтра — иначе YouTube не исправляет опечатки
			t.Fatalf("unexpected search config: %v", body)
		}
		b, _ := json.Marshal(fixture)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(b)))}, nil
	})}
	m := NewMusic(func() *http.Client { return client }, func() float64 { return 1 })
	tracks, err := m.search(context.Background(), "a remix")
	if err != nil || len(tracks) != 2 || tracks[0].ID != "first" || tracks[1].ID != "second" || tracks[1].Author != "Uploader" {
		t.Fatalf("organic videos must keep their search order: %+v, %v", tracks, err)
	}
}

func TestYouTubeGeneralSearchLive(t *testing.T) {
	if os.Getenv("JARVIS_NET") == "" {
		t.Skip("JARVIS_NET required")
	}
	client := &http.Client{Timeout: 20 * time.Second}
	m := NewMusic(func() *http.Client { return client }, func() float64 { return 1 })
	for _, query := range []string{"гимн твича апвоут", "NASA Perseverance landing footage"} {
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
		tracks, err := m.search(ctx, query)
		cancel()
		if err != nil || len(tracks) == 0 {
			t.Fatalf("%s: %+v %v", query, tracks, err)
		}
		t.Logf("%s: %+v", query, tracks[:min(4, len(tracks))])
	}
}
