package main

import (
	"bufio"
	"context"
	"net/http"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/thesyncim/gopus"
)

func TestYoutubeRequest(t *testing.T) {
	cases := map[string]string{
		"Джарвис, включи с ютуба Imagine Dragons Believer": "Imagine Dragons Believer",
		"поставь на ютубе песню Кино группа крови":         "Кино группа крови",
		"Джарвис, включи музыку":                           "",
		"что нового на ютубе":                              "",
		"включи YouTube Music Кино":                        "Кино",
		"поставь на паузу на ютубе":                        "",
	}
	for in, want := range cases {
		got, ok := youtubeRequest(in)
		if (want == "") == ok || got != want {
			t.Errorf("%q → %q, %v; ждали %q", in, got, ok, want)
		}
	}
}

// Живая проверка с настоящим YouTube: JARVIS_NET=1 go test -run TestMusicLive
func TestMusicLive(t *testing.T) {
	if os.Getenv("JARVIS_NET") == "" {
		t.Skip("нужна сеть: JARVIS_NET=1")
	}
	client := &http.Client{Timeout: 60 * time.Second}
	m := NewMusic(func() *http.Client { return client }, func() float64 { return 1 })
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	tracks, err := m.search(ctx, "Imagine Dragons Believer")
	if err != nil || len(tracks) == 0 {
		t.Fatalf("поиск: %v %v", tracks, err)
	}
	t.Logf("нашлось: %+v", tracks[:min(3, len(tracks))])
	st, info, err := m.audioURL(ctx, tracks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("видео: %+v", info)
	src, w, err := m.openAudio(ctx, tracks[0].ID, st, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	dec, _ := gopus.NewDecoder(gopus.DefaultDecoderConfig(musicRate, 1))
	pcm := make([]int16, musicRate/8)
	samples, peak := 0, 0
	t0 := time.Now()
	for samples < 10*musicRate {
		f, _, err := w.next()
		if err != nil {
			t.Fatalf("после %d отсчётов: %v", samples, err)
		}
		n, err := dec.DecodeInt16(f, pcm)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range pcm[:n] {
			peak = max(peak, int(s), -int(s))
		}
		samples += n
	}
	t.Logf("10 с звука за %.2f с, пик %d", time.Since(t0).Seconds(), peak)
	if peak < 3000 {
		t.Error("звук подозрительно тихий — декодер что-то не так понял")
	}
}

func TestMusicCommand(t *testing.T) {
	cases := []struct {
		in     string
		action string
		secs   float64
		abs    bool
	}{
		{"что сейчас играет", "what", 0, false},
		{"кто поёт?", "what", 0, false},
		{"перемотай вперёд на 30 секунд", "seek", 30, false},
		{"назад на 10 секунд", "seek", -10, false},
		{"промотай на минуту", "seek", 60, false},
		{"перемотай на 2 минуты", "seek", 120, false},
		{"включи сначала", "seek", 0, true},
		{"поставь на паузу", "pause", 0, false},
		{"продолжи", "resume", 0, false},
		{"играй дальше", "resume", 0, false},
		{"следующую песню", "next", 0, false},
		{"выключи музыку", "stop", 0, false},
		{"Джарвис, перемотай вперед на две минуты", "seek", 120, false},
		{"перемотай на 2 минуты 30 секунд", "seek", 150, false},
		{"перемотай на отметку 2:00", "seek", 120, true},
		{"Чарлис, перемотай на отметку 2:00", "seek", 120, true},
		{"назад на пятнадцать секунд", "seek", -15, false},
	}
	for _, c := range cases {
		a, s, abs, ok := musicCommand(c.in)
		if !ok || a != c.action || s != c.secs || abs != c.abs {
			t.Errorf("%q → %s %v %v %v; ждали %s %v %v", c.in, a, s, abs, ok, c.action, c.secs, c.abs)
		}
	}
	for _, text := range []string{"какая погода", "расскажи, как перемотать фильм", "поставь таймер на минуту", "как называется столица Беларуси"} {
		if _, _, _, ok := musicCommand(text); ok {
			t.Errorf("%q — не команда музыки", text)
		}
	}
}

// Длинное скачивание (дольше минуты, на которой ссылка рвалась) и перемотка: JARVIS_NET=1
func TestMusicLongAndSeekLive(t *testing.T) {
	if os.Getenv("JARVIS_NET") == "" {
		t.Skip("нужна сеть: JARVIS_NET=1")
	}
	client := &http.Client{Timeout: 60 * time.Second}
	m := NewMusic(func() *http.Client { return client }, func() float64 { return 1 })
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	tracks, err := m.search(ctx, "Макс Корж Жить в кайф")
	if err != nil || len(tracks) == 0 {
		t.Fatalf("поиск: %v", err)
	}
	st, _, err := m.audioURL(ctx, tracks[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("«%s»: %.0f с, %d байт, клиент %s", tracks[0].Title, st.dur, st.length, st.client)
	var mst runtime.MemStats
	runtime.ReadMemStats(&mst)
	t.Logf("память: куча %d МБ, всего у Go %d МБ", mst.HeapInuse>>20, mst.Sys>>20)
	t.Logf("ждём рекламу %.1f с", time.Until(st.ready).Seconds())
	if os.Getenv("NOWAIT") == "" {
		_ = sleepCtx(ctx, time.Until(st.ready))
	}
	renews := 0
	var src0 *ytReader
	src := &ytReader{ctx: ctx, client: client, url: st.url, ua: st.ua, refresh: func() (*ytStream, error) {
		renews++
		ns, _, err := m.audioURL(ctx, tracks[0].ID)
		if err == nil && renews <= 12 {
			t.Logf("обновление %d: клиент %s, позиция %d", renews, ns.client, src0.pos)
		}
		return ns, err
	}}
	src0 = src
	defer src.Close()
	w := &webm{r: bufio.NewReader(src)}
	var last int64
	for last < 150000 {
		_, ms, err := w.next()
		if err != nil {
			t.Fatalf("на %d мс: %v (обновлений ссылки %d)", last, err, renews)
		}
		last = ms
	}
	t.Logf("скачано 2,5 минуты, %d байт, обновлений ссылки %d", src.pos, renews)
	// Тот же путь перемотки, что в проигрывателе: до цели идёт разогрев без вывода звука.
	src2, w2, err := m.openAudio(ctx, tracks[0].ID, st, 120)
	if err != nil {
		t.Fatal(err)
	}
	defer src2.Close()
	f := &musicFrames{src: src2, w: w2, target: 120000}
	for {
		_, ms, err := f.next()
		if err != nil {
			t.Fatal(err)
		}
		if ms >= f.target {
			t.Logf("после перемотки на 2:00 первый слышимый кадр на %.3f с", float64(ms)/1000)
			if ms-f.target > 100 {
				t.Errorf("перемотка промахнулась: %d мс", ms)
			}
			break
		}
	}
}
