package main

// Один чат ChatGPT по подписке. Каждый звонок GPT-Live — продолжение одной беседы: тот же номер беседы
// (thread-id) и реплики прошлых звонков в начале нового. Хранится на колонке (chatgpt-chat.json),
// переживает перезапуск; «Начать новый чат» в «Подключениях» начинает с чистого листа.

import (
	"encoding/json"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	chatKeepTurns = 40   // сколько реплик помнить
	chatKeepChars = 9000 // и не больше стольких букв в начале звонка: длинная история замедляет ответ
)

type ChatTurn struct {
	TS   int64  `json:"ts"`
	User string `json:"user,omitempty"`
	Bot  string `json:"bot,omitempty"`
}

type ChatThread struct {
	mu    sync.Mutex
	path  string
	ID    string     `json:"id"`
	Since int64      `json:"since"`
	Turns []ChatTurn `json:"turns"`
}

// chatgptChat — общий чат ChatGPT (main); nil — без общего чата, как раньше.
var chatgptChat *ChatThread

func LoadChatThread(path string) *ChatThread {
	c := &ChatThread{path: path}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, c)
	}
	if c.ID == "" {
		c.ID, c.Since = newID(), time.Now().Unix()
		c.saveLocked()
	}
	return c
}

func (c *ChatThread) saveLocked() {
	b, err := json.Marshal(c)
	if err != nil || c.path == "" {
		return
	}
	tmp := c.path + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, c.path)
	}
}

// Thread — номер беседы для ChatGPT (один на все звонки).
func (c *ChatThread) Thread() string {
	if c == nil {
		return ""
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ID
}

// Add — реплика закончилась: запомнить вопрос и ответ.
func (c *ChatThread) Add(user, bot string) {
	user, bot = strings.TrimSpace(user), strings.TrimSpace(bot)
	if c == nil || (user == "" && bot == "") {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Turns = append(c.Turns, ChatTurn{TS: time.Now().Unix(), User: user, Bot: bot})
	if len(c.Turns) > chatKeepTurns {
		c.Turns = c.Turns[len(c.Turns)-chatKeepTurns:]
	}
	c.saveLocked()
}

// Recent — последние реплики, всего не больше chars букв (старые отбрасываются первыми).
func (c *ChatThread) Recent(chars int) []ChatTurn {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	n, i := 0, len(c.Turns)
	for i > 0 {
		t := c.Turns[i-1]
		n += len([]rune(t.User)) + len([]rune(t.Bot))
		if n > chars && i < len(c.Turns) {
			break
		}
		i--
	}
	return append([]ChatTurn(nil), c.Turns[i:]...)
}

// Reset — начать новый чат.
func (c *ChatThread) Reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ID, c.Since, c.Turns = newID(), time.Now().Unix(), nil
	c.saveLocked()
}

func (c *ChatThread) State() map[string]any {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return map[string]any{"turns": len(c.Turns), "since": c.Since}
}

// chatLines — реплики одной строкой на каждую: «Человек: …», «Джарвис: …».
func chatLines(turns []ChatTurn) []string {
	var lines []string
	for _, t := range turns {
		if t.User != "" {
			lines = append(lines, "Человек: "+t.User)
		}
		if t.Bot != "" {
			lines = append(lines, "Джарвис: "+t.Bot)
		}
	}
	return lines
}

// Лимит голоса в подписке ChatGPT («You have reached your usage limit»): когда он обновится, OpenAI не говорит,
// поэтому час не звоним в ChatGPT, а разговариваем через Gemini (если есть ключ). Потом пробуем снова.
var chatgptLimitedUntil atomic.Int64

const chatgptLimitRest = time.Hour

func chatgptLimited() bool { return time.Now().UnixNano() < chatgptLimitedUntil.Load() }

func chatgptLimitText(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "usage limit") || strings.Contains(m, "rate limit") || strings.Contains(m, "quota")
}

// AddFact — записать в чат, что Джарвис сделал сам (включил видео, выполнил команду): ChatGPT узнает это в следующем звонке.
func (c *ChatThread) AddFact(fact string) {
	if fact = strings.TrimSpace(fact); fact != "" {
		c.Add("", "["+fact+"]")
	}
}
