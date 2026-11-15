package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPatchLaunch(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "launch.json")
	_ = os.WriteFile(in, []byte(`{"nodes":[{"tasks":[{"name":"mediad","path":"/system/vendor/quasar/maind","arg":["--mediad"],"envs":["A=1"]},
	{"name":"maind","path":"/system/vendor/quasar/maind","arg":["--platformd"],"envs":["MALLOC_ARENA_MAX=2","LD_PRELOAD=/data/opt/alisa-standalone/vqe_tap.so:/x/other.so","ALICE_CAPTURE_SOCKET=/tmp/a.sock"]}]}]}`), 0o644)
	out := filepath.Join(dir, "out.json")
	if err := patchLaunch(in, out, "/data/opt/jarvisd/vqe_tap.so", "/tmp/jarvisd/mic.sock"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(out)
	s := string(b)
	for _, want := range []string{"LD_PRELOAD=/data/opt/jarvisd/vqe_tap.so:/x/other.so", "JARVIS_MIC_SOCKET=/tmp/jarvisd/mic.sock", "MALLOC_ARENA_MAX=2", `"A=1"`} {
		if !strings.Contains(s, want) {
			t.Fatalf("нет %q в %s", want, s)
		}
	}
	if strings.Contains(s, "alisa-standalone") || strings.Contains(s, "ALICE_CAPTURE_SOCKET") || strings.Count(s, "LD_PRELOAD") != 1 {
		t.Fatalf("лишнее: %s", s)
	}
}
