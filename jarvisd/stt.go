package main

// Быстрая расшифровка для команд: соединение с OpenAI Realtime (режим «только расшифровка») держится
// открытым заранее — без звука оно ничего не стоит. После «Джарвис» звук сразу идёт туда, и команда
// («выключи свет», «стоп», фраза сценария) распознаётся за доли секунды, не дожидаясь открытия GPT-Live.

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const sttURL = "wss://api.openai.com/v1/realtime?intent=transcription"

type STT struct {
	key    func() string
	client func() *http.Client

	writeMu sync.Mutex // clear → начало записи → новые кадры отправляются строго по порядку
	mu      sync.Mutex
	ws      *websocket.Conn
	since   time.Time
	results chan string // готовые фразы (completed) текущего захвата
	partial string
	active  bool
	speech  bool // сервер слышит речь прямо сейчас (между speech_started и speech_stopped)
}

// speechLang — язык хозяина для распознавания ("" — определять самому); задаётся в main из настроек.
var speechLang = func() string { return "ru" }

func NewSTT(key func() string, client func() *http.Client) *STT {
	s := &STT{key: key, client: client}
	go s.keep()
	return s
}

// keep — держать соединение открытым; переподключаться раз в 20 минут и после обрыва.
func (s *STT) keep() {
	for {
		if s.key() == "" {
			time.Sleep(10 * time.Second)
			continue
		}
		err := s.session()
		if err != nil {
			log.Printf("быстрая расшифровка: %v", shortErr(err))
		}
		time.Sleep(3 * time.Second)
	}
}

func (s *STT) session() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	ws, _, err := websocket.Dial(ctx, sttURL, &websocket.DialOptions{HTTPClient: s.client(),
		HTTPHeader: http.Header{"Authorization": {"Bearer " + s.key()}}})
	cancel()
	if err != nil {
		return err
	}
	ws.SetReadLimit(4 << 20)
	defer ws.CloseNow()
	cfg := map[string]any{"type": "session.update", "session": map[string]any{
		"type": "transcription",
		"audio": map[string]any{"input": map[string]any{
			"format":          map[string]any{"type": "audio/pcm", "rate": 24000},
			"transcription":   transcribeCfg(),
			"turn_detection":  map[string]any{"type": "server_vad", "threshold": 0.3, "prefix_padding_ms": 300, "silence_duration_ms": 300},
			"noise_reduction": nil,
		}}}}
	b, _ := json.Marshal(cfg)
	if err := ws.Write(context.Background(), websocket.MessageText, b); err != nil {
		return err
	}
	s.mu.Lock()
	s.ws, s.since = ws, time.Now()
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		if s.ws == ws {
			s.ws = nil
		}
		s.mu.Unlock()
	}()
	deadline := time.Now().Add(20 * time.Minute)
	for {
		rctx, rcancel := context.WithDeadline(context.Background(), deadline)
		_, raw, err := ws.Read(rctx)
		rcancel()
		if err != nil {
			if time.Now().After(deadline) {
				return nil // плановое переподключение
			}
			return err
		}
		var ev map[string]any
		if json.Unmarshal(raw, &ev) != nil {
			continue
		}
		if os.Getenv("JARVIS_STT_DEBUG") != "" {
			log.Printf("stt event: %.300s", raw)
		}
		switch str(ev["type"]) {
		case "input_audio_buffer.speech_started":
			s.mu.Lock()
			s.speech = true
			s.mu.Unlock()
		case "input_audio_buffer.speech_stopped":
			s.mu.Lock()
			s.speech = false
			s.mu.Unlock()
		case "conversation.item.input_audio_transcription.delta":
			s.mu.Lock()
			s.partial += str(ev["delta"])
			s.mu.Unlock()
		case "conversation.item.input_audio_transcription.completed":
			s.mu.Lock()
			ch, active := s.results, s.active
			s.partial = ""
			s.mu.Unlock()
			if active && ch != nil {
				select {
				case ch <- str(ev["transcript"]):
				default:
				}
			}
		case "error":
			log.Printf("быстрая расшифровка: %s", errMessage(ev))
		}
	}
}

// Ready — соединение открыто.
func (s *STT) Ready() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ws != nil
}

// Speaking — человек говорит прямо сейчас (фраза началась и ещё не закончилась).
func (s *STT) Speaking() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active && s.speech
}

// Begin — начать захват: дальше Feed(звук), фразы приходят в канал.
func (s *STT) Begin(preroll ...[]int16) (<-chan string, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	if s.ws == nil {
		s.mu.Unlock()
		return nil, errors.New("нет соединения")
	}
	ch := make(chan string, 4)
	s.results, s.partial, s.speech, s.active = ch, "", false, false
	s.mu.Unlock()
	if err := s.write(map[string]any{"type": "input_audio_buffer.clear"}); err != nil {
		return nil, err
	}
	for _, pcm := range preroll {
		if len(pcm) > 0 {
			if err := s.write(sttAudio(pcm)); err != nil {
				return nil, err
			}
		}
	}
	s.mu.Lock()
	s.active = true
	s.mu.Unlock()
	return ch, nil
}

func (s *STT) End() {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	s.active = false
	s.mu.Unlock()
	_ = s.write(map[string]any{"type": "input_audio_buffer.clear"})
}

// write вызывается с writeMu: иначе новые кадры могут обогнать начало записи.
func (s *STT) write(v any) error {
	s.mu.Lock()
	ws := s.ws
	s.mu.Unlock()
	if ws == nil {
		return errors.New("нет соединения")
	}
	b, _ := json.Marshal(v)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return ws.Write(ctx, websocket.MessageText, b)
}

// Feed — 16 кГц → 24 кГц (линейная интерполяция) и в OpenAI.
func (s *STT) Feed(pcm []int16) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.Lock()
	active := s.active && s.ws != nil
	s.mu.Unlock()
	if !active || len(pcm) == 0 {
		return
	}
	_ = s.write(sttAudio(pcm))
}

func sttAudio(pcm []int16) map[string]any {
	n := len(pcm) * 3 / 2
	out := make([]byte, 2*n)
	for i := 0; i < n; i++ {
		pos := float64(i) * 2 / 3
		j := int(pos)
		f := pos - float64(j)
		a := float64(pcm[j])
		b := a
		if j+1 < len(pcm) {
			b = float64(pcm[j+1])
		}
		v := (a + (b-a)*f) * 10 // +20 дБ: после обработки колонки голос тихий (как и для GPT-Live)
		v = max(-32767, min(32767, v))
		binary.LittleEndian.PutUint16(out[2*i:], uint16(int16(v)))
	}
	return map[string]any{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(out)}
}

func transcribeCfg() map[string]any {
	c := map[string]any{"model": "gpt-4o-mini-transcribe"}
	if l := speechLang(); l != "" {
		c["language"] = l
	}
	return c
}
