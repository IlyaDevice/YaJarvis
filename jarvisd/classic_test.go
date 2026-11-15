package main

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestClassicSpeakerMuteDiscardsBufferedAudio(t *testing.T) {
	client := &http.Client{Transport: youtubeTransport(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("output_format") != "pcm_24000" || r.Header.Get("xi-api-key") != "test-eleven-key" {
			t.Error("wrong TTS request")
		}
		pcm := strings.Repeat("\x00\x10", elevenRate*3)
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(pcm))}, nil
	})}
	p := Profile{Mode: "classic", TTSVoice: "test-voice", LLMModel: "gpt-6-luna"}
	s := NewClassicSpeaker(context.Background(), "test-eleven-key", p, client, &Journal{})
	if err := s.Ready(time.Second); err != nil {
		t.Fatal(err)
	}
	if err := s.Say("Проверка"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Audio():
	case <-time.After(time.Second):
		t.Fatal("speaker produced no audio")
	}
	s.Mute()
	select {
	case <-s.Audio():
		t.Fatal("muted speaker leaked old audio")
	case <-time.After(300 * time.Millisecond):
	}
	s.Close()
	select {
	case _, ok := <-s.Audio():
		if ok {
			t.Fatal("old audio after close")
		}
	case <-time.After(time.Second):
		t.Fatal("speaker did not close")
	}
	if err := s.Say("поздний ответ"); err == nil {
		t.Fatal("closed speaker accepted a new job")
	}
}

func TestLegacyClassicProfileKeepsModelAndVoice(t *testing.T) {
	p := Profile{Mode: "classic", Engine: "openai", LLMModel: "gpt-6.1-sol", TTSVoice: "my-eleven-voice", TTSModel: "eleven_multilingual_v2", Voice: "cedar"}
	p.fillDefaults(defaultSettings())
	if p.Engine != "elevenlabs" || p.Mode != "classic" || p.BackendModel != "gpt-6.1-sol" || p.TTSVoice != "my-eleven-voice" || p.TTSModel != "eleven_multilingual_v2" || p.Voice != "cedar" {
		t.Fatalf("legacy settings lost: %+v", p)
	}
}
