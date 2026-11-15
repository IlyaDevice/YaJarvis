package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewerVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{{"v0.9.8", "0.9.7", true}, {"0.10.0", "0.9.9", true}, {"v0.9.7", "0.9.7", false}, {"0.9.6", "0.9.7", false}, {"v1.0.0-beta", "0.9.9", true}} {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q,%q)=%v", c.a, c.b, got)
		}
	}
}

func TestYoutubeID(t *testing.T) {
	for in, want := range map[string]string{
		"https://youtu.be/YSg71LgSh_o?si=ybApSyLAutKmWFXz":     "YSg71LgSh_o",
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=10s":    "dQw4w9WgXcQ",
		"https://youtube.com/shorts/abcdefghijk?feature=share": "abcdefghijk",
		"гимн твича":                                           "",
	} {
		if got := youtubeID(in); got != want {
			t.Errorf("%q → %q, want %q", in, got, want)
		}
	}
}

func TestParseHexColor(t *testing.T) {
	r, g, b := parseHexColor("#ff8000")
	if r != 1 || g < 0.5 || g > 0.51 || b != 0 {
		t.Fatal(r, g, b)
	}
}

func TestTrainerLimit(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Update(func(s *Settings) error { s.TrainMode, s.TrainLimitMB = true, 1; return nil })
	tr := &Trainer{dataDir: dir, store: st}
	clip := make([]int16, 3*MicRate) // 96 КБ
	for i := 0; i < 15; i++ {
		tr.Save("bg", clip, "")
		tr.Save("pos", clip, "")
	}
	var used int64
	for _, c := range tr.clips() {
		used += c.Size
	}
	if used > 1<<20 {
		t.Fatalf("занято %d байт при лимите 1 МБ", used)
	}
	pos, _ := os.ReadDir(tr.dir("pos"))
	if len(pos) < 8 {
		t.Fatalf("оклики не должны вытесняться фоном: осталось %d", len(pos))
	}
	// без режима обучения пишутся только ложные срабатывания
	_ = st.Update(func(s *Settings) error { s.TrainMode = false; return nil })
	n := len(tr.clips())
	tr.Save("near", clip, "p50")
	if len(tr.clips()) != n {
		t.Fatal("near записан вне режима обучения")
	}
}
