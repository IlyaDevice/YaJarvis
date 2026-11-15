package main

// Общее для всех голосовых движков (GPT-Live, Gemini Live): что Engine умеет делать с разговором
// и учёт его хода — реплики в журнале, тишина, заглушение ответа.

import (
	"strings"
	"sync"
	"time"
)

// Session — открытый голосовой разговор с моделью. Микрофон идёт в Feed, голос модели приходит из Audio.
type Session interface {
	Feed(pcm []int16)      // кадр микрофона, 16 кГц
	Audio() <-chan []int16 // голос модели; канал закрывается, когда разговор оборвался
	Rate() int             // частота голоса модели
	Ready(timeout time.Duration) error
	Say(text string) error // попросить произнести ровно эту фразу
	Close()

	Mute()
	Muted() bool
	Annotate(route, answer string)
	SetText(text string)
	turnText() string
	FinishTurn(errText string)
	Utterance() (int64, string, time.Duration)
	Idle(idleS float64) bool
	UserTurns() int
}

// dialog — учёт хода разговора; встраивается в сессию каждого движка.
type dialog struct {
	journal      *Journal
	route, model string // как реплики подписаны в журнале

	mu           sync.Mutex
	lastActivity time.Time
	delegations  int // движок занят делом (инструмент, бэкенд) — молчание не считается концом разговора
	turn         *Turn
	turnUser     string
	turnBot      string
	turnBotT0    time.Time
	userEnd      time.Time
	muted        bool // ответ модели не играем (перебили словом «Джарвис» или выполнили команду сами)
	mutedAt      time.Time
	userTurns    int // сколько реплик человека было в этом разговоре
}

// onUser — очередной кусочек расшифровки речи человека.
func (s *dialog) onUser(d string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	s.lastActivity, s.userEnd = now, now
	// новая реплика: Джарвис уже успел поговорить (>1.5 с) — значит, это не запоздавший хвост расшифровки
	if s.turn == nil || (!s.turnBotT0.IsZero() && now.Sub(s.turnBotT0) > 1500*time.Millisecond) {
		s.nextTurnLocked()
	}
	if s.turnUser == "" {
		s.userTurns++
	}
	s.turnUser += d
	s.journal.Update(s.turn, func(t *Turn) { t.Text = strings.TrimSpace(s.turnUser) })
	if s.muted && now.Sub(s.mutedAt) > 600*time.Millisecond {
		s.muted = false // человек договорил новую просьбу — следующий ответ уже слушаем
	}
}

// onBot — очередной кусочек текста ответа модели.
func (s *dialog) onBot(d string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastActivity = time.Now()
	if s.turn == nil {
		s.nextTurnLocked()
	}
	if s.turnBotT0.IsZero() {
		s.turnBotT0 = time.Now()
	}
	s.turnBot += d
	s.journal.Update(s.turn, func(t *Turn) { t.Answer = strings.TrimSpace(s.turnBot) })
}

// heardVoice — модель произнесла слышимый звук: разговор жив; первый звук после реплики — задержка ответа.
func (s *dialog) heardVoice() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastActivity = time.Now()
	if t := s.turn; t != nil && t.AudioS == 0 && s.turnUser != "" && !s.userEnd.IsZero() {
		sec := time.Since(s.userEnd).Seconds()
		s.journal.Update(t, func(t *Turn) { t.AudioS = float64(int(sec*100+0.5)) / 100 })
	}
}

// working — движок начал (+1) или закончил (−1) дело, во время которого молчание нормально.
func (s *dialog) working(delta int) {
	s.mu.Lock()
	s.delegations = max(0, s.delegations+delta)
	s.lastActivity = time.Now()
	s.mu.Unlock()
}

// Utterance — текущая реплика человека: id реплики, текст и сколько прошло с последнего кусочка расшифровки.
func (s *dialog) Utterance() (int64, string, time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turn == nil || s.turnUser == "" {
		return 0, "", 0
	}
	return s.turn.ID, strings.TrimSpace(s.turnUser), time.Since(s.userEnd)
}

func (s *dialog) turnText() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turn == nil {
		return ""
	}
	return s.turn.Text
}

// SetText — текст реплики из быстрой расшифровки (движок мог не успеть).
func (s *dialog) SetText(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turn == nil {
		s.nextTurnLocked()
	}
	s.journal.Update(s.turn, func(t *Turn) { t.Text = text })
}

// Mute — не играть ответ модели (с этого момента и до новой реплики человека).
func (s *dialog) Mute() {
	s.mu.Lock()
	s.muted, s.mutedAt = true, time.Now()
	s.mu.Unlock()
}

func (s *dialog) Muted() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.muted
}

// Annotate — пометить текущую реплику в журнале (быстрая команда, стоп).
func (s *dialog) Annotate(route, answer string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.journal.Update(s.turn, func(t *Turn) {
		t.Route = route
		if answer != "" {
			t.Answer = answer
		}
	})
}

func (s *dialog) nextTurnLocked() {
	s.finishTurnLocked()
	s.turn = s.journal.New(s.route, s.model)
	s.journal.Update(s.turn, func(t *Turn) { t.Live = true })
	s.turnUser, s.turnBot, s.turnBotT0 = "", "", time.Time{}
}

func (s *dialog) finishTurnLocked() {
	if s.turn != nil {
		s.journal.Update(s.turn, func(t *Turn) { t.Live = false })
		s.journal.Remember(s.turn)
	}
	s.turn = nil
}

// FinishTurn — закрыть текущую реплику; errText — что пошло не так (покажется в переписке).
func (s *dialog) FinishTurn(errText string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if errText != "" {
		if s.turn == nil {
			s.turn = s.journal.New(s.route, s.model)
		}
		s.journal.Update(s.turn, func(t *Turn) { t.Error = errText })
	}
	s.finishTurnLocked()
}

// Idle — разговор закончен: обе стороны молчат idleS и движок ничем не занят.
// Модель только что сказала «сейчас поищу/включу» — значит, она ищет (поиск Google идёт на её стороне,
// нам его не видно): ждём дольше, чтобы не оборвать разговор посреди поиска.
func (s *dialog) Idle(idleS float64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if bot := strings.TrimSpace(s.turnBot); bot != "" && !strings.Contains(bot, "?") && promiseRe.MatchString(lastSentence(bot)) {
		idleS = max(idleS, 25)
	}
	return s.delegations == 0 && time.Since(s.lastActivity).Seconds() > idleS
}

// UserTurns — сколько раз человек что-то сказал в этом разговоре.
func (s *dialog) UserTurns() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.userTurns
}

// lastSentence — последнее предложение ответа.
func lastSentence(s string) string {
	s = strings.TrimRight(s, " .!…")
	if i := strings.LastIndexAny(s, ".!?…"); i >= 0 {
		return s[i+1:]
	}
	return s
}

// note — записать в журнал реплику, выполненную без разговора с моделью (стоп, команда Алисе, умный дом).
func (j *Journal) note(route, text, answer string) {
	t := j.New(route, "")
	j.Update(t, func(t *Turn) { t.Text, t.Answer = text, answer })
	j.Remember(t)
}

// Touch — разговор только что открылся: тишину считаем отсюда, а не с момента, когда начали соединяться
// (соединение может занять несколько секунд — человек за это время уже сказал фразу, и модель её обдумывает).
func (s *dialog) Touch() {
	s.mu.Lock()
	s.lastActivity = time.Now()
	s.mu.Unlock()
}
