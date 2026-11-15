package main

// GPT-Live (gpt-live-1): живой разговор голосом, как в приложении ChatGPT.
// Микрофон идёт в сессию непрерывно (100 мс кусками, в паузах — тишина), голос модели — обратно.
// Модель full-duplex: слушает, пока говорит, уступает, когда перебивают. Думает и вызывает
// инструменты бэкенд-модель Responses (delegation); пока она работает, Live говорит «секунду, проверяю».

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const liveURL = "wss://api.openai.com/v1/live/sessions"
const liveModel = "gpt-live-1"
const liveChunk = MicRate / 10

type VoiceInfo struct{ ID, Note string }
type VoiceGroup struct {
	Name   string      `json:"name"`
	Voices []VoiceInfo `json:"voices"`
}

var LiveVoiceGroups = []VoiceGroup{
	{"Стандартные", []VoiceInfo{{"cedar", "мужской"}, {"marin", "женский"}, {"ash", "мужской"}, {"alloy", "нейтральный"},
		{"ballad", "мужской"}, {"coral", "женский"}, {"echo", "мужской"}, {"sage", "женский"}, {"shimmer", "женский"}, {"verse", "мужской"}}},
	{"Английский акцент", []VoiceInfo{{"meridian", "мужской, Северная Америка"}, {"gleam", "женский, Северная Америка"},
		{"vesper", "мужской, британский"}, {"stone", "мужской, ирландский"}, {"willow", "женский, ирландский"},
		{"ripple", "мужской, австралийский"}, {"quartz", "женский, австралийский"}, {"cinder", "мужской, юг США"},
		{"delta", "женский, юг США"}, {"beacon", "мужской, филиппинский"}}},
	{"Немецкий", []VoiceInfo{{"flitz", "мужской"}, {"nuri", "женский"}, {"juni", "женский"}}},
	{"Французский", []VoiceInfo{{"sillage", "мужской"}, {"brise", "женский"}}},
	{"Португальский", []VoiceInfo{{"tempo", "мужской, бразильский"}, {"bossa", "женский, бразильский"}}},
	{"Японский", []VoiceInfo{{"shida", "мужской"}, {"harema", "женский"}}},
	{"Корейский", []VoiceInfo{{"noeul", "женский"}}},
	{"Хинди", []VoiceInfo{{"nira", "женский"}}},
}

var BackendModels = []string{"gpt-6-luna", "gpt-6-sol", "gpt-6.1-sol", "gpt-6.1-terra", "gpt-6-astra", "gpt-5.6-luna"}
var Efforts = []string{"none", "low", "medium", "high"}

func knownVoice(v string) bool {
	for _, g := range LiveVoiceGroups {
		for _, x := range g.Voices {
			if x.ID == v {
				return true
			}
		}
	}
	return false
}

const liveRules = "\n\nТы говоришь через умную колонку в комнате, голосом. Отвечай по-русски, живо и коротко, как в разговоре. " +
	"Тебя можно перебить — тогда сразу замолкай и слушай. Можешь и сам вставить реплику, если уместно. " +
	"Если нужно подумать, что-то узнать или управлять умным домом — коротко скажи «секунду, проверяю» " +
	"(или похоже, по-своему) и передай задачу бэкенду. Не утверждай, что выполнил действие, пока бэкенд не " +
	"подтвердил. Тебя позвали по имени «Джарвис» — на эту первую фразу отвечай обязательно, даже если её сказали быстро и неразборчиво. " +
	"Дальше, если с тобой не говорят (разговор людей в комнате, телевизор), — молчи."

const backendRules = "Ты бэкенд голосового ассистента Джарвиса в умной колонке. Транскрипты речи могут содержать ошибки — " +
	"пользуйся смыслом. Возвращай короткий фактический ответ: Джарвис сам скажет его голосом в своём стиле. " +
	"Текущие дата и время даны в контексте." + speakerContentRules

// Turn — одна реплика для журнала: заполняется на лету, панель видит её сразу.
type Turn struct {
	ID     int64   `json:"id"`
	TS     float64 `json:"ts"`
	Text   string  `json:"text"`
	Answer string  `json:"answer"`
	AudioS float64 `json:"audio_s,omitempty"`
	Live   bool    `json:"live,omitempty"`
	Route  string  `json:"route"`
	Model  string  `json:"model"`
	Error  string  `json:"error,omitempty"`
}

