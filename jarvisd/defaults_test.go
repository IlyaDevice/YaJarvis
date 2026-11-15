package main

import "testing"

func TestPublicDefaultsHaveNoPersonalData(t *testing.T) {
	s := defaultSettings()
	if s.City != "" || s.SpeakerRoom != "" || s.Timezone != "UTC" {
		t.Fatal("public defaults contain a preset location")
	}
	if s.OpenAIKey != "" || s.GeminiKey != "" || s.GrokKey != "" || s.ElevenLabsKey != "" || s.YandexToken != "" || s.Vless != "" || s.YouTubeRefresh != "" || s.GDriveRefresh != "" || s.PasswordHash != "" {
		t.Fatal("public defaults contain credentials")
	}
	if s.DevMode || s.TrainMode {
		t.Fatal("recording or training is enabled")
	}
	if !s.UpdateCheck || !s.UpdateAuto || defaultRepo != "newfpv/YaJavis" {
		t.Fatal("release update source is not configured")
	}
	for _, p := range s.Profiles {
		if p.City != "" || p.TTSVoice != "" {
			t.Fatal("profile contains a preset location or private voice")
		}
	}
}
