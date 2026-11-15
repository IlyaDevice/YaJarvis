package main

import (
	"testing"
	"time"
)

func TestGeminiCandidatesAcrossKeys(t *testing.T) {
	keys := []string{"key-one-test", "key-two-test"}
	for _, m := range GeminiModels {
		markGeminiSpent("key-one-test", m)
	}
	c := geminiCandidates(keys, "gemini-3.8-live")
	if len(c) != len(GeminiModels) || c[0].key != "key-two-test" || c[0].model != "gemini-3.8-live" {
		t.Fatalf("лимит первого ключа кончился — начинать со второго: %v", c)
	}
	markGeminiSpent("key-two-test", "gemini-3.8-live")
	if c := geminiCandidates(keys, "gemini-3.8-live"); c[0].model == "gemini-3.8-live" {
		t.Fatalf("исчерпанную модель второго ключа пропускаем: %v", c)
	}
	st := GeminiKeysState(keys)
	if st[0]["spent"] != len(GeminiModels) || st[1]["spent"] != 1 {
		t.Fatalf("состояние для панели: %v", st)
	}
}

// Лимит выбранной модели на первом ключе — сначала та же модель на остальных ключах, а не модель хуже.
func TestGeminiSameModelOtherKeysFirst(t *testing.T) {
	keys := []string{"k-a-test", "k-b-test", "k-c-test"}
	markGeminiSpent("k-a-test", "gemini-3.8-live")
	c := geminiCandidates(keys, "gemini-3.8-live")
	if c[0] != (geminiTry{"k-b-test", "gemini-3.8-live"}) || c[1] != (geminiTry{"k-c-test", "gemini-3.8-live"}) {
		t.Fatalf("сначала 3.8 на ключах 2 и 3: %v", c[:3])
	}
	if c[2].model == "gemini-3.8-live" {
		t.Fatalf("после всех ключей 3.8 — следующая модель: %v", c[2])
	}
}

// Лимит снова кончился сразу после часа отдыха — ждём дневного сброса (08:00 UTC), а не ещё час.
func TestGeminiRestUntilDailyReset(t *testing.T) {
	now := time.Date(2026, 11, 15, 17, 0, 0, 0, time.UTC)
	if got := geminiRestUntil(nil, now); got != now.Add(time.Hour) {
		t.Fatalf("первый раз — час: %v", got)
	}
	if got := geminiRestUntil(now.Add(-10*time.Minute), now); got != time.Date(2026, 11, 16, 8, 0, 0, 0, time.UTC) {
		t.Fatalf("второй раз подряд — до 08:00 UTC: %v", got)
	}
	if got := geminiRestUntil(now.Add(-5*time.Hour), now); got != now.Add(time.Hour) {
		t.Fatalf("давно отдыхала — снова час: %v", got)
	}
	late := time.Date(2026, 11, 16, 7, 30, 0, 0, time.UTC) // до сброса меньше часа — просто час
	if got := geminiRestUntil(late.Add(-time.Minute), late); got != late.Add(time.Hour) {
		t.Fatalf("перед самым сбросом — час: %v", got)
	}
}
