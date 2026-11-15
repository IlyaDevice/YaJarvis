package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/coder/websocket"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

var elevenRealtimeModels = []string{"eleven_realtime_v1_mini", "eleven_realtime_v1_max"}

func elevenRealtimeModel(m string) string {
	for _, x := range elevenRealtimeModels {
		if x == m {
			return x
		}
	}
	return elevenRealtimeModels[0]
}

// Reuse the independent PCM playback queue; websocket reads never wait for sound.
type ElevenRealtimeSession struct{ *GeminiSession }

func NewElevenRealtimeSession(parent context.Context, key string, p Profile, instructions string, tools *Tools, client *http.Client, journal *Journal) *ElevenRealtimeSession {
	ctx, cancel := context.WithCancel(parent)
	p.LiveModel = elevenRealtimeModel(p.LiveModel)
	g := &GeminiSession{ctx: ctx, cancel: cancel, key: key, prof: p, instructions: instructions, tools: tools, client: client, started: make(chan error, 1), out: make(chan []int16, 8), gain: 5.6, audioWake: make(chan struct{}, 1), audioDone: make(chan struct{}), interrupts: make(chan struct{}, 1)}
	g.dialog.journal, g.dialog.route, g.dialog.model, g.dialog.lastActivity = journal, "Eleven Realtime", p.LiveModel, time.Now()
	s := &ElevenRealtimeSession{g}
	go s.runEleven()
	return s
}
func (s *ElevenRealtimeSession) Ready(t time.Duration) error {
	select {
	case err := <-s.started:
		return err
	case <-time.After(t):
		return errors.New("Eleven Realtime долго не отвечает")
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}
func (s *ElevenRealtimeSession) OutBoost() float64 { return 0.35 }
func (s *ElevenRealtimeSession) Close()            { s.cancel() }
func (s *ElevenRealtimeSession) config(soft bool) map[string]any {
	voice := s.prof.TTSVoice
	if voice == "" {
		voice = "brian"
	}
	return map[string]any{"type": "realtime", "model": s.prof.LiveModel, "instructions": softPrompt(s.instructions, soft) + russianSessionRules + geminiRules + s.tools.Context(), "output_modalities": []string{"audio"}, "tools": s.tools.Definitions(), "max_duration_seconds": 600,
		"audio": map[string]any{"input": map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": 24000}, "transcription": map[string]any{"language": "ru"}, "turn_detection": map[string]any{"type": "semantic_vad", "eagerness": "medium", "create_response": true, "interrupt_response": true}}, "output": map[string]any{"format": map[string]any{"type": "audio/pcm"}, "voice": voice}}}
}

// пропуск на сессию, взятый заранее (пока в комнате кто-то есть): после слова остаётся только подключиться
var elevenReady struct {
	sync.Mutex
	key, secret string
	at          time.Time
}

const elevenSecretFresh = 100 * time.Second // в пропуске зашито время для модели — не держим его дольше

func elevenSecretKey(key string, p Profile) string {
	return key + "|" + p.ID + "|" + p.LiveModel + "|" + p.TTSVoice + "|" + p.Prompt
}

// PrewarmEleven — взять пропуск заранее. Один раз используется и выбрасывается.
func PrewarmEleven(key string, p Profile, instructions string, tools *Tools, client *http.Client) {
	p.LiveModel = elevenRealtimeModel(p.LiveModel)
	ck := elevenSecretKey(key, p)
	elevenReady.Lock()
	fresh := elevenReady.key == ck && time.Since(elevenReady.at) < elevenSecretFresh-30*time.Second
	elevenReady.Unlock()
	if fresh {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	s := &ElevenRealtimeSession{&GeminiSession{ctx: ctx, key: key, prof: p, instructions: instructions, tools: tools, client: client}}
	secret, err := s.mintSecret(false)
	if err != nil {
		return
	}
	elevenReady.Lock()
	elevenReady.key, elevenReady.secret, elevenReady.at = ck, secret, time.Now()
	elevenReady.Unlock()
}

func takeElevenSecret(ck string) string {
	elevenReady.Lock()
	defer elevenReady.Unlock()
	if elevenReady.key != ck || time.Since(elevenReady.at) > elevenSecretFresh {
		return ""
	}
	s := elevenReady.secret
	elevenReady.key, elevenReady.secret = "", ""
	return s
}

// mintSecret — пропуск на одну сессию с готовыми настройками (характер, инструменты, голос).
func (s *ElevenRealtimeSession) mintSecret(soft bool) (string, error) {
	b, _ := json.Marshal(map[string]any{"session": s.config(soft), "expires_after": map[string]any{"seconds": 600}})
	req, _ := http.NewRequestWithContext(s.ctx, http.MethodPost, elevenBase+"/v1/convai/realtime/client_secrets", bytes.NewReader(b))
	req.Header.Set("xi-api-key", s.key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	if resp.StatusCode/100 != 2 {
		err = elevenError(resp, s.key)
		resp.Body.Close()
		return "", err
	}
	var auth struct {
		Value string `json:"value"`
	}
	err = json.NewDecoder(resp.Body).Decode(&auth)
	resp.Body.Close()
	if err != nil || auth.Value == "" {
		return "", errors.New("Eleven Realtime не выдал вход для сессии")
	}
	return auth.Value, nil
}

func (s *ElevenRealtimeSession) dialEleven(soft bool) (*websocket.Conn, error) {
	t0 := time.Now()
	secret, how := "", "пропуск заранее"
	if !soft {
		secret = takeElevenSecret(elevenSecretKey(s.key, s.prof))
	}
	if secret == "" {
		var err error
		if secret, err = s.mintSecret(soft); err != nil {
			return nil, err
		}
		how = fmt.Sprintf("пропуск за %.2f с", time.Since(t0).Seconds())
	}
	t1 := time.Now()
	auth := struct{ Value string }{secret}
	ctx, cancel := context.WithTimeout(s.ctx, 12*time.Second)
	ws, _, err := websocket.Dial(ctx, "wss://api.elevenlabs.io/v1/convai/realtime", &websocket.DialOptions{HTTPClient: s.client, HTTPHeader: http.Header{"Authorization": {"Bearer " + auth.Value}}})
	cancel()
	if err != nil {
		return nil, errors.New("не удалось подключиться к Eleven Realtime")
	}
	ws.SetReadLimit(8 << 20)
	for {
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		_, raw, err := ws.Read(ctx)
		cancel()
		if err != nil {
			if quotaText(closeReason(err)) {
				elevenQuota(s.key, "Realtime: "+closeReason(err))
			}
			ws.CloseNow()
			return nil, fmt.Errorf("Eleven Realtime: %s", closeReason(err))
		}
		var ev map[string]any
		_ = json.Unmarshal(raw, &ev)
		if str(ev["type"]) == "error" {
			if quotaText(errMessage(ev)) {
				elevenQuota(s.key, "Realtime: "+errMessage(ev))
			}
			ws.CloseNow()
			return nil, fmt.Errorf("Eleven Realtime: %s", errMessage(ev))
		}
		if str(ev["type"]) == "session.created" || str(ev["type"]) == "session.updated" {
			break
		}
	}
	log.Printf("Eleven Realtime: %s, соединение и сессия за %.2f с", how, time.Since(t1).Seconds())
	return ws, nil
}

func (s *ElevenRealtimeSession) runEleven() {
	defer close(s.out)
	defer s.cancel()
	// модерация ElevenLabs иногда отклоняет характер (например, «матерись») — и через секунду пропускает тот же.
	// Пробуем ещё раз, а на третий раз смягчаем для ElevenLabs только строки про мат.
	var ws *websocket.Conn
	var err error
	for try := 0; try < 3; try++ {
		ws, err = s.dialEleven(try == 2)
		if err == nil || !policyRefusal(err) || s.ctx.Err() != nil {
			break
		}
		log.Printf("Eleven Realtime: модерация не пропустила характер (попытка %d) — пробую ещё раз%s", try+1, map[bool]string{true: ", без строк про мат"}[try == 1])
	}
	if err != nil {
		s.started <- err
		return
	}
	s.ws = ws
	defer ws.CloseNow()
	log.Printf("Eleven Realtime: готов %s", s.prof.LiveModel)
	s.started <- nil
	go s.audioPump()
	defer func() { s.cancel(); <-s.audioDone }()
	go s.feedEleven()
	calls := []grokCall{}
	seen := map[string]bool{}
	for {
		_, raw, err := ws.Read(s.ctx)
		if err != nil {
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
			s.dialog.mu.Lock()
			s.dialog.muted = false
			s.dialog.mu.Unlock()
		case "input_audio_buffer.speech_started":
			s.Mute()
			select {
			case s.interrupts <- struct{}{}:
			default:
			}
		case "response.function_call_arguments.done":
			id := str(ev["call_id"])
			if !seen[id] {
				seen[id] = true
				calls = append(calls, grokCall{ID: id, Name: str(ev["name"]), Args: str(ev["arguments"])})
			}
		case "response.done":
			if len(calls) > 0 {
				batch := calls
				calls = nil
				s.working(1)
				go s.elevenTools(batch)
			}
		case "error":
			if quotaText(errMessage(ev)) {
				elevenQuota(s.key, "Realtime: "+errMessage(ev))
			}
			s.FinishTurn("Eleven Realtime: " + errMessage(ev))
		}
	}
}
func (s *ElevenRealtimeSession) feedEleven() {
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	convert := &GrokSession{}
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
			pcm = make([]int16, 1600)
		}
		boosted := make([]int16, len(pcm))
		for i, v := range pcm {
			boosted[i] = int16(max(-32767, min(32767, float64(v)*s.gain)))
		}
		up := convert.up24(boosted)
		b := make([]byte, len(up)*2)
		for i, v := range up {
			binary.LittleEndian.PutUint16(b[2*i:], uint16(v))
		}
		if s.send(map[string]any{"type": "input_audio_buffer.append", "audio": base64.StdEncoding.EncodeToString(b)}) != nil {
			return
		}
	}
}
func (s *ElevenRealtimeSession) elevenTools(calls []grokCall) {
	defer s.working(-1)
	for _, c := range calls {
		ctx, cancel := context.WithTimeout(s.ctx, 40*time.Second)
		result := s.tools.Call(ctx, c.Name, c.Args)
		cancel()
		_ = s.send(map[string]any{"type": "conversation.item.create", "item": map[string]any{"type": "function_call_output", "call_id": c.ID, "output": result}})
	}
	_ = s.send(map[string]any{"type": "response.create"})
}
func (s *ElevenRealtimeSession) Say(text string) error {
	if err := s.send(map[string]any{"type": "conversation.item.create", "item": map[string]any{"type": "message", "role": "user", "content": []any{map[string]any{"type": "input_text", "text": "Произнеси по-русски ровно эту фразу, ничего не добавляя: " + text}}}}); err != nil {
		return err
	}
	return s.send(map[string]any{"type": "response.create"})
}

// policyRefusal — ElevenLabs не пропустил инструкции по своей политике использования.
func policyRefusal(err error) bool {
	t := strings.ToLower(err.Error())
	return strings.Contains(t, "prohibited") || strings.Contains(t, "violate")
}

// softPrompt — без строк, где характер просят материться (только для ElevenLabs и только после отказа).
func softPrompt(text string, soft bool) string {
	if !soft {
		return text
	}
	var out []string
	for _, line := range strings.Split(text, "\n") {
		l := strings.ToLower(line)
		if strings.Contains(l, "матер") || strings.Contains(l, " мат") || strings.HasPrefix(strings.TrimSpace(l), "мат") {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n") + "\n\nМожешь говорить грубовато и по-простому, но без мата."
}
