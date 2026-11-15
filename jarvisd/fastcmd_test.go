package main

import (
	"encoding/json"
	"testing"
)

const testCatalog = `{"rooms":[{"id":"r1","name":"Зал"},{"id":"r2","name":"Кухня"},{"id":"r3","name":"Спальня"}],
"devices":[
 {"id":"spk","name":"Станция","type":"devices.types.smart_speaker.yandex.station.micro","room":"r1","capabilities":[]},
 {"id":"l1","name":"Свет","type":"devices.types.light","room":"r1","capabilities":[{"type":"devices.capabilities.on_off"}]},
 {"id":"l2","name":"Лента стол","type":"devices.types.light","room":"r1","capabilities":[{"type":"devices.capabilities.on_off"}]},
 {"id":"l3","name":"Люстра","type":"devices.types.light","room":"r2","capabilities":[{"type":"devices.capabilities.on_off"}]},
 {"id":"l4","name":"Лента","type":"devices.types.light","room":"r3","capabilities":[{"type":"devices.capabilities.on_off"}]},
 {"id":"s1","name":"Розетка","type":"devices.types.socket","room":"r2","capabilities":[{"type":"devices.capabilities.on_off"}]},
 {"id":"v1","name":"Mijia Robot Vacuum 5","type":"devices.types.vacuum_cleaner","room":"r2","capabilities":[{"type":"devices.capabilities.on_off"}]},
 {"id":"ac","name":"Кондиционер","type":"devices.types.thermostat.ac","room":"r1","capabilities":[{"type":"devices.capabilities.on_off"}]}],
"groups":[],"scenarios":[{"id":"sc1","name":"Я дома"}]}`

func TestPlanCommand(t *testing.T) {
	var c iotCatalog
	if err := json.Unmarshal([]byte(testCatalog), &c); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"Джарвис, выключи свет.":  "off:l1",
		"включи свет на кухне":    "on:l3",
		"выключи весь свет":       "off:l1,l2,l3,l4",
		"Выключи стол":            "off:l2",
		"включи ленту":            "on:l4",
		"включи ленту в спальне":  "on:l4",
		"выключи розетку":         "off:s1",
		"запусти сценарий я дома": "scenario:sc1",
		"включи телевизор":        "",
		"запусти пылесос":         "on:v1",
		"останови пылесос":        "off:v1",
		"выключи кондей":          "off:ac",
		"расскажи анекдот":        "",
	}
	for text, want := range cases {
		got := ""
		if m := fastCmdRe.FindStringSubmatch(normText(text)); m != nil {
			if p := planCommand(&c, m[1], m[2]); p != nil {
				if p.scenario != "" {
					got = "scenario:" + p.scenario
				} else {
					got = map[bool]string{true: "on:", false: "off:"}[p.on]
					for i, id := range p.ids {
						if i > 0 {
							got += ","
						}
						got += id
					}
				}
			}
		}
		if got != want {
			t.Errorf("%q: получилось %q, ждали %q", text, got, want)
		}
	}
	if m := colorRe.FindStringSubmatch(normText("Джарвис, сделай ленту красной")); m == nil || m[2] != "красн" || m[1] != "ленту" {
		t.Errorf("цвет не распознан: %v", m)
	}
	if m := colorRe.FindStringSubmatch(normText("поставь лампу тёплым светом")); m == nil || m[2] != "тепл" {
		t.Errorf("тёплый не распознан: %v", m)
	}
	for _, s := range []string{"Джарвис, включи свет", "Жарвис стоп", "Jarvis!", "дарвис привет"} {
		if !hasName(s) {
			t.Errorf("%q — имя есть", s)
		}
	}
	for in, want := range map[string]string{
		"Джарвис, поставь таймер на пять минут": "поставь таймер на пять минут",
		"Джарвис, разбуди меня в семь":          "разбуди меня в семь",
		"включи музыку":                         "включи музыку",
		"Джарвис, громче":                       "громче",
		"напомни через час выключить плиту":     "напомни через час выключить плиту",
		"Джарвис, отмени будильник":             "отмени будильник",
	} {
		if got, ok := AliceSkill(in); !ok || got != want {
			t.Errorf("AliceSkill(%q) = %q, %v", in, got, ok)
		}
	}
	for _, in := range []string{"Джарвис, расскажи про музыку барокко", "кто придумал таймер", "Джарвис, включи свет", "что ты думаешь про громкость в клубах"} {
		if _, ok := AliceSkill(in); ok {
			t.Errorf("AliceSkill(%q) — это вопрос Джарвису, не команда Алисе", in)
		}
	}
	// так проверочная расшифровка слышала настоящие оклики (журнал колонки 13.11.2026)
	for _, s := range []string{"Джарис, выключи лампу.", "Чарлис, выключи лампу.", "Жарови, не молчи мне!", "Жарится.", "Жары скутывшы весь свет.", "Эй, Дарвис!", "Привет. Джарвис, включи свет", "Žarvis.", "Zharvis, включи свет", "Jarvis"} {
		if !hasName(s) {
			t.Errorf("%q — это оклик, имя должно находиться", s)
		}
	}
	if got := canonName("Чарлис, выключи лампу."); got != "джарвис выключи лампу" {
		t.Errorf("canonName: %q", got)
	}
	if !fastCmdRe.MatchString(canonName("Джарис, выключи лампу.")) {
		t.Error("после исправления имени быстрая команда должна распознаваться")
	}
	if got, ok := commandAfterLostName("Сейчас отключи свет."); !ok || got != "джарвис отключи свет" {
		t.Errorf("commandAfterLostName: %q %v", got, ok)
	}
	for _, s := range []string{"Мне не нравится интерфейс", "Сейчас Клод же дал", "Большая козочка уже."} {
		if _, ok := commandAfterLostName(s); ok {
			t.Errorf("%q — не команда", s)
		}
	}
	for _, s := range []string{"сука", "давай играть", "чарли иди сюда", "Мне не нравится интерфейс разговора", "В комнате сегодня жарко", "Большая козочка уже."} {
		if hasName(s) {
			t.Errorf("%q — имени нет", s)
		}
	}
	for _, s := range []string{"Стоп", "Джарвис, стоп!", "хватит", "всё, хватит", "замолчи пожалуйста", "Джарвис стоп"} {
		if !isStop(s) {
			t.Errorf("%q должно быть стоп", s)
		}
	}
	for _, s := range []string{"стоп-кран сорвали", "не останавливайся", "расскажи про стоп-слова", "закрой шторы"} {
		if isStop(s) {
			t.Errorf("%q не стоп", s)
		}
	}
	// «стоп» — всё, включая музыку; «хватит», «закрой чат» — только разговор
	for _, s := range []string{"Стоп", "Джарвис, стоп!", "остановись", "прекрати"} {
		if !isStopAll(s) {
			t.Errorf("%q — остановить всё", s)
		}
	}
	for _, s := range []string{"хватит", "всё, хватит", "закрой чат", "Джарвис, закрой разговор", "пока", "спасибо, достаточно", "можешь идти"} {
		if !isStop(s) || isStopAll(s) {
			t.Errorf("%q — закрыть только разговор", s)
		}
	}
}
