package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestChatThread(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chat.json")
	c := LoadChatThread(path)
	id := c.Thread()
	if id == "" {
		t.Fatal("нет номера беседы")
	}
	for i := 0; i < chatKeepTurns+5; i++ {
		c.Add("вопрос", strings.Repeat("о", 300))
	}
	c2 := LoadChatThread(path) // после перезапуска — тот же чат
	if c2.Thread() != id || len(c2.Turns) != chatKeepTurns {
		t.Fatalf("после перезапуска: %s %d", c2.Thread(), len(c2.Turns))
	}
	r := c2.Recent(1000)
	if len(r) == 0 || len(r) > 4 {
		t.Fatalf("Recent(1000) = %d реплик", len(r))
	}
	if r[len(r)-1] != c2.Turns[len(c2.Turns)-1] {
		t.Fatal("последняя реплика должна остаться")
	}
	c2.Reset()
	if c2.Thread() == id || len(c2.Recent(chatKeepChars)) != 0 {
		t.Fatal("новый чат не начался")
	}
	var j Journal
	got := 0
	j.OnRemember = func(t Turn) { got++ }
	j.note("x", "привет", "здравствуй")
	if got != 1 {
		t.Fatal("журнал не сообщил о реплике")
	}
}

func TestMemoryWorthy(t *testing.T) {
	if memoryWorthy("Home Assistant недоступен — проверь адрес и включён ли сервер") {
		t.Fatal("ошибку запоминать не надо")
	}
	if !memoryWorthy("Готово: «Mijia Robot Vacuum 5» vacuum.start-sweep") {
		t.Fatal("успешную команду надо запомнить")
	}
}
