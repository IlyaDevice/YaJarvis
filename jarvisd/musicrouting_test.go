package main

import (
	"context"
	"testing"
	"time"
)

func TestMusicButtonPauseResume(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Music{cancel: cancel, st: &ytStream{}, pos: 42}
	started := make(chan struct{})
	m.playback = func(ctx context.Context, _ int, _ string, _ *ytStream, pos float64) error {
		if pos != 42 {
			t.Errorf("продолжение с %v вместо 42", pos)
		}
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}
	e := &Engine{music: m, alice: NewAliceMusic(nil)}
	defer buttonAt.Store(0)
	defer m.Stop()
	e.playPauseButton()
	if ctx.Err() == nil || m.PausedSince().IsZero() || m.Playing() {
		t.Fatal("кнопка не остановила YouTube с сохранением позиции")
	}
	e.playPauseButton()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("кнопка не продолжила YouTube")
	}
}

func TestAliceStartPausesYouTubeAndUpdatesPanel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m := &Music{cancel: cancel}
	a := NewAliceMusic(nil)
	a.state = "playing"
	e := &Engine{music: m, alice: a}
	buttonAt.Store(0)
	e.aliceStarted()
	if ctx.Err() == nil || m.Playing() {
		t.Fatal("при запуске Алисы YouTube продолжил играть")
	}
	w := &Web{engine: e}
	if got := w.musicNow(); got.Source != "yandex" || !got.Playing {
		t.Fatalf("панель показывает неверный источник: %+v", got)
	}
}

func TestYandexMusicRouting(t *testing.T) {
	for _, text := range []string{"включи музыку", "Джарвис, поставь песню Белого", "запусти радио", "включи плейлист"} {
		if _, ok := AliceSkillFor(text, false); ok {
			t.Errorf("выключенная Яндекс Музыка получила %q", text)
		}
		if _, ok := AliceSkillFor(text, true); !ok {
			t.Errorf("включенная Яндекс Музыка не получила %q", text)
		}
	}
	for _, text := range []string{"поставь таймер на пять минут", "включи будильник", "пауза", "громче"} {
		if _, ok := AliceSkillFor(text, false); !ok {
			t.Errorf("отключение музыки затронуло команду %q", text)
		}
	}
}

func TestAliceMusicResumeNotifies(t *testing.T) {
	a := NewAliceMusic(nil)
	a.id, a.state = "123", "paused"
	started := make(chan struct{}, 2)
	a.OnStart = func() { started <- struct{}{} }
	a.onMedia("[audio stream=123] Notification: Resumed")
	if !a.Playing() {
		t.Fatal("состояние не обновилось")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("нет уведомления о продолжении")
	}
	a.onMedia("[audio stream=123] Notification: Playing")
	select {
	case <-started:
		t.Fatal("повторное уведомление запустило обработчик ещё раз")
	default:
	}
}
