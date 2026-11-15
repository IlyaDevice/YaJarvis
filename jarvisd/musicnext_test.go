package main

import (
	"context"
	"testing"
)

// у «личного» видео без подобранных рекомендаций «дальше» — похожее, а не пусто (кнопка «Следующий» активна)
func TestPersonalNextFallsBackToMix(t *testing.T) {
	m := &Music{cur: Track{ID: "seed"}, personal: true, mix: []Track{{ID: "mix1"}, {ID: "mix2"}}}
	if nx := m.upNext(5); len(nx) != 2 || nx[0].ID != "mix1" {
		t.Fatalf("upNext = %v", nx)
	}
	m.results, m.nextSeed = []Track{{ID: "rec1"}}, "seed"
	if nx := m.upNext(5); len(nx) != 1 || nx[0].ID != "rec1" {
		t.Fatalf("рекомендации под видео идут первыми: %v", nx)
	}
	if tr, ok := m.popNext(context.Background()); !ok || tr.ID != "rec1" {
		t.Fatalf("popNext = %v %v", tr, ok)
	}
	if tr, ok := m.popNext(context.Background()); !ok || tr.ID != "mix1" {
		t.Fatalf("после рекомендаций — похожее: %v %v", tr, ok)
	}
}
