package main

// Grok Voice (xAI): живой разговор голос-в-голос, протокол почти как у OpenAI Realtime — веб-сокет,
// PCM 24 кГц, паузы определяет сервер, умный дом и музыка — функциями. xAI из России и Беларуси закрыт,
// поэтому всё идёт через VPN колонки.

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	grokRate  = 24000
	grokModel = "grok-voice-latest"
)

var grokURL = "wss://api.x.ai/v1/realtime?model=" + grokModel

var GrokVoiceGroups = []VoiceGroup{{"Grok", []VoiceInfo{
	{"ara", "тёплый женский"}, {"eve", "бодрый женский"}, {"leo", "уверенный мужской"},
	{"rex", "низкий мужской"}, {"sal", "спокойный"}}}}

func grokVoice(v string) string {
	v = strings.ToLower(v)
	for _, g := range GrokVoiceGroups {
		for _, x := range g.Voices {
			if x.ID == v {
				return v
			}
		}
	}
	return "rex"
}

type GrokSession struct {
	key, instructions string
	prof              Profile
	tools             *Tools
	client            *http.Client
	dialog

	ws        *websocket.Conn
	ctx       context.Context
	cancel    context.CancelFunc
	started   chan error
	once      sync.Once
	pendingMu sync.Mutex
	pending   []int16
	out       chan []int16
	gain      float64
	wmu       sync.Mutex
	playhead  time.Time
	calls     []grokCall
	last      int16 // последний отсчёт микрофона — для плавного перехода 16→24 кГц между кусками
}

type grokCall struct{ ID, Name, Args string }

func NewGrokSession(parent context.Context, key string, prof Profile, instructions string, tools *Tools,
	client *http.Client, journal *Journal) *GrokSession {
	ctx, cancel := context.WithCancel(parent)
	s := &GrokSession{key: key, prof: prof, instructions: instructions, tools: tools, client: client,
		ctx: ctx, cancel: cancel, started: make(chan error, 1), out: make(chan []int16, 64), gain: 5.6}
	s.dialog.journal, s.dialog.lastActivity = journal, time.Now()
	s.dialog.route, s.dialog.model = "Grok Voice", grokModel
	go s.run()
	return s
}

func (s *GrokSession) Audio() <-chan []int16 { return s.out }
func (s *GrokSession) Rate() int             { return grokRate }

func (s *GrokSession) start(err error) { s.once.Do(func() { s.started <- err }) }

func (s *GrokSession) session() map[string]any {
	tools := append([]any{}, s.tools.Definitions()...)
	if s.prof.WebSearch {
		tools = append(tools, map[string]any{"type": "web_search"})
	}
	return map[string]any{
		"voice":          grokVoice(s.prof.Voice),
		"instructions":   s.instructions + geminiRules,
		"turn_detection": map[string]any{"type": "server_vad", "silence_duration_ms": 600},
		"audio": map[string]any{
			"input":  map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": grokRate}, "transcription": grokTranscription()},
			"output": map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": grokRate}}},
		"tools": tools,
	}
}

func (s *GrokSession) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	s.wmu.Lock()
	defer s.wmu.Unlock()
	if s.ws == nil {
		return errors.New("Grok не подключён")
	}
	return s.ws.Write(ctx, websocket.MessageText, b)
}

func (s *GrokSession) run() {
	defer close(s.out)
	defer s.cancel()
	t0 := time.Now()
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	ws, resp, err := websocket.Dial(ctx, grokURL, &websocket.DialOptions{HTTPClient: s.client,
		HTTPHeader: http.Header{"Authorization": {"Bearer " + s.key}}})
	cancel()
	if err != nil {
		msg := "не подключиться к Grok: " + shortErr(err)
		if resp != nil && (resp.StatusCode == 401 || resp.StatusCode == 403) {
			msg = fmt.Sprintf("xAI не принял ключ или страну (ответ %d) — проверьте ключ, баланс и VPN", resp.StatusCode)
		}
		s.start(errors.New(msg))
		return
	}
	ws.SetReadLimit(16 << 20)
	s.wmu.Lock()
	s.ws = ws
	s.wmu.Unlock()
	defer ws.CloseNow()
	if err := s.send(map[string]any{"type": "session.update", "session": s.session()}); err != nil {
		s.start(err)
		return
	}
	for {
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		_, raw, err := ws.Read(ctx)
		cancel()
		if err != nil {
			s.start(fmt.Errorf("Grok не ответил: %s", closeReason(err)))
			return
		}
		var ev map[string]any
		_ = json.Unmarshal(raw, &ev)
		if str(ev["type"]) == "error" {
			s.start(fmt.Errorf("Grok: %s", errMessage(ev)))
			return
		}
		if str(ev["type"]) == "session.updated" {
			break
		}
	}
	log.Printf("Grok Voice: сессия открыта за %.2f с (голос %s)", time.Since(t0).Seconds(), grokVoice(s.prof.Voice))
	s.start(nil)
	go s.feeder()
	s.reader()
}

func (s *GrokSession) Ready(timeout time.Duration) error {
	select {
	case err := <-s.started:
		return err
	case <-time.After(timeout):
		return errors.New("Grok долго не отвечает")
	}
}

func (s *GrokSession) Feed(pcm []int16) {
	s.pendingMu.Lock()
	s.pending = queueMic(s.pending, pcm)
	s.pendingMu.Unlock()
}

