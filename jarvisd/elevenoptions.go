package main

import (
	"fmt"
	"os"
)

func addElevenOptions() error {
	store, err := OpenStore(os.Args[2])
	if err != nil {
		return err
	}
	return store.Update(func(s *Settings) error {
		base := s.ActiveProfile()
		if base.TTSVoice == "" {
			for _, p := range s.Profiles {
				if p.TTSVoice != "" {
					base = p
					break
				}
			}
		}
		if base.TTSVoice == "" {
			return fmt.Errorf("сначала выбери голос ElevenLabs")
		}
		for _, variant := range []struct{ Name, Engine, Model string }{{"Eleven Mini", "eleven_realtime", "eleven_realtime_v1_mini"}, {"Eleven Max", "eleven_realtime", "eleven_realtime_v1_max"}, {"Eleven v4 Turbo", "elevenlabs", "eleven_v4_turbo"}} {
			exists := false
			for _, p := range s.Profiles {
				if p.Name == variant.Name {
					exists = true
					break
				}
			}
			if exists {
				continue
			}
			p := base
			p.ID, p.Name, p.Engine, p.Mode = "p"+randHex(4), variant.Name, variant.Engine, "live"
			if variant.Engine == "eleven_realtime" {
				p.LiveModel = variant.Model
			} else {
				classicDefaults(&p)
				p.TTSModel = variant.Model
				p.STTModel = "scribe_v3_live"
			}
			s.Profiles = append(s.Profiles, p)
		}
		fmt.Println("Добавлены ассистенты Eleven Mini, Eleven Max и Eleven v4 Turbo. Текущий выбор сохранён.")
		return nil
	})
}
