package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestWatchNextUsesCurrentVideoAndAccount(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Update(func(s *Settings) error { s.YouTubeRefresh = "test-refresh"; return nil })
	requests := 0
	seed := "current0001"
	client := &http.Client{Transport: youtubeTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		if r.Header.Get("Authorization") != "Bearer test-access" || r.URL.Path != "/youtubei/v1/next" {
			t.Fatalf("wrong watch request %s", r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["videoId"] != seed {
			t.Fatalf("wrong seed: %v", body["videoId"])
		}
		if _, ok := body["playlistId"]; ok {
			t.Fatal("watch recommendations must not use a Music RD playlist")
		}
		raw := `{"contents":{"items":[
   {"compactVideoRenderer":{"videoId":"visual00001","title":{"simpleText":"Minecraft RTX gameplay"}}},
   {"compactVideoRenderer":{"videoId":"current0001","title":{"simpleText":"Стендап текущий"}}},
   {"compactVideoRenderer":{"videoId":"watched0001","title":{"simpleText":"Стендап просмотренный"}}},
   {"compactVideoRenderer":{"videoId":"podcast0001","title":{"simpleText":"Подкаст про жизнь"}}},
   {"compactVideoRenderer":{"videoId":"standup0001","title":{"simpleText":"Стендап концерт"}}},
   {"compactVideoRenderer":{"videoId":"standup0002","title":{"simpleText":"Стендап другой концерт"}}}
  ]}}`
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(raw))}, nil
	})}
	m := NewMusic(func() *http.Client { return client }, nil)
	m.Account = NewYTAccount(store, func() *http.Client { return client })
	m.Account.access, m.Account.exp = "test-access", time.Now().Add(time.Hour)
	m.cur = Track{ID: seed}
	m.paused = true
	m.history = []Track{{ID: "watched0001"}}
	m.results = []Track{{ID: "old-result"}}
	m.mix = []Track{{ID: "old-mix"}}
	e := &Engine{music: m}
	answer, ok := e.controlMusicContext(context.Background(), "следующий ролик")
	if !ok || !strings.Contains(answer, "под текущим роликом") || m.proposal == nil || m.proposal.items[0].ID != "standup0001" {
		t.Fatalf("proposal: %s %+v", answer, m.proposal)
	}
	if m.Playing() || len(m.queue) != 0 || len(m.results) != 0 || len(m.mix) != 0 {
		t.Fatal("proposal started or queued unapproved audio")
	}
	answer, _ = m.respondProposal(context.Background(), "дальше")
	if !strings.Contains(answer, "другой концерт") || requests != 1 {
		t.Fatal("rejection must advance within this video's recommendations", answer, requests)
	}
	started := make(chan string, 1)
	m.playback = func(ctx context.Context, _ int, id string, _ *ytStream, _ float64) error {
		started <- id
		<-ctx.Done()
		return nil
	}
	m.pre = preStream{id: "standup0002", st: &ytStream{dur: 300}, at: time.Now()}
	answer, _ = m.respondProposal(context.Background(), "да")
	select {
	case id := <-started:
		if id != "standup0002" {
			t.Fatal(id)
		}
	case <-time.After(time.Second):
		t.Fatal("accepted video did not start")
	}
	m.Pause()
	seed = "standup0002"
	m.proposeRelated(context.Background())
	if requests != 2 {
		t.Fatal("next selection must refresh recommendations for the newly accepted video")
	}
	m.Stop()
}

func TestFinishedSelectionOffersRelatedWithoutAutoplay(t *testing.T) {
	offered := make(chan string, 2)
	m := &Music{cur: Track{ID: "current0001"}, personal: true, OnRelatedEnd: func(id string) { offered <- id }}
	m.ended(0, 300, 300)
	select {
	case id := <-offered:
		if id != "current0001" {
			t.Fatal(id)
		}
	case <-time.After(time.Second):
		t.Fatal("finished video did not offer continuation")
	}
	if !m.paused || m.Playing() || m.cur.ID != "current0001" {
		t.Fatal("must keep the source video without starting another")
	}
	m.ended(0, 300, 300)
	select {
	case <-offered:
		t.Fatal("duplicate end event")
	default:
	}
	if answer := m.proposeRelatedFor(context.Background(), "replaced001"); answer != "" {
		t.Fatal("stale end event should not interrupt a replacement", answer)
	}
}
