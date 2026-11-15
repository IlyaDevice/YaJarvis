package hardware

import "testing"

func TestRetailModels(t *testing.T) {
	for _, tc := range []struct{ compatible, dtid, product, display, want string }{
		{"yandex,station-micro\x00amlogic, axg", "", "", "Yandex Station Micro v1.2", "lite"},
		{"yandex,cucumber-dvt2\x00amlogic,a5", "a5_cucumber_dvt2", "", "Yandex Cucumber DVT2", "midi"},
		{"yandex,station-midi\x00amlogic,axg", "", "", "Yandex Station Midi", "station2"},
		{"", "", "yandexstation_2", "", "max"},
		{"yandex,plum", "", "", "", "lite2"},
		{"yandex,station-mini2", "", "", "", "mini2"},
		{"", "axg_yandexmini", "", "Amlogic", "mini1"},
		{"yandex,bergamot", "", "", "", "mini3"},
		{"yandex,pickle-evt", "", "", "", "mini3pro"},
		{"yandex,mango", "", "", "", "street"},
		{"yandex,orion-pvt", "", "", "", "station3"},
		{"yandex,saturn", "", "", "", "hub"},
		{"yandex,cucumber", "", "yandexmidi", "Yandex Station Midi", "midi"},
		{"amlogic,axg", "", "", "Amlogic", "unknown"},
	} {
		if got := Detect(tc.compatible, tc.dtid, tc.product, tc.display); got.ID != tc.want {
			t.Errorf("%q %q %q %q: %s, want %s", tc.compatible, tc.dtid, tc.product, tc.display, got.ID, tc.want)
		}
	}
}
