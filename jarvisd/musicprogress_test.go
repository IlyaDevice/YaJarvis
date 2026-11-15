package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPlaybackPositionSurvivesRestartAndPausedSeek(t *testing.T) {
	path := filepath.Join(t.TempDir(), "progress.json")
	m := &Music{progress: openPlaybackHistory(path), cur: Track{ID: "story123456", Title: "Истории", Author: "апвоут"}, st: &ytStream{dur: 1000}, paused: true, pos: 123}
	m.saveProgress(false)
	reopened := openPlaybackHistory(path)
	v, err := reopened.find("вчерашний апвоут")
	if err != nil || v.Pos != 123 {
		t.Fatalf("lost position after restart: %+v %v", v, err)
	}
	if err := m.Seek(200, true); err != nil {
		t.Fatal(err)
	}
	v, err = openPlaybackHistory(path).find("апвоут")
	if err != nil || v.Pos != 200 {
		t.Fatalf("paused seek was not persisted: %+v %v", v, err)
	}
	m.saveProgress(true)
	if _, err := openPlaybackHistory(path).find("апвоут"); err == nil {
		t.Fatal("completed video must not be resumed")
	}
}

func TestResumeSavedRestoresOffsetWithoutSearching(t *testing.T) {
	path := filepath.Join(t.TempDir(), "progress.json")
	p := openPlaybackHistory(path)
	p.items["story123456"] = savedPlayback{Track: Track{ID: "story123456", Title: "Истории", Author: "апвоут"}, Pos: 120, Dur: 1000, Updated: time.Now()}
	started := make(chan float64, 1)
	m := &Music{progress: p, pre: preStream{id: "story123456", st: &ytStream{dur: 1000}, at: time.Now()}, playback: func(ctx context.Context, _ int, _ string, _ *ytStream, pos float64) error {
		started <- pos
		<-ctx.Done()
		return nil
	}}
	answer := m.resumeSaved(context.Background(), "вчерашний апвоут")
	if !strings.Contains(answer, "1:57") {
		t.Fatal(answer)
	}
	select {
	case pos := <-started:
		if pos != 117 {
			t.Fatal(pos)
		}
	case <-time.After(time.Second):
		t.Fatal("resume did not restore playback")
	}
	m.Stop()
	if _, ok := savedResumeQuery("продолжи уборку кухни"); ok {
		t.Fatal("vacuum resume must never become a YouTube search")
	}
}
