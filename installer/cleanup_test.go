package main

import (
	"strings"
	"testing"
)

func TestPlanCleanupFreesOnlyWhatIsNeeded(t *testing.T) {
	sp := &Speaker{FreeMB: 10, NeedMB: 30, JunkKB: 4 * 1024, Addons: []Addon{
		{ID: "yam", KB: 4 * 1024},
		{ID: "alisa-cli", KB: 40 * 1024, Mic: true},
		{ID: "midipult", KB: 2 * 1024},
	}}
	junk, rm := planCleanup(sp, false)
	if !junk || len(rm) != 1 || rm[0].ID != "alisa-cli" {
		t.Fatalf("junk=%v rm=%v", junk, rm)
	}
	sp.FreeMB = 40
	if junk, rm := planCleanup(sp, false); junk || rm != nil {
		t.Fatalf("enough space, but plan junk=%v rm=%v", junk, rm)
	}
	if _, rm := planCleanup(sp, true); len(rm) != 3 {
		t.Fatalf("clean install must remove all found addons, got %v", rm)
	}
}

func TestRemoveScriptNeverTouchesAccess(t *testing.T) {
	for _, a := range addonCatalog {
		s := removeScript(a)
		for _, bad := range []string{"/data/quasar ", "/data/quasar/", "dropbear ", "yandex-noupdate", "/data/opt/bin", "rm -rf /data ", "rm -rf /data/opt ", ".yajavis-counter", "/data/opt/jarvisd"} {
			if strings.Contains(s, "rm -rf "+bad) || strings.Contains(s, "rm -f /etc/init.d/S99quasar") {
				t.Fatalf("%s: опасная команда (%s): %s", a.ID, bad, s)
			}
		}
		if !strings.Contains(s, "S98jarvisd|S99quasar|S99zdropbear|rcK|rcS") {
			t.Fatalf("%s: init scripts of Alice/SSH/Jarvis must be protected: %s", a.ID, s)
		}
		for _, p := range a.Paths {
			if p == "/data" || p == "/data/opt" || strings.HasPrefix(p, "/data/quasar") || strings.HasPrefix(p, "/data/opt/jarvisd") {
				t.Fatalf("%s: path %s is off limits", a.ID, p)
			}
		}
	}
}
