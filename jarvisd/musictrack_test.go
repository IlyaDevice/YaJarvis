package main

import "testing"

// несколько озвучек: берём основную (русскую), а не первую в списке (английскую)
func TestSoundCandidatesPrefersDefaultTrack(t *testing.T) {
	fm := []any{
		map[string]any{"itag": 251.0, "url": "en", "audioTrack": map[string]any{"id": "en.3", "displayName": "English", "audioIsDefault": false}},
		map[string]any{"itag": 251.0, "url": "ru-drc", "isDrc": true, "audioTrack": map[string]any{"id": "ru.4", "displayName": "Русский (оригинал)", "audioIsDefault": true}},
		map[string]any{"itag": 251.0, "url": "ru", "audioTrack": map[string]any{"id": "ru.4", "displayName": "Русский (оригинал)", "audioIsDefault": true}},
		map[string]any{"itag": 250.0, "url": "other"},
	}
	c := soundCandidates(fm, 251)
	if len(c) != 3 || c[0]["url"] != "ru" || c[1]["url"] != "ru-drc" {
		t.Fatalf("порядок: %v, %v, %v", c[0]["url"], c[1]["url"], c[2]["url"])
	}
	// дорожка одна — без audioTrack
	if c := soundCandidates(fm, 250); len(c) != 1 || c[0]["url"] != "other" {
		t.Fatal("одна дорожка")
	}
}
