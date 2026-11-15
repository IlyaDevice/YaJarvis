package main

import "testing"

// галочка канала умного дома есть только у подключённого канала и сама включается при подключении
func TestHomeUseFollowsConnections(t *testing.T) {
	s := defaultSettings()
	if s.UseHome("yandex") || s.UseHome("smartlife") || s.UseHome("xiaomi") || s.UseHome("homeassistant") {
		t.Fatal("без ключей и входов каналы должны быть выключены")
	}
	if !s.UseHome("alice") || !s.UseHome("yandexmusic") {
		t.Fatal("Алиса и Яндекс Музыка доступны всегда")
	}
	s.HomeUse = map[string]bool{"yandex": false}
	was := s.HomeConnected("yandex")
	s.YandexToken = "y0_test"
	s.homeJustConnected("yandex", was)
	if !s.UseHome("yandex") {
		t.Fatal("подключили Яндекс — галочка должна включиться")
	}
	s.HomeUse["yandex"] = false
	was = s.HomeConnected("yandex")
	s.YandexToken = "y0_other"
	s.homeJustConnected("yandex", was)
	if s.UseHome("yandex") {
		t.Fatal("сменили токен у подключённого канала — выбор «выключено» сохраняется")
	}
}
