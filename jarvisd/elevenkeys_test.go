package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestElevenKeysRotate(t *testing.T) {
	s := &Settings{}
	s.addElevenKey("sk_first_key_1111")
	s.addElevenKey("sk_second_key_2222")
	s.addElevenKey("sk_first_key_1111") // повтор не добавляется
	if len(s.ElevenKeys) != 2 || s.ElevenLabsKey != "sk_first_key_1111" {
		t.Fatalf("ключи %+v, текущий %q", s.ElevenKeys, s.ElevenLabsKey)
	}
	s.ElevenKeys[0].Until = time.Now().Add(time.Hour).Unix()
	s.pickEleven()
	if s.ElevenLabsKey != "sk_second_key_2222" {
		t.Fatalf("после квоты первого говорит %q", s.ElevenLabsKey)
	}
	s.ElevenKeys[1].Until = time.Now().Add(2 * time.Hour).Unix()
	s.pickEleven()
	if s.ElevenLabsKey != "sk_first_key_1111" { // все отдыхают — тот, что освободится раньше
		t.Fatalf("все без квоты, а выбран %q", s.ElevenLabsKey)
	}
	s.ElevenKeys[0].Until = time.Now().Add(-time.Minute).Unix()
	s.pickEleven()
	if s.ElevenLabsKey != "sk_first_key_1111" {
		t.Fatalf("квота вернулась, а выбран %q", s.ElevenLabsKey)
	}
	if !quotaText("This request exceeds your quota of 10000") || quotaText("invalid api key") {
		t.Fatal("quotaText ошибается")
	}
}

func TestPlaceAndKeysMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	old := `{"vol_scale":2,"city":"","timezone":"Europe/Moscow","elevenlabs_key":"sk_only_key_9999","active":"b",
	 "profiles":[{"id":"a","timezone":"Asia/Omsk","city":"Омск"},{"id":"b","timezone":"Europe/Paris","city":"Париж"}]}`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s := st.Get()
	if s.Timezone != "Europe/Paris" || s.City != "Париж" || !s.PlaceMoved {
		t.Fatalf("пояс %q город %q", s.Timezone, s.City)
	}
	for _, p := range s.Profiles {
		if p.Timezone != "Europe/Paris" || p.City != "Париж" {
			t.Fatalf("у ассистента %s свой пояс %q", p.ID, p.Timezone)
		}
	}
	if len(s.ElevenKeys) != 1 || s.ElevenLabsKey != "sk_only_key_9999" {
		t.Fatalf("ключи ElevenLabs: %+v", s.ElevenKeys)
	}
	if !s.UseVPN("openai") || s.UseVPN("youtube") || s.UseVPN("yandex") {
		t.Fatal("галочки VPN по умолчанию не те")
	}
	s.VPN = map[string]bool{"youtube": true}
	if !s.UseVPN("youtube") {
		t.Fatal("галочка YouTube не включилась")
	}
}