type LiveSession struct {
	key, instructions string
	prof              Profile
	voicePrompt       string
	tools             *Tools
	client            *http.Client
	dialog

	ws        *websocket.Conn
	ctx       context.Context
	cancel    context.CancelFunc
	started   chan error
	pendingMu sync.Mutex
	pending   []int16
	Out       chan []int16 // голос модели, 16 кГц
	gain      float64
}

func (s *LiveSession) Audio() <-chan []int16 { return s.Out }
func (s *LiveSession) Rate() int             { return MicRate }

func NewLiveSession(parent context.Context, key string, prof Profile, instructions, voicePrompt string,
	tools *Tools, client *http.Client, journal *Journal) *LiveSession {
	ctx, cancel := context.WithCancel(parent)
	s := &LiveSession{key: key, prof: prof, instructions: instructions, voicePrompt: voicePrompt, tools: tools,
		client: client, ctx: ctx, cancel: cancel, started: make(chan error, 1),
		Out: make(chan []int16, 400), gain: math.Pow(10, 15.0/20)}
	s.dialog.journal, s.dialog.lastActivity = journal, time.Now()
	s.dialog.route, s.dialog.model = "GPT-Live", fmt.Sprintf("gpt-live-1 → %s (%s)", prof.BackendModel, prof.Effort)
	go s.run()
	return s
}

func (s *LiveSession) sessionConfig() map[string]any {
	p := s.prof
	voice := p.Voice
	if !knownVoice(voice) && !strings.HasPrefix(voice, "voice_") {
		voice = "cedar"
	}
	var voiceVal any = voice
	instr := s.instructions + liveRules
	if strings.HasPrefix(voice, "voice_") {
		voiceVal = map[string]any{"id": voice}
		if s.voicePrompt != "" {
			instr += "\n\nКак звучит твой голос: " + s.voicePrompt
		}
	}
	effort := p.Effort
	if effort == "" {
		effort = "low"
	}
	model := p.BackendModel
	if model == "" {
		model = BackendModels[0]
	}
	tools := append([]any{}, s.tools.Definitions()...) // всегда массив, не null
	if p.WebSearch {
		tools = append(tools, map[string]any{"type": "web_search"})
	}
	resp := map[string]any{"model": model, "instructions": backendRules + s.tools.Context(), "tools": tools,
		"tool_choice": "auto", "reasoning": map[string]any{"effort": effort}}
	if p.Priority {
		resp["service_tier"] = "priority"
	}
	return map[string]any{"model": liveModel, "instructions": instr,
		"audio":      map[string]any{"format": map[string]any{"type": "audio/pcm", "rate": MicRate}, "output": map[string]any{"voice": voiceVal}},
		"delegation": map[string]any{"type": "responses", "responses": resp}}
}

func (s *LiveSession) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	return s.ws.Write(ctx, websocket.MessageText, b)
}

func (s *LiveSession) run() {
	defer close(s.Out)
	defer s.cancel()
	t0 := time.Now()
	opts := &websocket.DialOptions{HTTPClient: s.client, HTTPHeader: http.Header{"Authorization": {"Bearer " + s.key}}}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	ws, _, err := websocket.Dial(ctx, liveURL, opts)
	cancel()
	if err != nil && s.ctx.Err() == nil { // заранее открытое соединение могло «остыть» — пробуем свежее
		log.Printf("GPT-Live: первая попытка не удалась (%v) — открываю новое соединение", shortErr(err))
		ctx, cancel = context.WithTimeout(FreshDial(s.ctx), 10*time.Second)
		ws, _, err = websocket.Dial(ctx, liveURL, opts)
		cancel()
	}
	if err != nil {
		s.started <- fmt.Errorf("не подключиться к OpenAI: %w", err)
		return
	}
	ws.SetReadLimit(16 << 20)
	s.ws = ws
	defer ws.CloseNow()
	if err := s.send(map[string]any{"type": "session.start", "event_id": "start", "session": s.sessionConfig()}); err != nil {
		s.started <- err
		return
	}
	for {
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		_, raw, err := ws.Read(ctx)
		cancel()
		if err != nil {
			s.started <- fmt.Errorf("OpenAI не ответил: %w", err)
			return
		}
		var ev map[string]any
		_ = json.Unmarshal(raw, &ev)
		if ev["type"] == "session.started" {
			break
		}
		if ev["type"] == "error" {
			s.started <- fmt.Errorf("OpenAI: %v", errMessage(ev))
			return
		}
	}
	log.Printf("GPT-Live: сессия открыта за %.2f с (голос %s)", time.Since(t0).Seconds(), s.prof.Voice)
	s.started <- nil
	go s.feeder()
	s.reader()
}

