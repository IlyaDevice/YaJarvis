package main

// Второй голосовой движок — Gemini Live (Google). Тот же живой разговор, что у GPT-Live: микрофон идёт
// в сессию непрерывно, голос модели — обратно; модель сама решает, когда ей отвечать, и уступает, если перебили.
// Протокол: WebSocket BidiGenerateContent, сообщения setup / realtimeInput / serverContent / toolCall.
// Цена (ноябрь 2026): звук на вход $0,005 за минуту, на выход $0,018 за минуту; есть бесплатный уровень.

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const geminiHost = "generativelanguage.googleapis.com"
const geminiRate = 24000 // частота голоса модели

var geminiURL = "wss://" + geminiHost + "/ws/google.ai.generativelanguage.v1beta.GenerativeService.BidiGenerateContent"

var GeminiModels = []string{"gemini-3.8-live", "gemini-3.8-live-extended-thinking"}

var GeminiVoiceGroups = []VoiceGroup{{"Gemini", []VoiceInfo{
	{"Charon", "деловой"}, {"Kore", "твёрдый"}, {"Puck", "бодрый"}, {"Zephyr", "яркий"}, {"Fenrir", "энергичный"},
	{"Leda", "юный"}, {"Orus", "твёрдый"}, {"Aoede", "лёгкий"}, {"Callirrhoe", "спокойный"}, {"Autonoe", "яркий"},
	{"Enceladus", "с придыханием"}, {"Iapetus", "чёткий"}, {"Umbriel", "спокойный"}, {"Algieba", "мягкий"},
	{"Despina", "мягкий"}, {"Erinome", "чёткий"}, {"Algenib", "хрипловатый"}, {"Rasalgethi", "деловой"},
	{"Laomedeia", "бодрый"}, {"Achernar", "тихий"}, {"Alnilam", "твёрдый"}, {"Schedar", "ровный"},
	{"Gacrux", "зрелый"}, {"Pulcherrima", "напористый"}, {"Achird", "дружелюбный"}, {"Zubenelgenubi", "непринуждённый"},
	{"Vindemiatrix", "нежный"}, {"Sadachbia", "живой"}, {"Sadaltager", "знающий"}, {"Sulafat", "тёплый"},
}}}

func geminiVoice(v string) string {
	for _, x := range GeminiVoiceGroups[0].Voices {
		if strings.EqualFold(x.ID, v) {
			return x.ID
		}
	}
	return "Charon"
}

func geminiModel(m string) string {
	for _, x := range GeminiModels {
		if x == m {
			return x
		}
	}
	return GeminiModels[0]
}

const geminiRules = "\n\nТы говоришь через умную колонку в комнате, голосом. Отвечай по-русски, живо и коротко, как в разговоре. " +
	"Тебя можно перебить — тогда сразу замолкай и слушай. Не утверждай, что выполнил действие, пока инструмент не " +
	"подтвердил. Тебя позвали по имени «Джарвис» — на эту первую фразу отвечай обязательно, даже если её сказали быстро и неразборчиво. " +
	"Дальше, если с тобой не говорят (разговор людей в комнате, телевизор), — молчи. " +
	"Если говоришь, что что-то включишь или найдёшь для колонки, — в том же ответе вызывай инструмент, а не жди."

type GeminiSession struct {
	level             levelMeter
	key, instructions string
	prof              Profile
	tools             *Tools
	client            *http.Client
	dialog

	ws              *websocket.Conn
	ctx             context.Context
	cancel          context.CancelFunc
	started         chan error
	pendingMu       sync.Mutex
	pending         []int16
	out             chan []int16
	gain            float64
	wmu             sync.Mutex // запись в сокет из нескольких горутин
	playhead        time.Time  // до какого момента уже отдан звук (модель присылает его быстрее, чем он звучит)
	saidText        string     // что модель сказала в этом ответе
	turnTool        bool       // и вызывала ли она инструмент
	nudged          bool       // напоминание уже было — до следующей фразы человека не повторяем
	audioMu         sync.Mutex
	audioQueue      [][]int16
	audioSamples    int
	audioGeneration uint64
	audioWake       chan struct{}
	audioDone       chan struct{}
	interrupts      chan struct{}
}