// up24 — 16 кГц → 24 кГц: на каждые 2 отсчёта три, линейно между соседними.
func (s *GrokSession) up24(pcm []int16) []int16 {
	out := make([]int16, 0, len(pcm)*3/2+2)
	prev := float64(s.last)
	for i := 0; i+1 < len(pcm); i += 2 {
		a, b := float64(pcm[i]), float64(pcm[i+1])
		out = append(out, int16(prev+(a-prev)*2/3), int16(a+(b-a)/3), int16(b))
		prev = b
	}
	if len(pcm)%2 == 1 { // нечётный хвост — без интерполяции
		out = append(out, pcm[len(pcm)-1])
		prev = float64(pcm[len(pcm)-1])
	}
	s.last = int16(prev)
	return out
}

func (s *GrokSession) feeder() {
	silence := make([]int16, MicRate/10)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-tick.C:
		}
		s.pendingMu.Lock()
		pcm := s.pending
		s.pending = nil
		s.pendingMu.Unlock()
		if len(pcm) == 0 {
			pcm = silence
		}
		boosted := make([]int16, len(pcm))
		for i, v := range pcm {
			boosted[i] = int16(max(-32767, min(32767, float64(v)*s.gain)))
		}
		up := s.up24(boosted)
		data := make([]byte, 2*len(up))
		for i, v := range up {
			binary.LittleEndian.PutUint16(data[2*i:], uint16(v))
		}
		if err := s.send(map[string]any{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(data)}); err != nil {
			return
		}
	}
}

func (s *GrokSession) reader() {
	for {
		_, raw, err := s.ws.Read(s.ctx)
		if err != nil {
			if s.ctx.Err() == nil {
				log.Printf("Grok Voice: соединение закрыто: %s", closeReason(err))
			}
			return
		}
		var ev map[string]any
		if json.Unmarshal(raw, &ev) != nil {
			continue
		}
		switch str(ev["type"]) {
		case "response.output_audio.delta", "response.audio.delta":
			if !s.emit(str(ev["delta"])) {
				return
			}
		case "response.output_audio_transcript.delta", "response.audio_transcript.delta":
			s.onBot(str(ev["delta"]))
		case "conversation.item.input_audio_transcription.completed":
			s.onUser(str(ev["transcript"]))
		case "input_audio_buffer.speech_started": // человек перебил: несыгранное выбрасываем
			s.Mute()
			s.playhead = time.Time{}
		case "response.function_call_arguments.done":
			s.calls = append(s.calls, grokCall{ID: str(ev["call_id"]), Name: str(ev["name"]), Args: str(ev["arguments"])})
		case "response.done":
			if len(s.calls) > 0 {
				calls := s.calls
				s.calls = nil
				s.working(+1)
				go s.callTools(calls)
			}
		case "error":
			log.Printf("Grok Voice: ошибка: %s", errMessage(ev))
		}
	}
}

// emit — как у Gemini: звук приходит быстрее, чем звучит, держим впереди не больше полутора секунд.
func (s *GrokSession) emit(b64 string) bool {
	b, _ := base64.StdEncoding.DecodeString(b64)
	pcm := make([]int16, len(b)/2)
	loud := false
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(b[2*i:]))
		if pcm[i] > 400 || pcm[i] < -400 {
			loud = true
		}
	}
	const step = grokRate / 10
	for len(pcm) > 0 {
		n := min(step, len(pcm))
		chunk := pcm[:n]
		pcm = pcm[n:]
		now := time.Now()
		if s.playhead.Before(now) {
			s.playhead = now
		}
		if ahead := s.playhead.Sub(now) - 1500*time.Millisecond; ahead > 0 {
			select {
			case <-time.After(ahead):
			case <-s.ctx.Done():
				return false
			}
		}
		s.playhead = s.playhead.Add(time.Duration(n) * time.Second / grokRate)
		if loud {
			s.heardVoice()
		}
		select {
		case s.out <- chunk:
		case <-s.ctx.Done():
			return false
		}
	}
	return true
}

// callTools — выполнить все вызовы ответа, отдать результаты и ровно один response.create
// (на каждый вызов обязательно нужен ответ, иначе модель замолкает навсегда).
func (s *GrokSession) callTools(calls []grokCall) {
	defer s.working(-1)
	for _, c := range calls {
		ctx, cancel := context.WithTimeout(s.ctx, 40*time.Second)
		result := s.tools.Call(ctx, c.Name, c.Args)
		cancel()
		log.Printf("Grok Voice: %s(%s) → %s", c.Name, c.Args, result)
		_ = s.send(map[string]any{"type": "conversation.item.create", "item": map[string]any{
			"type": "function_call_output", "call_id": c.ID, "output": result}})
	}
	_ = s.send(map[string]any{"type": "response.create"})
}

func (s *GrokSession) Say(text string) error {
	if err := s.send(map[string]any{"type": "conversation.item.create", "item": map[string]any{"type": "message", "role": "user",
		"content": []any{map[string]any{"type": "input_text", "text": "Произнеси вслух ровно эту фразу, ничего не добавляя: " + text}}}}); err != nil {
		return err
	}
	return s.send(map[string]any{"type": "response.create"})
}

func (s *GrokSession) Close() { s.cancel() }

// grokModels — какие модели доступны ключу (для проверки связи и выбора текстовой модели).
func grokModels(ctx context.Context, client *http.Client, key string) ([]string, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.x.ai/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("xAI ответил %d: %s", resp.StatusCode, strings.TrimSpace(string(data[:min(len(data), 200)])))
	}
	var out struct {
		Data []struct{ ID string } `json:"data"`
	}
	_ = json.Unmarshal(data, &out)
	var ids []string
	for _, m := range out.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

func grokTranscription() map[string]any {
	if l := speechLang(); l != "" {
		return map[string]any{"language_hint": l}
	}
	return map[string]any{}
}
