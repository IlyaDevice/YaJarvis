// Package hardware identifies retail Station models from board identifiers.
package hardware

import "strings"

type Model struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Compatible and DT ID take precedence over product and display names.
// station-midi/yandexmidi means Station 2; retail Midi is cucumber.
// Sources: hackalice.me/devices/ and nyakokitsu/yam/docs/PLATFORMS.md.
func Detect(compatible, dtid, product, display string) Model {
	rules := []struct {
		id, name string
		markers  []string
	}{
		{"mini3pro", "Яндекс Станция Мини 3 Про", []string{"pickle", "mini 3 pro", "мини 3 про"}},
		{"mini3", "Яндекс Станция Мини 3", []string{"bergamot", "mini 3", "мини 3"}},
		{"midi", "Яндекс Станция Миди", []string{"cucumber"}},
		{"station3", "Яндекс Станция 3", []string{"orion"}},
		{"lite2", "Яндекс Станция Лайт 2", []string{"plum", "yandex,prune", "lite 2", "лайт 2", "lite2", "micro2", "micro_2", "micro-2"}},
		{"street", "Яндекс Станция Стрит", []string{"mango"}},
		{"fiero", "Fiero Hi", []string{"blackthorn", "fiero"}},
		{"mini2", "Яндекс Станция Мини 2", []string{"station-mini2", "station-mini-2", "yandexmini_2", "mini 2", "мини 2"}},
		{"station2", "Яндекс Станция 2", []string{"station-midi", "yandexmidi", "station midi"}},
		{"max", "Яндекс Станция Макс", []string{"yandexstation_2", "station max", "станция макс"}},
		{"station1", "Яндекс Станция первого поколения", []string{"tulip", "yandexstation"}},
		{"hub", "Умный хаб Яндекса", []string{"saturn"}},
		{"lite", "Яндекс Станция Лайт", []string{"station-micro", "station micro", "yandexmicro", "station lite", "станция лайт"}},
		{"mini1", "Яндекс Станция Мини", []string{"station-mini", "yandexmini", "station mini"}},
		{"jbl", "JBL Link", []string{"jbl", "mediatek,mt8167"}},
	}
	for _, raw := range []string{compatible, dtid, product, display} {
		raw = strings.ToLower(raw)
		for _, rule := range rules {
			for _, marker := range rule.markers {
				if strings.Contains(raw, marker) {
					return Model{rule.id, rule.name}
				}
			}
		}
	}
	return Model{"unknown", "Яндекс Станция (модель не определена)"}
}
