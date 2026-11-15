package main

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestSTTPrerollBeforeLiveFrames(t *testing.T) {
	cleared := make(chan struct{})
	got := make(chan []map[string]any, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
		defer cancel()
		var events []map[string]any
		for i := 0; i < 4; i++ {
			_, raw, err := ws.Read(ctx)
			if err != nil {
				return
			}
			var ev map[string]any
			_ = json.Unmarshal(raw, &ev)
			events = append(events, ev)
			if i == 0 {
				close(cleared)
			}
		}
		got <- events
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ws, _, err := websocket.Dial(ctx, srv.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	s := &STT{ws: ws}
	fed := make(chan struct{})
	go func() { <-cleared; s.Feed([]int16{20, 20}); close(fed) }()
	if _, err := s.Begin([]int16{10, 10}); err != nil {
		t.Fatal(err)
	}
	<-fed
	s.End()
	select {
	case events := <-got:
		if str(events[0]["type"]) != "input_audio_buffer.clear" || str(events[3]["type"]) != "input_audio_buffer.clear" {
			t.Fatalf("unexpected boundaries: %+v", events)
		}
		for i, want := range []int16{100, 200} {
			b, err := base64.StdEncoding.DecodeString(str(events[i+1]["audio"]))
			if err != nil || len(b) < 2 || int16(binary.LittleEndian.Uint16(b)) != want {
				t.Fatalf("audio order %d: want %d, got %v, %v", i, want, b, err)
			}
		}
	case <-ctx.Done():
		t.Fatal("did not receive ordered audio")
	}
}
