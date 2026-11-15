package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestGeminiRejectsMusicWithoutCurrentUserSpeech(t *testing.T) {
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		if _, _, err = ws.Read(ctx); err != nil {
			return
		}
		send := func(v any) { b, _ := json.Marshal(v); _ = ws.Write(ctx, websocket.MessageText, b) }
		send(map[string]any{"setupComplete": map[string]any{}})
		send(map[string]any{"toolCall": map[string]any{"functionCalls": []any{map[string]any{"id": "stale-1", "name": "play_music", "args": map[string]any{"query": "стендап из истории"}}}}})
		for {
			_, raw, err := ws.Read(ctx)
			if err != nil {
				return
			}
			if strings.Contains(string(raw), "functionResponses") {
				got <- string(raw)
				return
			}
		}
	}))
	defer srv.Close()
	old := geminiURL
	geminiURL = "ws" + strings.TrimPrefix(srv.URL, "http")
	defer func() { geminiURL = old }()
	sess := NewGeminiSession(context.Background(), "test-key", Profile{Engine: "gemini"}, "История: включи стендап.", NewTools(func() string { return "" }, http.DefaultClient), http.DefaultClient, &Journal{})
	defer sess.Close()
	if err := sess.Ready(time.Second); err != nil {
		t.Fatal(err)
	}
	select {
	case raw := <-got:
		if !strings.Contains(raw, "Не включаю:") {
			t.Fatalf("stale music was not rejected: %s", raw)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("no tool rejection")
	}
}