// promiseRe — модель пообещала что-то включить («сейчас поищу что-нибудь для фона»).
var promiseRe = regexp.MustCompile(`(?i)(поищу|найду|подберу|включу|включаю|поставлю|запущу)`)

// checkPromise — ответ закончился обещанием включить, а инструмент модель не вызвала:
// один раз напоминаем ей сделать это сейчас, иначе разговор просто закончится тишиной.
func (s *GeminiSession) checkPromise() {
	text, tool := strings.TrimSpace(s.saidText), s.turnTool
	s.saidText, s.turnTool = "", false
	if tool || s.nudged || s.tools == nil || s.tools.Music == nil || text == "" || strings.Contains(text, "?") ||
		len([]rune(text)) > 120 || !promiseRe.MatchString(text) {
		return
	}
	s.nudged = true
	log.Printf("Gemini Live: пообещал «%s», но ничего не включил — напоминаю", text)
	_ = s.send(map[string]any{"clientContent": map[string]any{"turnComplete": true, "turns": []any{map[string]any{
		"role": "user", "parts": []any{map[string]any{"text": "(Колонка: ты пообещал включить, но не вызвал инструмент. " +
			"Вызови play_music прямо сейчас, запрос придумай сам по смыслу разговора. Вслух скажи только «Включаю».)"}}}}}})
}

func NewGeminiSession(parent context.Context, key string, prof Profile, instructions string, tools *Tools,
	client *http.Client, journal *Journal) *GeminiSession {
	ctx, cancel := context.WithCancel(parent)
	s := &GeminiSession{key: key, prof: prof, instructions: instructions, tools: tools, client: client,
		ctx: ctx, cancel: cancel, started: make(chan error, 1), out: make(chan []int16, 8), gain: 5.6,
		audioWake: make(chan struct{}, 1), audioDone: make(chan struct{}), interrupts: make(chan struct{}, 1)}
	s.dialog.journal, s.dialog.lastActivity = journal, time.Now()
	s.dialog.route, s.dialog.model = "Gemini Live", geminiModel(prof.LiveModel)
	go s.run()
	return s
}

func (s *GeminiSession) Audio() <-chan []int16          { return s.out }
func (s *GeminiSession) Rate() int                      { return geminiRate }
func (s *GeminiSession) Interruptions() <-chan struct{} { return s.interrupts }

