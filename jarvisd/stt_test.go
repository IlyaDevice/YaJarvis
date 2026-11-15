package main

import (
	"encoding/binary"
	"net/http"
	"os"
	"testing"
	"time"
)

// JARVIS_VLESS=… OPENAI_API_KEY=… JARVIS_WAV=… go test -run STTLive -v
func TestSTTLive(t *testing.T) {
	key, wav := os.Getenv("OPENAI_API_KEY"), os.Getenv("JARVIS_WAV")
	if key == "" || wav == "" {
		t.Skip("нет OPENAI_API_KEY / JARVIS_WAV")
	}
	p := &Proxy{}
	if err := p.Apply(os.Getenv("JARVIS_VLESS")); err != nil {
		t.Fatal(err)
	}
	s := NewSTT(func() string { return key }, func() *http.Client { return p.Client(0) })
	for i := 0; i < 100 && !s.Ready(); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if !s.Ready() {
		t.Fatal("не подключилось")
	}
	time.Sleep(500 * time.Millisecond)
	raw, _ := os.ReadFile(wav)
	pcm := make([]int16, (len(raw)-44)/2)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(raw[44+2*i:]))
	}
	ch, err := s.Begin()
	if err != nil {
		t.Fatal(err)
	}
	// в реальном времени, кусками по 20 мс
	for i := 0; i < len(pcm); i += 320 {
		s.Feed(pcm[i:min(i+320, len(pcm))])
		time.Sleep(20 * time.Millisecond)
	}
	end := time.Now()
	for i := 0; i < 50; i++ { // тишина после фразы
		s.Feed(make([]int16, 320))
		time.Sleep(20 * time.Millisecond)
		select {
		case txt := <-ch:
			t.Logf("«%s» через %v после конца звука", txt, time.Since(end).Round(time.Millisecond))
			return
		default:
		}
	}
	select {
	case txt := <-ch:
		t.Logf("«%s» через %v после конца звука", txt, time.Since(end).Round(time.Millisecond))
	case <-time.After(5 * time.Second):
		t.Fatal("расшифровки нет")
	}
}
