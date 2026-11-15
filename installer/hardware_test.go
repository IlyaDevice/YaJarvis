package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestInspectConnectedSpeaker(t *testing.T) {
	ip := os.Getenv("JARVIS_TEST_SPEAKER")
	if ip == "" {
		t.Skip("set JARVIS_TEST_SPEAKER for a read-only hardware check")
	}
	sp, err := inspect(ip)
	if err != nil {
		t.Fatal(err)
	}
	if sp.ModelID == "unknown" || sp.Arch == "" || sp.Userland == "" || !sp.Root {
		t.Fatal("connected speaker identification is incomplete")
	}
	if len(sp.Problems) != 0 {
		t.Fatalf("preflight: %v", sp.Problems)
	}
	t.Logf("identified %s (%s), binary %s, maind %s", sp.Model, sp.ModelID, sp.Arch, sp.Userland)
}

func TestMaindABI(t *testing.T) {
	for _, tc := range []struct {
		class, machine byte
		want           string
	}{{1, 40, "armhf"}, {2, 183, "arm64"}, {2, 62, ""}, {1, 183, ""}} {
		b := make([]byte, 20)
		copy(b, "\x7fELF")
		b[4], b[5], b[18] = tc.class, 1, tc.machine
		var words []string
		for _, v := range b {
			words = append(words, fmt.Sprint(v))
		}
		if got := elfUserland(strings.Join(words, " ")); got != tc.want {
			t.Errorf("class %d machine %d: %s", tc.class, tc.machine, got)
		}
	}
	if elfUserland("1 2 3") != "" {
		t.Fatal("accepted truncated ELF")
	}
}

func TestLaunchCompatibility(t *testing.T) {
	if !supportsLaunch(`{"processes":[{"path":"/system/vendor/quasar/maind","arg":["--platformd"]}]}`) {
		t.Fatal("compatible profile rejected")
	}
	for _, raw := range []string{"", `{}`, `{"path":"/other","arg":["--platformd"]}`, `{"path":"/maind","arg":["--mediad"]}`} {
		if supportsLaunch(raw) {
			t.Fatal("incompatible profile accepted")
		}
	}
}