// geminiTools — те же инструменты, что у GPT, в виде, который понимает Gemini (простая схема без вложенных словарей).
func geminiTools(t *Tools, webSearch bool) []any {
	var tools []any
	if t.HomeAssistant.ready() {
		d := haDefinition()
		tools = append(tools, map[string]any{"functionDeclarations": []any{map[string]any{"name": d["name"], "description": d["description"], "parameters": geminiSchema(d["parameters"]), "behavior": "BLOCKING"}}})
	}
	if len(t.Definitions()) > 0 {
		str := map[string]any{"type": "STRING"}
		list := func(desc string) map[string]any {
			return map[string]any{"type": "ARRAY", "items": str, "description": desc}
		}
		tools = append(tools, map[string]any{"functionDeclarations": []any{map[string]any{
			"name":     "smart_home",
			"behavior": "BLOCKING",
			"description": "Яндекс Умный дом: включить/выключить, яркость, цвет, температура белого, режимы (пылесос, кондиционер), " +
				"пауза, температура кондиционера; или запустить сценарий. Указывай только то, что просили.",
			"parameters": map[string]any{"type": "OBJECT", "properties": map[string]any{
				"device_ids":    list("id устройств или групп"),
				"on":            map[string]any{"type": "BOOLEAN", "description": "true — включить (пылесос: начать уборку), false — выключить (пылесос: на базу)"},
				"brightness":    map[string]any{"type": "INTEGER", "description": "яркость 1–100"},
				"color":         map[string]any{"type": "STRING", "description": "цвет #RRGGBB (красный #FF0000 и т. п.)"},
				"temperature_k": map[string]any{"type": "INTEGER", "description": "температура белого света в кельвинах: тёплый 2700, нейтральный 4500, холодный 6500"},
				"modes":         list("режимы в виде «название=значение», например cleanup_mode=wet_cleaning, work_speed=turbo, thermostat=cool"),
				"toggles":       list("переключатели в виде «название=true/false», например pause=true"),
				"ranges":        list("числа в виде «название=значение», например temperature=22"),
				"scenario_id":   map[string]any{"type": "STRING", "description": "id сценария, если просили его запустить (тогда остальное не нужно)"},
			}},
		}}})
	}
	if t.Music != nil {
		tools = append(tools, map[string]any{"functionDeclarations": []any{map[string]any{
			"name": "play_music", "behavior": "BLOCKING",
			"description": "Включить на колонке звук с YouTube: песню, подкаст, ролик, что-нибудь «на фон». Обычную музыку по названию включает Алиса, " +
				"но если человек просит включить что-то на колонке тебя (особенно без точного названия: «что-нибудь интересное», «чтобы болтало на фоне») — " +
				"вызывай сразу. Для подбора используй query «включи что-нибудь интересное из моих рекомендаций» и добавь жанр, если назван. Подбирай разговорный контент для прослушивания без экрана. Не обещай «сейчас поищу» без вызова.",
			"parameters": map[string]any{"type": "OBJECT", "properties": map[string]any{
				"query": map[string]any{"type": "STRING", "description": "что искать: исполнитель и название"}}, "required": []any{"query"}},
		}}})
	}
	if t.MusicCtl != nil {
		tools = append(tools, map[string]any{"functionDeclarations": []any{map[string]any{
			"name": "music_control", "behavior": "BLOCKING",
			"description": "Управлять тем, что сейчас играет с YouTube: пауза, продолжить, перемотка вперёд/назад, с начала, следующее видео, выключить, что играет.",
			"parameters": map[string]any{"type": "OBJECT", "properties": map[string]any{
				"command": map[string]any{"type": "STRING", "description": "команда по-русски: «пауза», «продолжи», «перемотай вперёд на 2 минуты», «назад на 30 секунд», «сначала», «следующее», «выключи музыку», «что играет»"}}, "required": []any{"command"}},
		}}})
	}
	if webSearch {
		tools = append(tools, map[string]any{"googleSearch": map[string]any{}})
	}
	return tools
}

// geminiHomeArgs — аргументы smart_home из вида Gemini («название=значение» списком) в общий вид инструмента.
func geminiSchema(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, val := range x {
			if k == "type" {
				if s, ok := val.(string); ok {
					out[k] = strings.ToUpper(s)
					continue
				}
			}
			out[k] = geminiSchema(val)
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, val := range x {
			out[i] = geminiSchema(val)
		}
		return out
	default:
		return v
	}
}

func geminiHomeArgs(args map[string]any) string {
	out := map[string]any{}
	for k, v := range args {
		list, isList := v.([]any)
		switch {
		case (k == "modes" || k == "toggles" || k == "ranges") && isList:
			m := map[string]any{}
			for _, item := range list {
				name, val, ok := strings.Cut(fmt.Sprint(item), "=")
				if !ok {
					continue
				}
				name, val = strings.TrimSpace(name), strings.TrimSpace(val)
				switch k {
				case "toggles":
					m[name] = val == "true"
				case "ranges":
					var f float64
					if _, err := fmt.Sscanf(val, "%g", &f); err == nil {
						m[name] = f
					}
				default:
					m[name] = val
				}
			}
			out[k] = m
		default:
			out[k] = v
		}
	}
	b, _ := json.Marshal(out)
	return string(b)
}

func (s *GeminiSession) setup() map[string]any {
	gen := map[string]any{"responseModalities": []string{"AUDIO"},
		"speechConfig": map[string]any{"voiceConfig": map[string]any{"prebuiltVoiceConfig": map[string]any{"voiceName": geminiVoice(s.prof.Voice)}}}}
	setup := map[string]any{
		"model":                    "models/" + geminiModel(s.prof.LiveModel),
		"generationConfig":         gen,
		"systemInstruction":        map[string]any{"parts": []any{map[string]any{"text": s.instructions + geminiRules + "\n\n" + s.tools.Context() + russianSessionRules}}},
		"inputAudioTranscription":  map[string]any{"languageCodes": []string{"ru-RU"}, "customVocabulary": []string{"Джарвис", "давай", "стендап", "апвоут"}},
		"outputAudioTranscription": map[string]any{},
		"realtimeInputConfig": map[string]any{
			"activityHandling": "START_OF_ACTIVITY_INTERRUPTS",
			"automaticActivityDetection": map[string]any{"disabled": false,
				"startOfSpeechSensitivity": "START_SENSITIVITY_HIGH", "prefixPaddingMs": 160, "silenceDurationMs": 600},
		},
	}
	if tools := geminiTools(s.tools, s.prof.WebSearch); len(tools) > 0 {
		setup["tools"] = tools
	}
	return map[string]any{"setup": setup}
}

