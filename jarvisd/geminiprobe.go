package main

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Uses a real Gemini connection and a recorded PCM voice; never opens the speaker.
func probeGeminiInterruption() error {
	if len(os.Args) < 4 {
		return errors.New("нужны настройки и файл PCM mono s16le 16000 Hz")
	}
	store, err := OpenStore(os.Args[2])
	if err != nil {
		return err
	}
	cfg := store.Get()
	p := cfg.ActiveProfile()
	b, err := os.ReadFile(os.Args[3])
	if err != nil {
		return err
	}
	if len(b) < 3200 || len(b) > 16000*2*20 || len(b)%2 != 0 {
		return errors.New("неподходящий размер PCM")
	}
	proxy := &Proxy{}
	if err = proxy.Apply(cfg.Vless); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	s := NewGeminiSession(ctx, cfg.GeminiKey, p, russianSessionRules, NewTools(func() string { return "" }, DirectClient), proxy.LiveClient(), &Journal{})
	defer s.Close()
	if err = s.Ready(12 * time.Second); err != nil {
		return err
	}
	if err = s.Say(strings.Repeat("Я рассказываю длинную историю о путешествии по большому городу. ", 30)); err != nil {
		return err
	}
	var fedAt time.Time
	heard := 0
	var feedDone <-chan struct{}
	for {
		select {
		case pcm, ok := <-s.Audio():
			if !ok {
				return errors.New("Gemini закрыл сессию до проверки перебивания")
			}
			heard += len(pcm)
			if heard >= geminiRate/2 && fedAt.IsZero() {
				fedAt = time.Now()
				done := make(chan struct{})
				feedDone = done
				go func() {
					defer close(done)
					tick := time.NewTicker(20 * time.Millisecond)
					defer tick.Stop()
					for i := 0; i < len(b); i += 640 {
						end := min(i+640, len(b))
						pcm := make([]int16, (end-i)/2)
						for j := range pcm {
							pcm[j] = int16(float64(int16(binary.LittleEndian.Uint16(b[i+2*j:]))) / s.gain)
						}
						s.Feed(pcm)
						select {
						case <-ctx.Done():
							return
						case <-tick.C:
						}
					}
				}()
			}
		case <-s.Interruptions():
			if fedAt.IsZero() {
				return errors.New("перебивание пришло до подачи записанной речи")
			}
			fmt.Printf("Gemini Live: реальная речь прервала ответ через %d мс; очередь аудио очищена, динамик не включался.\n", time.Since(fedAt).Milliseconds())
			return nil
		case <-feedDone:
			feedDone = nil
		case <-ctx.Done():
			return errors.New("Gemini не подтвердил перебивание за время проверки")
		}
	}
}
