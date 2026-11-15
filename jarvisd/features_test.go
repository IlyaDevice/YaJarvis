package main

import (
	"strings"
	"testing"
	"time"
)

func TestQueueRequest(t *testing.T) {
	cases := map[string]string{
		"добавь в очередь Кино группа крови":       "кино группа крови",
		"Джарвис, поставь в очередь с ютуба Queen": "queen",
		"закинь Imagine Dragons в очередь":         "imagine dragons",
		"включи потом песню Сплин":                 "сплин",
		"включи следующую песню":                   "",
		"пауза": "",
	}
	for in, want := range cases {
		got, ok := queueRequest(in)
		if ok != (want != "") || got != want {
			t.Errorf("queueRequest(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
	if _, ok := youtubeRequest("поставь в очередь с ютуба Queen"); ok {
		t.Error("очередь не должна включать сразу")
	}
	if a, _, _, ok := musicCommand("включи похожее"); !ok || a != "radio" {
		t.Errorf("«включи похожее» = %q %v", a, ok)
	}
}

func TestMixTracks(t *testing.T) {
	out := map[string]any{"contents": []any{
		map[string]any{"playlistPanelVideoRenderer": map[string]any{"videoId": "aaaaaaaaaaa",
			"title": map[string]any{"simpleText": "One"}, "shortBylineText": map[string]any{"runs": []any{map[string]any{"text": "A"}}}}},
		map[string]any{"playlistPanelVideoRenderer": map[string]any{"videoId": "bbbbbbbbbbb", "title": map[string]any{"simpleText": "Two"}}},
		map[string]any{"playlistPanelVideoRenderer": map[string]any{"videoId": "aaaaaaaaaaa", "title": map[string]any{"simpleText": "One"}}},
	}}
	got := mixTracks(out)
	if len(got) != 2 || got[0].Author != "A" || got[1].ID != "bbbbbbbbbbb" {
		t.Fatalf("mixTracks = %+v", got)
	}
}

func TestQueueOrder(t *testing.T) {
	m := &Music{Radio: func() bool { return false }}
	m.queue = []Track{{ID: "q1"}, {ID: "q2"}}
	m.results = []Track{{ID: "r1"}}
	m.mix = []Track{{ID: "m1"}}
	if n := m.upNext(10); len(n) != 3 || n[0].ID != "q1" || n[2].ID != "r1" {
		t.Fatalf("upNext без радио = %+v", n)
	}
	m.Radio = nil
	if n := m.upNext(10); len(n) != 4 || n[2].ID != "m1" {
		t.Fatalf("upNext с радио = %+v", n)
	}
	m.Unqueue(0)
	if len(m.queue) != 1 || m.queue[0].ID != "q2" {
		t.Fatalf("Unqueue = %+v", m.queue)
	}
}

func TestNight(t *testing.T) {
	s := defaultSettings()
	s.Timezone = "UTC"
	s.Profiles[0].Timezone = "UTC"
	if s.IsNight() {
		t.Fatal("выключенный ночной режим")
	}
	now := time.Now().UTC()
	hm := func(d time.Duration) string { return now.Add(d).Format("15:04") }
	s.Night = NightSettings{Enabled: true, From: hm(-time.Hour), To: hm(time.Hour), Voice: 30, Bright: 5, Speaker: 3}
	if !s.IsNight() {
		t.Fatal("сейчас должна быть ночь")
	}
	s.Profiles[0].Volume = 90
	if v := s.VoiceVolume(); v != 30 {
		t.Errorf("голос ночью = %d", v)
	}
	if b := s.LedFor().Brightness; b != 5 {
		t.Errorf("яркость ночью = %d", b)
	}
	s.Night.From, s.Night.To = hm(time.Hour), hm(2*time.Hour)
	if s.IsNight() {
		t.Error("ночь ещё не началась")
	}
	n := NightSettings{From: "25:00", Speaker: 50}
	n.fill()
	if n.From != "23:00" || n.Speaker != 10 || n.Voice != 5 {
		t.Errorf("fill = %+v", n)
	}
}

func TestAliceMusicLog(t *testing.T) {
	a := NewAliceMusic(nil)
	a.cache["31411989"] = ymTrack{Title: "False Alarm", Artist: "The Weeknd", Dur: 220}
	a.onMain(`[1750:1778] [2026-11-14 21:03:41.669] [media] [info] [media_stats.cpp:190] event=track_start, payload={"analyticsContextName":"music","id":"31411989","url":"x"}` + "\n")
	time.Sleep(50 * time.Millisecond) // lookup из кэша
	if !a.Active() || a.Snapshot().Track.Title != "False Alarm" || !a.Snapshot().Playing {
		t.Fatalf("после старта: %+v", a.Snapshot())
	}
	a.onMedia(`[1948:15006] [2026-11-14 21:03:46.556] [media] [info] [gstreamer_audio_player2.cpp:346] [audio stream=31411989 mark=8BNVUD] pause (position 3433 updated 1.09s ago)` + "\n")
	s := a.Snapshot()
	if !s.Paused || s.Pos < 3.4 || s.Pos > 3.5 {
		t.Fatalf("пауза: %+v", s)
	}
	a.onMain(`event=track_finished, payload={"id":"31411989","reason":"cancel"}` + "\n")
	a.onMain(`event=track_start, payload={"id":"31411989"}` + "\n")
	if s := a.Snapshot(); !s.Playing || s.Pos < 3.4 {
		t.Fatalf("продолжение с места: %+v", s)
	}
	a.onMain(`event=track_finished, payload={"id":"31411989","reason":"end"}` + "\n")
	if a.Active() {
		t.Fatal("трек доиграл — неактивно")
	}
}

func TestParseYMTrack(t *testing.T) {
	js := `{"result":[{"title":"False Alarm","durationMs":220300,"coverUri":"avatars.yandex.net/get-music-content/1/a/%%","artists":[{"name":"The Weeknd"}],"albums":[{"title":"Starboy"}]}]}`
	tr, err := parseYMTrack(strings.NewReader(js), 200)
	if err != nil || tr.Artist != "The Weeknd" || tr.Cover != "https://avatars.yandex.net/get-music-content/1/a/400x400" || tr.Dur < 220 || tr.Album != "Starboy" {
		t.Fatalf("parseYMTrack = %+v, %v", tr, err)
	}
}