func (s *GeminiSession) send(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()
	s.wmu.Lock()
	defer s.wmu.Unlock()
	return s.ws.Write(ctx, websocket.MessageText, b)
}

func (s *GeminiSession) run() {
	defer close(s.out)
	defer s.cancel()
	t0 := time.Now()
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	// ключ — в заголовке, а не в адресе: адрес попадает в тексты ошибок и в журнал
	ws, resp, err := websocket.Dial(ctx, geminiURL, &websocket.DialOptions{HTTPClient: s.client,
		HTTPHeader: http.Header{"x-goog-api-key": {s.key}}})
	cancel()
	if err != nil {
		msg := "не подключиться к Gemini: " + shortErr(err)
		if resp != nil && (resp.StatusCode == 400 || resp.StatusCode == 401 || resp.StatusCode == 403) {
			msg = fmt.Sprintf("Gemini не принял ключ или страну (ответ %d) — проверьте ключ и VPN", resp.StatusCode)
		}
		s.started <- errors.New(msg)
		return
	}
	ws.SetReadLimit(16 << 20)
	s.ws = ws
	defer ws.CloseNow()
	if err := s.send(s.setup()); err != nil {
		s.started <- err
		return
	}
	for {
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
		_, raw, err := ws.Read(ctx)
		cancel()
		if err != nil {
			s.started <- fmt.Errorf("Gemini не ответил: %s", closeReason(err))
			return
		}
		var ev map[string]any
		_ = json.Unmarshal(raw, &ev)
		if _, ok := ev["setupComplete"]; ok {
			break
		}
	}
	log.Printf("Gemini Live: сессия открыта за %.2f с (голос %s)", time.Since(t0).Seconds(), geminiVoice(s.prof.Voice))
	s.started <- nil
	go s.feeder()
	go s.audioPump()
	defer func() { s.cancel(); <-s.audioDone }()
	s.reader()
}

// closeReason — причина, с которой сервер закрыл соединение (там Gemini пишет, что не так с запросом).
func closeReason(err error) string {
	var ce websocket.CloseError
	if errors.As(err, &ce) && ce.Reason != "" {
		return ce.Reason
	}
	return shortErr(err)
}

func (s *GeminiSession) Ready(timeout time.Duration) error {
	select {
	case err := <-s.started:
		return err
	case <-time.After(timeout):
		return errors.New("Gemini долго не отвечает")
	}
}

// Feed — кадр микрофона.
func (s *GeminiSession) Feed(pcm []int16) {
	s.pendingMu.Lock()
	s.pending = queueMic(s.pending, pcm)
	s.pendingMu.Unlock()
}

func (s *GeminiSession) feeder() {
	silence := make([]byte, liveChunk*2)
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
		data := silence
		if len(pcm) > 0 {
			data = make([]byte, 2*len(pcm))
			for i, v := range pcm {
				x := float64(v) * s.gain // после обработки колонки голос тихий
				x = max(-32767, min(32767, x))
				binary.LittleEndian.PutUint16(data[2*i:], uint16(int16(x)))
			}
		}
		if err := s.send(map[string]any{"realtimeInput": map[string]any{"audio": map[string]any{
			"mimeType": "audio/pcm;rate=16000", "data": base64.StdEncoding.EncodeToString(data)}}}); err != nil {
			return
		}
	}
}

