package main

import (
	"errors"
	"strings"
	"testing"
)

func TestSoftPrompt(t *testing.T) {
	text := "Ты — Джарвис, мой братан.\n    Коротко: 1–3 предложения.\n    Используй мат. Матерись\nЧисла пиши словами."
	if softPrompt(text, false) != text {
		t.Fatal("без отказа характер менять нельзя")
	}
	got := softPrompt(text, true)
	if strings.Contains(got, "Матерись") || !strings.Contains(got, "Коротко") || !strings.Contains(got, "Числа пиши словами") {
		t.Fatalf("смягчение: %q", got)
	}
	if !policyRefusal(errors.New("Eleven Realtime: Agent prompt and/or first message seems to violate Prohibited Uses Policy.")) {
		t.Fatal("отказ модерации не распознан")
	}
}
