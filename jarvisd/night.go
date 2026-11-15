package main

// Ночной режим: голос и кольцо ограничивают VoiceVolume/LedFor, а громкость самой колонки (Алиса, музыка)
// держим здесь — раз в полминуты, пока ночь, не даём ей быть выше заданной.

import (
	"fmt"
	"log"
	"time"
)

func nightCap(store *Store) {
	s := store.Get()
	if !s.IsNight() {
		return
	}
	v := speakerVolume()
	if v <= s.Night.Speaker {
		return
	}
	if err := AliceText(fmt.Sprintf("громкость %d", s.Night.Speaker), true); err != nil {
		log.Printf("ночной режим: громкость: %v", err)
		return
	}
	volMu.Lock()
	volSet, volSetAt = s.Night.Speaker, time.Now()
	volMu.Unlock()
	log.Printf("ночной режим: громкость колонки %d → %d", v, s.Night.Speaker)
}

// WatchNight — следить за ночью; на переходе день↔ночь перенастроить кольцо.
func WatchNight(store *Store, leds *Leds) {
	was := false
	for {
		s := store.Get()
		night := s.IsNight()
		if night != was {
			was = night
			leds.Configure(s.LedFor())
			if night {
				log.Printf("ночной режим: начался (%s–%s)", s.Night.From, s.Night.To)
			}
		}
		if night {
			nightCap(store)
		}
		time.Sleep(30 * time.Second)
	}
}
