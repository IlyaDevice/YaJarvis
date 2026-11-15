package main

// Подсветка. Универсально через /sys/class/leds: светодиоды с цветом в имени
// («micro:red:ring1», «…:green:…») группируются по цвету. Перед своим режимом запоминаем,
// что горело у Алисы, и возвращаем как было. Нет подходящих светодиодов — подсветки просто нет.
//
// Кольцо живое: «слушаю» — белый свет плавно загорается и чуть дышит; «думаю» — бирюзовый огонёк бежит
// по кругу; «говорю» — фиолетовый свет вспыхивает в такт голосу.

import (
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type LedMode int

const (
	LedOff LedMode = iota
	LedListening
	LedThinking
	LedSpeaking
)

type ledChan struct {
	path  string
	color string // red | green | blue | white
	max   int
	group int    // номер светодиода на кольце (у одного светодиода три цвета)
	last  int    // что записано сейчас — одинаковое не пишем повторно
	trig  string // штатный триггер ядра (Лайт 2: led_ring_fb), пока кольцом управляет Джарвис — «none»
}

// currentTrigger — активный триггер светодиода: в файле trigger он в [скобках].
func currentTrigger(raw string) string {
	for _, f := range strings.Fields(raw) {
		if strings.HasPrefix(f, "[") && strings.HasSuffix(f, "]") {
			return strings.Trim(f, "[]")
		}
	}
	return ""
}

// takeTriggers / restoreTriggers — у Лайт 2 кольцо ведёт триггер led_ring_fb и сразу перетирает яркость,
// которую пишет Джарвис. На время своей анимации ставим «none», потом возвращаем как было.
func (l *Leds) takeTriggers() {
	for i := range l.chans {
		c := &l.chans[i]
		tp := filepath.Join(filepath.Dir(c.path), "trigger")
		b, err := os.ReadFile(tp)
		if err != nil {
			continue
		}
		if t := currentTrigger(string(b)); t != "" && t != "none" {
			c.trig = t
			_ = os.WriteFile(tp, []byte("none"), 0o644)
		}
	}
}

func (l *Leds) restoreTriggers() {
	for i := range l.chans {
		c := &l.chans[i]
		if c.trig != "" {
			_ = os.WriteFile(filepath.Join(filepath.Dir(c.path), "trigger"), []byte(c.trig), 0o644)
			c.trig = ""
		}
	}
}

type Leds struct {
	// Source — подсветка активного ассистента (цвета, яркость) и общий выключатель; читается,
	// когда кольцо загорается, — смена ассистента подхватывается сразу.
	Source func() LedSettings

	mu      sync.Mutex
	palette map[LedMode]map[string]float64
	off     bool // подсветка Джарвиса выключена в настройках
	preview int  // номер текущего показа цвета из настроек
	chans   []ledChan
	clock   []ledChan // сегменты LED-часов (Мини 3, «bergamot:white:clockG1»): не кольцо, у них своя анимация
	saved   map[string]int
	mode    LedMode
	until   time.Time

	groups int
	since  time.Time // когда включился текущий режим
	level  float64   // громкость голоса 0..1 (сглаженная)
	target float64
	hear   float64 // громкость того, кто говорит в микрофон, 0..1 (сглаженная) — линия на часах
	hearT  float64
	frames int
	lvlAt  time.Time // когда голос последний раз сообщил громкость
	phase  LedMode   // что делает Джарвис (даже если подсветки нет или она выключена) — для панели
}

func NewLeds() *Leds {
	l := &Leds{saved: map[string]int{}, palette: ledPalette}
	entries, _ := filepath.Glob("/sys/class/leds/*")
	sort.Strings(entries)
	for _, e := range entries {
		name := strings.ToLower(filepath.Base(e))
		color := ""
		for _, c := range []string{"red", "green", "blue", "white"} {
			if strings.Contains(name, c) {
				color = c
			}
		}
		if color == "" || strings.Contains(name, "mute") {
			continue
		}
		max := readInt(filepath.Join(e, "max_brightness"))
		if max <= 0 {
			max = 255
		}
		if i := strings.LastIndex(name, "clock"); i >= 0 {
			l.clock = append(l.clock, ledChan{path: filepath.Join(e, "brightness"), color: name[i+len("clock"):], max: max, last: -1})
			continue
		}
		l.chans = append(l.chans, ledChan{path: filepath.Join(e, "brightness"), color: color, max: max, last: -1,
			group: groupOf(strings.Replace(name, color, "", 1))})
	}
	l.groups = len(ledGroups)
	go l.watchdog()
	go l.animate()
	return l
}

var ledGroups = map[string]int{}

// groupOf — номер светодиода по его имени без цвета («micro::ring2»).
func groupOf(key string) int {
	if _, ok := ledGroups[key]; !ok {
		ledGroups[key] = len(ledGroups)
	}
	return ledGroups[key]
}

func (l *Leds) Available() bool { return len(l.chans)+len(l.clock) > 0 }

// Hear — громкость голоса в микрофоне (0..1), пока Джарвис слушает: на часах Мини 3 линия по центру
// становится шире и ярче, как у Алисы.
func (l *Leds) Hear(v float64) {
	if len(l.clock) == 0 {
		return
	}
	l.mu.Lock()
	l.hearT = math.Max(l.hearT, math.Min(1, v))
	l.mu.Unlock()
}

// clockShape — яркость сегмента часов 0..1. Рисуем только средние черточки (G0…G3): горизонтальная
// линия от центра, её ширина и яркость идут за голосом. «Думаю» — черточка бегает по цифрам.
func (l *Leds) clockShape(seg string, t float64) float64 {
	if len(seg) != 2 || seg[0] != 'g' || seg[1] < '0' || seg[1] > '3' {
		return 0 // цифры, двоеточие, градус — гасим, пока Джарвис на связи
	}
	pos := int(seg[1] - '0')
	inner := pos == 1 || pos == 2
	switch l.mode {
	case LedThinking:
		c := math.Cos(2 * math.Pi * (t*1.2 - float64(pos)/4))
		return 0.1 + 0.9*math.Pow(math.Max(0, c), 2)
	case LedListening, LedSpeaking:
		v := l.hear
		if l.mode == LedSpeaking {
			v = l.level
		}
		if inner {
			return math.Min(1, t/0.2) * (0.2 + 0.8*math.Min(1, v/0.35))
		}
		return math.Min(1, math.Max(0, (v-0.3)/0.35))
	}
	return 0
}

// Level — громкость голоса Джарвиса прямо сейчас (0..1): кольцо вспыхивает в такт.
func (l *Leds) Level(v float64) {
	l.mu.Lock()
	l.target = math.Max(l.target, math.Min(1, v))
	l.lvlAt = time.Now()
	l.mu.Unlock()
}

// shape — яркость светодиода (0..1) в режиме mode через t секунд после включения режима.
func (l *Leds) shape(mode LedMode, group int, t float64) float64 {
	switch mode {
	case LedListening: // загорается и глубоко дышит
		return math.Min(1, t/0.25) * (0.5 + 0.5*(0.5+0.5*math.Cos(2*math.Pi*t/2.8)))
	case LedThinking:
		n := float64(max(1, l.groups))
		c := math.Cos(2 * math.Pi * (t*1.2 - float64(group)/n))
		return 0.15 + 0.85*math.Pow(math.Max(0, c), 2)
	case LedSpeaking:
		if time.Since(l.lvlAt) > 400*time.Millisecond { // голоса нет (проба в настройках) — отбиваем ритм сами
			return 0.2 + 0.8*math.Pow(math.Max(0, math.Sin(2*math.Pi*t*1.7)), 3)
		}
		return 0.3 + 0.7*l.level
	}
	return 0
}

// frame — один кадр анимации (вызывается с захваченным mu).
func (l *Leds) frame() {
	if l.mode == LedOff {
		return
	}
	l.level = l.level*0.6 + l.target*0.4
	l.target *= 0.5
	l.hear = l.hear*0.6 + l.hearT*0.4
	l.hearT *= 0.5
	t := time.Since(l.since).Seconds()
	if len(l.clock) > 0 {
		l.frames++
		k := 0.0 // яркость часов — по самому яркому цвету режима (учитывает яркость из настроек)
		for _, v := range l.palette[l.mode] {
			k = math.Max(k, v)
		}
		k = math.Min(1, k*1.6)
		for i := range l.clock {
			c := &l.clock[i]
			if l.frames%12 == 0 { // раз в полсекунды переписываем всё: maind мог дорисовать время
				c.last = -1
			}
			v := int(k * l.clockShape(c.color, t) * float64(c.max))
			if v != c.last {
				_ = os.WriteFile(c.path, []byte(strconv.Itoa(v)), 0o644)
				c.last = v
			}
		}
	}
	for i := range l.chans {
		c := &l.chans[i]
		v := int(l.palette[l.mode][c.color] * l.shape(l.mode, c.group, t) * float64(c.max))
		if v != c.last {
			_ = os.WriteFile(c.path, []byte(strconv.Itoa(v)), 0o644)
			c.last = v
		}
	}
}

func (l *Leds) animate() {
	if !l.Available() {
		return
	}
	for range time.Tick(40 * time.Millisecond) {
		l.mu.Lock()
		l.frame()
		l.mu.Unlock()
	}
}

// уровни 0..1 для каждого цвета
var ledPalette = map[LedMode]map[string]float64{
	LedListening: {"red": 0.43, "green": 0.43, "blue": 0.43, "white": 0.43}, // ровный белый — «слушаю»
	LedThinking:  {"red": 0, "green": 0.43, "blue": 0.6, "white": 0.2},      // бирюзовый — «думаю»
	LedSpeaking:  {"red": 0.2, "green": 0.1, "blue": 0.55, "white": 0.3},    // фиолетовый — «говорю»
}

// ledColor — уровни каналов для цвета #rrggbb при яркости 0..100.
func ledColor(hex string, bright int) map[string]float64 {
	k := float64(max(0, min(100, bright))) / 100
	r, g, b := parseHexColor(hex)
	return map[string]float64{"red": r * k, "green": g * k, "blue": b * k, "white": min(r, g, b) * k}
}

func paletteOf(s LedSettings) map[LedMode]map[string]float64 {
	return map[LedMode]map[string]float64{LedListening: ledColor(s.Listening, s.Brightness),
		LedThinking: ledColor(s.Thinking, s.Brightness), LedSpeaking: ledColor(s.Speaking, s.Brightness)}
}

// Configure — цвета и яркость из настроек (общий выключатель — s.Enabled).
func (l *Leds) Configure(s LedSettings) {
	l.mu.Lock()
	l.palette, l.off = paletteOf(s), !s.Enabled
	l.mu.Unlock()
	if !s.Enabled {
		l.Set(LedOff)
	}
}

// Show — зажечь кольцо цветом color в манере режима mode на dur (кнопки в настройках:
// примерить цвет ассистента, «поиграть с подсветкой»). Работает и при выключенной подсветке.
func (l *Leds) Show(mode LedMode, color string, bright int, dur time.Duration) {
	l.mu.Lock()
	l.preview++
	n := l.preview
	p := map[LedMode]map[string]float64{}
	for k, v := range l.palette {
		p[k] = v
	}
	p[mode] = ledColor(color, bright)
	l.palette = p
	same := l.mode == mode
	if same { // этот режим уже горит — просто новый цвет, анимация продолжается без рывка
		l.frame()
	}
	l.mu.Unlock()
	if !same {
		l.set(mode, true)
	}
	l.mu.Lock()
	l.until = time.Now().Add(dur)
	l.mu.Unlock()
	time.AfterFunc(dur, func() {
		l.mu.Lock()
		mine := l.preview == n && l.mode == mode
		l.mu.Unlock()
		if mine {
			l.Set(LedOff)
		}
	})
}

func parseHexColor(s string) (r, g, b float64) {
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil || len(s) != 6 {
		return 1, 1, 1
	}
	return float64(v>>16&255) / 255, float64(v>>8&255) / 255, float64(v&255) / 255
}

func (l *Leds) Set(mode LedMode) { l.set(mode, false) }

// Phase — что делает Джарвис: слушает, думает, говорит (LedOff — ждёт).
func (l *Leds) Phase() LedMode {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.phase
}

func (l *Leds) set(mode LedMode, force bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !force {
		l.phase = mode
	}
	if l.mode == LedOff && mode != LedOff && !force && l.Source != nil { // загорается по-настоящему — цвета ассистента
		s := l.Source()
		l.palette, l.off = paletteOf(s), !s.Enabled
	}
	if mode == l.mode || !l.Available() || (l.off && mode != LedOff && !force) {
		return
	}
	if l.mode == LedOff { // запоминаем то, что было у Алисы
		for _, c := range l.chans {
			l.saved[c.path] = readInt(c.path)
		}
		for i := range l.clock {
			l.saved[l.clock[i].path] = readInt(l.clock[i].path)
		}
		l.hear, l.hearT = 0, 0
		l.takeTriggers()
	}
	l.mode, l.since = mode, time.Now()
	if mode == LedOff { // возвращаем то, что горело у Алисы
		for i := range l.chans {
			c := &l.chans[i]
			_ = os.WriteFile(c.path, []byte(strconv.Itoa(l.saved[c.path])), 0o644)
			c.last = -1
		}
		for i := range l.clock { // часы снова показывают время
			c := &l.clock[i]
			if v := l.saved[c.path]; v >= 0 {
				_ = os.WriteFile(c.path, []byte(strconv.Itoa(v)), 0o644)
			}
			c.last = -1
		}
		l.restoreTriggers() // штатная анимация Алисы снова ведёт кольцо
	} else {
		l.frame()
	}
	switch mode {
	case LedListening:
		l.until = time.Now().Add(20 * time.Second)
	case LedThinking, LedSpeaking:
		l.until = time.Now().Add(10 * time.Minute)
	default:
		l.until = time.Time{}
	}
}

// watchdog — подсветка не должна остаться гореть, если что-то пошло не так
func (l *Leds) watchdog() {
	for range time.Tick(time.Second) {
		l.mu.Lock()
		expired := !l.until.IsZero() && time.Now().After(l.until)
		l.mu.Unlock()
		if expired {
			l.Set(LedOff)
		}
	}
}

func readInt(path string) int {
	b, err := os.ReadFile(path)
	if err != nil {
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		return -1
	}
	return n
}