func errMessage(ev map[string]any) string {
	if e, ok := ev["error"].(map[string]any); ok {
		return fmt.Sprint(e["message"])
	}
	return "ошибка"
}

func (s *LiveSession) Ready(timeout time.Duration) error {
	select {
	case err := <-s.started:
		return err
	case <-time.After(timeout):
		return errors.New("OpenAI долго не отвечает")
	}
}

// Feed — кадр микрофона.
func (s *LiveSession) Feed(pcm []int16) {
	s.pendingMu.Lock()
	s.pending = queueMic(s.pending, pcm)
	s.pendingMu.Unlock()
}

func (s *LiveSession) feeder() {
	silence := make([]byte, liveChunk*2)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	first := true
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-tick.C:
		}
		s.pendingMu.Lock()
		limit := MicRate * 12 / 100 // <= 1.2x real time after startup
		if first {
			limit, first = 4*MicRate, false
		}
		pcm := takeMic(&s.pending, limit)
		s.pendingMu.Unlock()
		data := silence
		if len(pcm) > 0 {
			data = make([]byte, 2*len(pcm))
			for i, v := range pcm {
				x := float64(v) * s.gain
				x = math.Max(-32767, math.Min(32767, x))
				binary.LittleEndian.PutUint16(data[2*i:], uint16(int16(x)))
			}
		}
		for i := 0; i < len(data); i += liveChunk * 2 {
			end := min(i+liveChunk*2, len(data))
			if err := s.send(map[string]any{"type": "session.input_audio.append",
				"audio": base64.StdEncoding.EncodeToString(data[i:end])}); err != nil {
				return
			}
		}
	}
}

func (s *LiveSession) reader() {
	for {
		_, raw, err := s.ws.Read(s.ctx)
		if err != nil {
			return
		}
		var ev map[string]any
		if json.Unmarshal(raw, &ev) != nil {
			continue
		}
		kind, _ := ev["type"].(string)
		switch kind {
		case "session.output_audio.delta":
			b, _ := base64.StdEncoding.DecodeString(str(ev["delta"]))
			pcm := make([]int16, len(b)/2)
			loud := false
			for i := range pcm {
				pcm[i] = int16(binary.LittleEndian.Uint16(b[2*i:]))
				if pcm[i] > 400 || pcm[i] < -400 {
					loud = true
				}
			}
			if loud {
				s.heardVoice()
			}
			select {
			case s.Out <- pcm:
			default:
			}
		case "session.input_transcript.delta":
			s.onUser(str(ev["delta"]))
		case "session.output_transcript.delta":
			s.onBot(str(ev["delta"]))
		case "session.delegation.created":
			s.working(+1)
		case "response.event":
			if inner, ok := ev["event"].(map[string]any); ok {
				s.backendEvent(inner)
			}
		case "error":
			log.Printf("GPT-Live: ошибка: %s", errMessage(ev))
		case "session.closed":
			return
		}
	}
}

func str(v any) string { s, _ := v.(string); return s }

func (s *LiveSession) backendEvent(ev map[string]any) {
	switch str(ev["type"]) {
	case "response.completed", "response.failed", "response.cancelled", "response.incomplete":
		s.working(-1)
	case "response.output_item.done":
		item, _ := ev["item"].(map[string]any)
		if str(item["type"]) == "function_call" {
			s.working(+1)
			go s.callTool(item)
		}
	}
}

func (s *LiveSession) callTool(item map[string]any) {
	defer s.working(-1)
	ctx, cancel := context.WithTimeout(s.ctx, 40*time.Second)
	result := s.tools.Call(ctx, str(item["name"]), str(item["arguments"]))
	cancel()
	log.Printf("GPT-Live: %s(%s) → %s", str(item["name"]), str(item["arguments"]), result)
	_ = s.send(map[string]any{"type": "response.item.create", "item": map[string]any{
		"type": "function_call_output", "call_id": item["call_id"], "output": result}})
	_ = s.send(map[string]any{"type": "response.create"})
}

// Say — попросить модель произнести фразу (прослушивание голоса в настройках).
func (s *LiveSession) Say(text string) error {
	return s.send(map[string]any{"type": "session.commentary.append", "event_id": "preview", "delegation_id": nil, "content": text})
}

func (s *LiveSession) Close() {
	if s.ws != nil {
		_ = s.send(map[string]any{"type": "session.close"})
		time.Sleep(200 * time.Millisecond)
	}
	s.cancel()
}
