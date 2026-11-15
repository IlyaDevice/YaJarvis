package main

// Журнал последних реплик (в памяти) + короткая история диалога для контекста следующего разговора.

import (
	"sync"
	"time"
)

type Journal struct {
	mu      sync.Mutex
	items   []*Turn
	next    int64
	history []Turn // последние реплики для контекста

	OnRemember func(Turn) // реплика закончилась (main: общий чат ChatGPT)
}

func (j *Journal) New(route, model string) *Turn {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.next++
	t := &Turn{ID: j.next, TS: float64(time.Now().UnixMilli()) / 1000, Route: route, Model: model}
	j.items = append([]*Turn{t}, j.items...)
	if len(j.items) > 100 {
		j.items = j.items[:100]
	}
	return t
}

func (j *Journal) Update(t *Turn, fn func(*Turn)) {
	if t == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	fn(t)
}

func (j *Journal) Remember(t *Turn) {
	j.mu.Lock()
	if t.Text == "" && t.Answer == "" {
		j.mu.Unlock()
		return
	}
	j.history = append(j.history, *t)
	if len(j.history) > 12 {
		j.history = j.history[len(j.history)-12:]
	}
	hook, done := j.OnRemember, *t
	j.mu.Unlock()
	if hook != nil {
		hook(done)
	}
}

// History — реплики за последние 30 минут.
func (j *Journal) History() []Turn {
	j.mu.Lock()
	defer j.mu.Unlock()
	var out []Turn
	for _, t := range j.history {
		if time.Since(time.UnixMilli(int64(t.TS*1000))) < 30*time.Minute {
			out = append(out, t)
		}
	}
	return out
}

func (j *Journal) Items(limit int) []Turn {
	j.mu.Lock()
	defer j.mu.Unlock()
	out := make([]Turn, 0, min(limit, len(j.items)))
	for i, t := range j.items {
		if i >= limit {
			break
		}
		out = append(out, *t)
	}
	return out
}
