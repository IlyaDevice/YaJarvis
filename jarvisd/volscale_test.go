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

// автообновление включается по умолчанию один раз — дальше выбор человека не трогаем
func TestAutoUpdateDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	_ = os.WriteFile(path, []byte(`{"vol_scale":2,"update_check":false,"update_auto":false}`), 0o600)
	st, _ := OpenStore(path)
	if s := st.Get(); !s.UpdateAuto || !s.UpdateCheck {
		t.Fatal("старая установка: автообновление должно включиться")
	}
	_ = st.Update(func(s *Settings) error { s.UpdateAuto = false; return nil })
	st2, _ := OpenStore(path)
	if st2.Get().UpdateAuto {
		t.Fatal("выбор «вручную» после этого должен сохраняться")
	}
	st3, _ := OpenStore(filepath.Join(t.TempDir(), "new.json"))
	if !st3.Get().UpdateAuto {
		t.Fatal("новая установка — с автообновлением")
	}
}
