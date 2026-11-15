package main

import (
	"context"
	"os"
	"testing"
	"time"
)

// JARVIS_VLESS=… OPENAI_API_KEY=… go test -run LiveSession -v
func TestLiveSession(t *testing.T) {
	key := os.Getenv("OPENAI_API_KEY")
	if key == "" {
		t.Skip("нет OPENAI_API_KEY")
	}
	p := &Proxy{}
	if err := p.Apply(os.Getenv("JARVIS_VLESS")); err != nil {
		t.Fatal(err)
	}
	j := &Journal{}
	prof := defaultSettings().Profiles[0]
	prof.Voice = "stone"
	s := NewLiveSession(context.Background(), key, prof, "Ты Джарвис. Говори по-русски.", "", NewTools(func() string { return "" }, DirectClient), p.Client(0), j)
	defer s.Close()
	t0 := time.Now()
	if err := s.Ready(15 * time.Second); err != nil {
		t.Fatal(err)
	}
	t.Logf("сессия открыта за %v", time.Since(t0).Round(time.Millisecond))
	_ = s.Say("Проверка связи. Джарвис на месте.")
	samples, loudN := 0, 0
	var first time.Duration
	deadline := time.After(12 * time.Second)
	t1 := time.Now()
loop:
	for {
		select {
		case pcm, ok := <-s.Out:
			if !ok {
				break loop
			}
			samples += len(pcm)
			if loud(pcm) {
				if first == 0 {
					first = time.Since(t1)
				}
				loudN++
			} else if loudN > 20 && s.Idle(1.2) {
				break loop
			}
		case <-deadline:
			break loop
		}
	}
	s.FinishTurn("")
	items := j.Items(5)
	t.Logf("звук: %.1f с всего, первый голос через %v, громких кусков %d", float64(samples)/MicRate, first.Round(time.Millisecond), loudN)
	for _, it := range items {
		t.Logf("журнал: вопрос %q ответ %q", it.Text, it.Answer)
	}
	if loudN == 0 {
		t.Fatal("голоса не было")
	}
}
