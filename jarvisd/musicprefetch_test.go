package main

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// «дальше» берёт заранее подготовленную ссылку и в сеть не ходит.
func TestNextUsesPrefetched(t *testing.T) {
	played := make(chan string, 1)
	m := &Music{
		client: func() *http.Client { t.Fatal("в сеть ходить не должны"); return nil },
		playback: func(ctx context.Context, gen int, id string, st *ytStream, sec float64) error {
			played <- id
			<-ctx.Done()
			return nil
		},
	}
	m.queue = []Track{{ID: "next1", Title: "Следующий"}}
	m.pre = preStream{id: "next1", st: &ytStream{dur: 100}, info: Track{ID: "next1", Title: "Следующий"}, at: time.Now()}
	tr, err := m.Next(context.Background())
	if err != nil || tr.ID != "next1" {
		t.Fatalf("Next: %+v %v", tr, err)
	}
	select {
	case id := <-played:
		if id != "next1" {
			t.Fatalf("заиграло %s", id)
		}
	case <-time.After(time.Second):
		t.Fatal("не заиграло")
	}
	if m.pre.st != nil {
		t.Fatal("подготовленная ссылка должна забираться один раз")
	}
	m.Stop()
	// устаревшая ссылка не берётся
	m.pre = preStream{id: "x", st: &ytStream{}, at: time.Now().Add(-2 * prefetchTTL)}
	if _, _, ok := m.takePrefetched("x"); ok {
		t.Fatal("устаревшая ссылка взята")
	}
}