func (s *GeminiSession) reader() {
	for {
		_, raw, err := s.ws.Read(s.ctx)
		if err != nil {
			if s.ctx.Err() == nil {
				log.Printf("Gemini Live: соединение закрыто: %s", closeReason(err))
			}
			return
		}
		var ev struct {
			ServerContent *struct {
				ModelTurn *struct {
					Parts []struct {
						InlineData *struct {
							Data string `json:"data"`
						} `json:"inlineData"`
					} `json:"parts"`
				} `json:"modelTurn"`
				InputTranscription  *struct{ Text string } `json:"inputTranscription"`
				OutputTranscription *struct{ Text string } `json:"outputTranscription"`
				Interrupted         bool                   `json:"interrupted"`
				TurnComplete        bool                   `json:"turnComplete"`
			} `json:"serverContent"`
			ToolCall *struct {
				FunctionCalls []struct {
					ID   string         `json:"id"`
					Name string         `json:"name"`
					Args map[string]any `json:"args"`
				} `json:"functionCalls"`
			} `json:"toolCall"`
			GoAway any `json:"goAway"`
		}
		if json.Unmarshal(raw, &ev) != nil {
			continue
		}
		if c := ev.ServerContent; c != nil {
			if c.Interrupted { // человек перебил: то, что ещё не сыграно, выбрасываем
				s.Mute()
				s.saidText, s.turnTool = "", false
				select {
				case s.interrupts <- struct{}{}:
				default:
				}
				log.Printf("Gemini Live: перебили речью — буфер звука очищен")
			}
			if c.InputTranscription != nil && c.InputTranscription.Text != "" {
				s.onUser(c.InputTranscription.Text)
				s.dialog.mu.Lock()
				s.dialog.muted = false
				s.dialog.mu.Unlock()
				s.nudged = false
			}
			if c.OutputTranscription != nil && c.OutputTranscription.Text != "" {
				s.onBot(c.OutputTranscription.Text)
				s.saidText += c.OutputTranscription.Text
			}
			if c.TurnComplete {
				s.checkPromise()
			}
			if c.ModelTurn != nil && !c.Interrupted {
				for _, p := range c.ModelTurn.Parts {
					if p.InlineData != nil {
						if !s.emit(p.InlineData.Data) {
							return
						}
					}
				}
			}
		}
		if ev.ToolCall != nil {
			s.turnTool = true
			s.working(+1)
			go s.callTools(ev.ToolCall.FunctionCalls)
		}
		if ev.GoAway != nil {
			log.Printf("Gemini Live: сервер попросил завершить сессию")
		}
	}
}

// emit — отдать кусок голоса плееру. Модель присылает звук быстрее, чем он звучит, поэтому придерживаем его
// так, чтобы впереди было не больше полутора секунд: иначе очередь плеера переполнится, а перебить будет нельзя.
func (s *GeminiSession) emit(b64 string) bool {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(b)%2 != 0 {
		return true
	}
	pcm := make([]int16, len(b)/2)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(b[2*i:]))
	}
	s.audioMu.Lock()
	if s.audioSamples+len(pcm) > 60*geminiRate {
		s.audioMu.Unlock()
		log.Printf("Gemini Live: переполнен буфер голоса — закрываю сессию")
		s.cancel()
		return false
	}
	s.audioQueue = append(s.audioQueue, pcm)
	s.audioSamples += len(pcm)
	s.audioMu.Unlock()
	select {
	case s.audioWake <- struct{}{}:
	default:
	}
	return true
}

// Mute invalidates queued and in-flight audio without waiting for playback pacing.
func (s *GeminiSession) Mute() {
	s.dialog.Mute()
	s.audioMu.Lock()
	s.audioGeneration++
	s.audioQueue, s.audioSamples = nil, 0
	for {
		select {
		case _, ok := <-s.out:
			if !ok {
				s.audioMu.Unlock()
				return
			}
		default:
			s.audioMu.Unlock()
			return
		}
	}
}

func (s *GeminiSession) audioPump() {
	defer close(s.audioDone)
	for {
		s.audioMu.Lock()
		if len(s.audioQueue) == 0 {
			s.audioMu.Unlock()
			select {
			case <-s.audioWake:
				continue
			case <-s.ctx.Done():
				return
			}
		}
		pcm, gen := s.audioQueue[0], s.audioGeneration
		s.audioQueue[0] = nil
		s.audioQueue = s.audioQueue[1:]
		s.audioSamples -= len(pcm)
		s.audioMu.Unlock()
		if !s.playPCM(pcm, gen) {
			return
		}
	}
}

