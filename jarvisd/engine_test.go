package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Неуверенное срабатывание: разговор с моделью не открывается, пока быстрая расшифровка не подтвердит имя.
// Проверяем ветки, которым модель не нужна вовсе: ложное срабатывание и «стоп».
func TestConverseVerifiesNameBeforeOpening(t *testing.T) {
	dir := t.TempDir()
	store, err := OpenStore(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = store.Update(func(s *Settings) error { s.OpenAIKey = "sk-test-not-real"; return nil })
	newEngine := func() *Engine {
		return &Engine{store: store, proxy: &Proxy{}, notice: func(context.Context, string) {}, player: NewPlayer(), leds: NewLeds(), journal: &Journal{},
			tools: NewTools(func() string { return "" }, DirectClient), stt: &STT{}, dataDir: dir}
	}
	run := func(e *Engine, phrase string) time.Duration {
		quick := make(chan string, 1)
		quick <- phrase
		t0 := time.Now()
		done := make(chan struct{})
		go func() {
			e.converse(true, false, make([]int16, MicRate), make([]int16, 3*MicRate), quick)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatalf("%q: разговор не закончился сам", phrase)
		}
		return time.Since(t0)
	}

	e := newEngine()
	run(e, "Мне не нравится интерфейс разговора.")
	if e.live != nil {
		t.Error("ложное срабатывание не должно открывать разговор с моделью")
	}
	if n := len(e.journal.Items(10)); n != 0 {
		t.Errorf("ложное срабатывание не должно попадать в переписку, записей: %d", n)
	}
	if files, _ := filepath.Glob(filepath.Join(dir, "false", "*.wav")); len(files) != 1 {
		t.Errorf("клип ложного срабатывания должен сохраниться для дообучения, файлов: %d", len(files))
	}

	e = newEngine()
	run(e, "Чарлис, стоп!") // так расшифровка слышит «Джарвис, стоп»
	items := e.journal.Items(10)
	if len(items) != 1 || items[0].Route != "стоп" || items[0].Text != "Чарлис, стоп!" {
		t.Errorf("«стоп» должен выполниться без модели и попасть в переписку: %+v", items)
	}
	if e.live != nil {
		t.Error("для «стоп» разговор с моделью не нужен")
	}

	// Пауза должна остаться в нашем проигрывателе и не уйти Алисе или голосовой модели.
	e = newEngine()
	canceled := false
	e.music = &Music{cancel: func() { canceled = true }, cur: Track{Title: "Test", Author: "Artist"},
		st: &ytStream{dur: 200}, pos: 40}
	run(e, "Джарвис, поставь на паузу")
	if !canceled || !e.music.paused || !e.music.Active() || e.music.Playing() || e.live != nil {
		t.Fatal("пауза должна сохранить активную дорожку без голосовой модели")
	}
	e.TextTurn("перемотай вперед на 30 секунд", false)
	if e.music.pos != 70 || !e.music.paused {
		t.Fatalf("перемотка на паузе: pos=%v paused=%v", e.music.pos, e.music.paused)
	}
	e.TextTurn("что сейчас играет", false)
	items = e.journal.Items(10)
	if len(items) == 0 || items[0].Route != "музыка" || !strings.Contains(items[0].Answer, "YouTube:") {
		t.Fatalf("статус должен отвечать про YouTube: %+v", items)
	}

	// «Стоп» из настроек или «Алиса», пока имя ещё проверяется, — разговор не открывается
	e = newEngine()
	e.busy = true
	quick := make(chan string)
	done := make(chan struct{})
	go func() { e.converse(true, false, nil, nil, quick); close(done) }()
	time.Sleep(150 * time.Millisecond)
	e.Stop("тест")
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("остановка во время проверки имени не сработала")
	}
}
