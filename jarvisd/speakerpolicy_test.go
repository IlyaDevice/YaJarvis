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

func TestListeningSelection(t *testing.T) {
	var modern map[string]any
	if err := json.Unmarshal([]byte(`{"lockupViewModel":{"contentType":"LOCKUP_CONTENT_TYPE_VIDEO","contentId":"modern12345","metadata":{"lockupMetadataViewModel":{"title":{"content":"Стендап концерт"},"metadata":{"contentMetadataViewModel":{"metadataRows":[{"metadataParts":[{"text":{"content":"Автор"}}]}]}}}}}}`), &modern); err != nil {
		t.Fatal(err)
	}
	cards := recommendationTracks(modern)
	if len(cards) != 1 || cards[0].Author != "Автор" || cards[0].Title != "Стендап концерт" {
		t.Fatalf("modern TV card: %+v", cards)
	}
	in := []Track{{ID: "visual", Title: "Я попробовал RTX в Minecraft"}, {ID: "story", Title: "Истории о жизни"}, {ID: "standup", Title: "Спасибо у меня всё", Author: "Слава Комиссаренко"}, {ID: "music", Title: "Song official video"}, {ID: "upvote", Title: "Сегодня я узнал", Author: "апвоут"}, {ID: "unknown", Title: "Невероятный эксперимент"}}
	got := recommendationFilter("включи что-нибудь интересное", in)
	if len(got) != 3 || got[0].ID != "standup" || got[1].ID != "upvote" {
		t.Fatalf("not suitable for listening: %+v", got)
	}
}
func TestChatterDoesNotBlockCompletedWork(t *testing.T) {
	var lines []string
	e := &Engine{notice: func(_ context.Context, s string) { lines = append(lines, s) }}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	answer := e.mediaWithChatter(ctx, func() string { time.Sleep(20 * time.Millisecond); return "готово" })
	if answer != "готово" || ctx.Err() != nil || len(lines) != 1 || strings.TrimSpace(lines[0]) == "" {
		t.Fatalf("answer=%s lines=%v", answer, lines)
	}
}
func TestAccountSearchUsesSignedInRequest(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Update(func(s *Settings) error { s.YouTubeRefresh = "test-refresh"; return nil })
	client := &http.Client{Transport: youtubeTransport(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer test-access" || r.URL.Path != "/youtubei/v1/search" {
			t.Fatalf("wrong account search: %s", r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["query"] != "стендап полный концерт" {
			t.Fatalf("wrong query %v", body["query"])
		}
		raw := `{"contents":{"items":[{"videoRenderer":{"videoId":"standup1234","title":{"simpleText":"Стендап концерт"}}}]}}`
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(raw))}, nil
	})}
	m := NewMusic(func() *http.Client { return client }, nil)
	m.Account = NewYTAccount(store, func() *http.Client { return client })
	m.Account.access = "test-access"
	m.Account.exp = time.Now().Add(time.Hour)
	got, err := m.accountSearch(context.Background(), "включи стендап из моих рекомендаций")
	if err != nil || len(got) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
}