func (s *GeminiSession) playPCM(pcm []int16, gen uint64) bool {
	const step = geminiRate / 10 // плееру — кусками по 100 мс
	for len(pcm) > 0 {
		s.audioMu.Lock()
		current := gen == s.audioGeneration
		s.audioMu.Unlock()
		if !current {
			s.playhead = time.Time{}
			return true
		}
		n := min(step, len(pcm))
		chunk := pcm[:n]
		pcm = pcm[n:]
		now := time.Now()
		if s.playhead.Before(now) {
			s.playhead = now
		}
		if ahead := s.playhead.Sub(now) - 200*time.Millisecond; ahead > 0 {
			select {
			case <-time.After(ahead):
			case <-s.ctx.Done():
				return false
			}
		}
		s.playhead = s.playhead.Add(time.Duration(n) * time.Second / geminiRate)
		if loud(chunk) {
			s.heardVoice()
		}
		s.level.add(chunk, geminiRate)
		s.audioMu.Lock()
		if gen == s.audioGeneration {
			select {
			case s.out <- chunk:
			default:
			}
		}
		s.audioMu.Unlock()
		if s.ctx.Err() != nil {
			return false
		}
	}
	return true
}

func (s *GeminiSession) callTools(calls []struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}) {
	defer s.working(-1)
	var out []any
	for _, c := range calls {
		ctx, cancel := context.WithTimeout(s.ctx, 40*time.Second)
		args := geminiHomeArgs(c.Args)
		if c.Name != "smart_home" {
			b, _ := json.Marshal(c.Args)
			args = string(b)
		}
		result := s.tools.Call(ctx, c.Name, args)
		cancel()
		log.Printf("Gemini Live: %s(%s) → %s", c.Name, args, result)
		out = append(out, map[string]any{"id": c.ID, "name": c.Name, "response": map[string]any{"result": result}})
	}
	_ = s.send(map[string]any{"toolResponse": map[string]any{"functionResponses": out}})
}

// Say — попросить модель произнести фразу (прослушивание голоса, ответ на текстовое сообщение).
func (s *GeminiSession) Say(text string) error {
	return s.send(map[string]any{"clientContent": map[string]any{"turnComplete": true, "turns": []any{map[string]any{
		"role": "user", "parts": []any{map[string]any{"text": "Произнеси вслух ровно эту фразу, ничего не добавляя: " + text}}}}}})
}

func (s *GeminiSession) Close() {
	if s.level.level().secs > 0 {
		log.Printf("Gemini Live: за разговор — %s", s.level.String())
	}
	s.cancel()
}

// askGemini — текстовый вопрос модели Gemini (для сообщений из переписки, когда ключа OpenAI нет).
func askGemini(ctx context.Context, client *http.Client, key, instructions, text string, webSearch bool) (string, error) {
	body := map[string]any{
		"systemInstruction": map[string]any{"parts": []any{map[string]any{"text": instructions}}},
		"contents":          []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": text}}}},
	}
	if webSearch {
		body["tools"] = []any{map[string]any{"googleSearch": map[string]any{}}}
	}
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://"+geminiHost+"/v1beta/models/gemini-3.8-flash:generateContent", strings.NewReader(string(raw)))
	req.Header.Set("x-goog-api-key", key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct {
		Candidates []struct {
			Content struct {
				Parts []struct{ Text string }
			}
		}
		Error *struct{ Message string }
	}
	_ = json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != 200 {
		msg := fmt.Sprintf("Gemini ответил %d", resp.StatusCode)
		if out.Error != nil {
			msg += ": " + out.Error.Message
		}
		return "", errors.New(msg)
	}
	var b strings.Builder
	for _, c := range out.Candidates {
		for _, p := range c.Content.Parts {
			b.WriteString(p.Text)
		}
	}
	if s := strings.TrimSpace(b.String()); s != "" {
		return s, nil
	}
	return "", errors.New("модель ничего не ответила")
}
