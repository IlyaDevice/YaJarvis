package main

import (
	"context"
	"testing"
)

func TestRecommendationIntentAndFeed(t *testing.T) {
	for _, s := range []string{"Джарвис, включи что-нибудь интересное", "включи стендап из моих рекомендаций", "поставь мои рекомендации YouTube"} {
		if !recommendationRequest(s) {
			t.Errorf("missed %s", s)
		}
	}
	for _, s := range []string{"расскажи что-нибудь интересное", "включи свет", "найди рецепт интересного супа"} {
		if recommendationRequest(s) {
			t.Errorf("wrongly routed %s", s)
		}
	}
	card := func(id, title string) any {
		return map[string]any{"videoRenderer": map[string]any{"videoId": id, "title": map[string]any{"simpleText": title}, "ownerText": map[string]any{"simpleText": "канал"}}}
	}
	feed := []any{map[string]any{"adSlotRenderer": card("advert12345", "Реклама")}, card("video123456", "Стендап"), card("video123457", "Апвоут"), card("video123456", "Повтор")}
	got := recommendationTracks(feed)
	if len(got) != 2 || got[0].Title != "Стендап" || got[1].Author != "канал" {
		t.Fatalf("unexpected feed %+v", got)
	}
	filtered := recommendationFilter("включи стендап из рекомендаций", got)
	if len(filtered) != 1 || filtered[0].ID != "video123456" {
		t.Fatalf("filter %+v", filtered)
	}
	concert := recommendationFilter("включи стендап из моих рекомендаций", []Track{{ID: "concert1234", Title: "Спасибо, у меня всё", Author: "Слава Комиссаренко"}})
	if len(concert) != 1 {
		t.Fatal("standup channel without genre in title must remain eligible")
	}
	m := NewMusic(nil, nil)
	if _, err := m.recommendations(context.Background()); err == nil {
		t.Fatal("must not pretend anonymous results are personal")
	}
	m.personal = true
	m.results = got
	m.mix = []Track{{ID: "unrelated"}}
	next, ok := m.popNext(context.Background())
	if !ok || next.ID != "video123456" {
		t.Fatal("personal queue must precede related radio")
	}
}
