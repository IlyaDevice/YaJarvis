package main

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestProposalRequiresExplicitAcceptance(t *testing.T) {
	started := make(chan string, 2)
	m := &Music{client: func() *http.Client { t.Fatal("unexpected network"); return nil }, playback: func(ctx context.Context, _ int, id string, _ *ytStream, _ float64) error {
		started <- id
		<-ctx.Done()
		return nil
	}}
	m.proposal = &mediaProposal{items: []Track{{ID: "first", Title: "Первый рассказ"}, {ID: "second", Title: "Второй рассказ"}}, expires: time.Now().Add(time.Minute)}
	if m.Playing() || len(m.queue) != 0 || len(m.Snapshot().Next) != 0 {
		t.Fatal("proposal must not start or enqueue audio")
	}
	answer, ok := m.respondProposal(context.Background(), "хуйня")
	if !ok || !strings.Contains(answer, "Второй рассказ") || m.Playing() {
		t.Fatal(answer)
	}
	m.pre = preStream{id: "second", st: &ytStream{dur: 200}, at: time.Now()}
	answer, ok = m.respondProposal(context.Background(), "да")
	if !ok || !strings.HasPrefix(answer, "Включаю:") {
		t.Fatal(answer)
	}
	select {
	case id := <-started:
		if id != "second" {
			t.Fatal(id)
		}
	case <-time.After(time.Second):
		t.Fatal("accepted video did not start")
	}
	if m.hasProposal() || len(m.results) != 0 {
		t.Fatal("unapproved alternatives must not autoplay")
	}
	m.Stop()
}
func TestSelectionNeverBecomesLiteralSearchOrQueue(t *testing.T) {
	m := &Music{client: func() *http.Client { t.Fatal("must not search literal recommendation words"); return nil }}
	for _, q := range []string{"рекомендации YouTube", "рекомендованные", "подбери сам", "выбери что-нибудь послушать"} {
		if !recommendationRequest(q) {
			t.Fatal(q)
		}
		if _, err := m.Enqueue(context.Background(), q); err == nil {
			t.Fatal("must not enqueue " + q)
		}
		if _, err := m.Play(context.Background(), q); err == nil {
			t.Fatal("must require proposal " + q)
		}
	}
	musicCalls, queueCalls := 0, 0
	tools := NewTools(func() string { return "" }, nil)
	tools.Music = func(context.Context, string) string { musicCalls++; return "Послушаем?" }
	tools.MusicQueue = func(context.Context, string) string { queueCalls++; return "queued" }
	tools.Call(context.Background(), "play_music", `{"query":"рекомендации YouTube","queue":true}`)
	if musicCalls != 1 || queueCalls != 0 {
		t.Fatal("model must not turn a selection into queueing")
	}
	m.proposal = &mediaProposal{items: []Track{{ID: "x"}}, expires: time.Now().Add(-time.Second)}
	if m.hasProposal() {
		t.Fatal("expired proposal must never start on yes")
	}
	for _, s := range []string{"я не говорил да", "нет давай другой", "погода завтра"} {
		if proposalReply(s) == "accept" {
			t.Fatal("false acceptance: " + s)
		}
	}
}
