package main

// Замер громкости голосов движков — без звука в динамике. Каждый движок говорит одну и ту же фразу,
// меряем уровень речи (RMS по кадрам 20 мс, где есть голос) и пики. Запуск на колонке:
//   touch /tmp/jarvisd/voicetest && kill -USR1 <pid>
// Итог — в журнале: «громкость голоса: …».

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"
)

var voiceTestHook func()

const voiceTestPhrase = "Привет! Я Джарвис. Сегодня отличный день, чтобы что-нибудь сделать. Чем могу помочь?"

type voiceLevel struct {
	rms, peak, secs float64 // дБ от полной шкалы; секунд речи
}

func measureVoice(pcm []int16, rate int) voiceLevel {
	var m levelMeter
	m.add(pcm, rate)
	return m.level()
}

// levelMeter — уровень голоса модели за разговор (для журнала: видно, какой движок тише).
type levelMeter struct {
	mu   sync.Mutex
	rms  []float64
	peak float64
}

func (m *levelMeter) add(pcm []int16, rate int) {
	frame := rate / 50
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := 0; i+frame <= len(pcm) && len(m.rms) < 30000; i += frame {
		var sum float64
		for _, v := range pcm[i : i+frame] {
			x := float64(v)
			sum += x * x
			m.peak = math.Max(m.peak, math.Abs(x))
		}
		if r := math.Sqrt(sum / float64(frame)); r > 32768*math.Pow(10, -50.0/20) { // тише −50 дБ — пауза
			m.rms = append(m.rms, r)
		}
	}
}

func (m *levelMeter) level() voiceLevel {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.rms) == 0 {
		return voiceLevel{rms: -120, peak: -120}
	}
	rms := append([]float64(nil), m.rms...)
	sort.Float64s(rms)
	// уровень речи — средняя мощность громкой половины кадров (паузы и хвосты слогов не тянут вниз)
	var sum float64
	half := rms[len(rms)/2:]
	for _, r := range half {
		sum += r * r
	}
	db := func(x float64) float64 { return 20 * math.Log10(math.Max(x, 1)/32768) }
	return voiceLevel{rms: db(math.Sqrt(sum / float64(len(half)))), peak: db(m.peak), secs: float64(len(rms)) / 50}
}

func (m *levelMeter) String() string {
	l := m.level()
	if l.secs == 0 {
		return "голоса не было"
	}
	return fmt.Sprintf("уровень голоса %.1f дБ, пик %.1f дБ", l.rms, l.peak)
}

// recordVoice — модель говорит фразу; звук не играем, только собираем.
func (e *Engine) recordVoice(engine string) ([]int16, int, error) {
	s := e.store.Get()
	p := s.ActiveProfile()
	for _, q := range s.Profiles { // голос того ассистента, у которого этот движок
		if q.Engine == engine {
			p = q
			break
		}
	}
	p.Engine, p.WebSearch = engine, false
	if msg := keyMissing(s, engine); msg != "" {
		return nil, 0, errors.New(msg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	const instr = "Ты озвучиваешь готовый текст. Произнеси ровно то, что тебе дали, ничего не добавляя, и замолчи."
	noTools := NewTools(func() string { return "" }, DirectClient)
	var sess Session
	switch engine {
	case "gemini":
		sess = NewGeminiSession(ctx, s.GeminiKey, p, instr, noTools, e.proxy.LiveClientFor("gemini"), &Journal{})
	case "chatgpt":
		sess = NewChatGPTSession(ctx, e.chatgptAuth, p, instr, e.proxy.LiveClientFor("chatgpt"),
			func(context.Context, string) (string, error) {
				return "", errors.New("озвучка без бэкенда")
			}, &Journal{})
	case "grok":
		sess = NewGrokSession(ctx, s.GrokKey, p, instr, noTools, e.proxy.LiveClientFor("grok"), &Journal{})
	default:
		sess = NewLiveSession(ctx, s.OpenAIKey, p, instr, "", noTools, e.proxy.LiveClientFor("openai"), &Journal{})
	}
	defer sess.Close()
	if err := sess.Ready(15 * time.Second); err != nil {
		return nil, 0, err
	}
	if err := sess.Say(voiceTestPhrase); err != nil {
		return nil, 0, err
	}
	var rec []int16
	heard, quiet := false, time.Now()
	for !heard || time.Since(quiet) < 1500*time.Millisecond {
		select {
		case pcm, ok := <-sess.Audio():
			if !ok {
				return rec, sess.Rate(), nil
			}
			rec = append(rec, pcm...)
			if loud(pcm) {
				heard, quiet = true, time.Now()
			}
		case <-time.After(200 * time.Millisecond):
		case <-ctx.Done():
			if heard {
				return rec, sess.Rate(), nil
			}
			return nil, 0, errors.New("голос не прозвучал")
		}
	}
	return rec, sess.Rate(), nil
}

// VoiceTest — замерить все подключённые движки и записать итог в журнал.
func (e *Engine) VoiceTest() {
	e.mu.Lock()
	if e.busy || e.live != nil {
		e.mu.Unlock()
		log.Printf("громкость голоса: Джарвис занят — замер отложен")
		return
	}
	e.busy = true
	e.mu.Unlock()
	defer func() { e.mu.Lock(); e.busy = false; e.mu.Unlock() }()
	var out []string
	for _, eng := range []string{"gemini", "chatgpt"} /* платные поминутно движки не трогаем */ {
		if keyMissing(e.store.Get(), eng) != "" {
			continue
		}
		pcm, rate, err := e.recordVoice(eng)
		if err != nil {
			out = append(out, fmt.Sprintf("%s: %v", eng, err))
			continue
		}
		l := measureVoice(pcm, rate)
		boost := 1.0
		if eng == "chatgpt" {
			boost = (&ChatGPTSession{}).OutBoost()
		}
		out = append(out, fmt.Sprintf("%s: речь %.1f дБ, пик %.1f дБ, %.1f с речи (с усилением движка ×%.2g: речь %.1f дБ)",
			eng, l.rms, l.peak, l.secs, boost, l.rms+20*math.Log10(boost)))
	}
	log.Printf("громкость голоса: %s", strings.Join(out, "; "))
}

func (m *levelMeter) reset() {
	m.mu.Lock()
	m.rms, m.peak = nil, 0
	m.mu.Unlock()
}

// boostPCM — копия звука с усилением db (с ограничением по краям шкалы); db <= 0 — тот же срез.
func boostPCM(pcm []int16, db int) []int16 {
	if db <= 0 || len(pcm) == 0 {
		return pcm
	}
	k := math.Pow(10, float64(db)/20)
	out := make([]int16, len(pcm))
	for i, v := range pcm {
		out[i] = int16(max(-32767, min(32767, float64(v)*k)))
	}
	return out
}
