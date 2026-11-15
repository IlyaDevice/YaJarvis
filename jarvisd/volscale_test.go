package main

import (
	"os"
	"path/filepath"
	"testing"
)

// старые настройки переводятся в новую шкалу громкости голоса один раз: 100 → 50
func TestVolScaleMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(path, []byte(`{"volume":80,"active":"p1","profiles":[{"id":"p1","volume":100}],"night":{"voice":40}}`), 0o600)
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s := st.Get()
	if s.VolScale != 2 || s.Volume != 40 || s.Profiles[0].Volume != 50 || s.Night.Voice != 20 {
		t.Fatalf("после перевода: %d %d %d %d", s.VolScale, s.Volume, s.Profiles[0].Volume, s.Night.Voice)
	}
	st2, _ := OpenStore(path)
	if v := st2.Get().Profiles[0].Volume; v != 50 {
		t.Fatalf("второй раз переводить нельзя: %d", v)
	}
}
