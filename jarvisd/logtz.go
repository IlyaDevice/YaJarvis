package main

// Время в журнале — по часовому поясу хозяина: часы колонки идут по Гринвичу.

import (
	"io"
	"sync"
	"sync/atomic"
	"time"
)

type tzLog struct {
	loc atomic.Pointer[time.Location]
	mu  sync.Mutex
	out io.Writer
	buf []byte
}

var logTZ = &tzLog{}

func (l *tzLog) set(tz string) {
	if loc, err := time.LoadLocation(tz); err == nil && tz != "" {
		l.loc.Store(loc)
	}
}

// Write — log.SetFlags(0): дату и время ставим сами, в поясе из настроек.
func (l *tzLog) Write(p []byte) (int, error) {
	now := time.Now()
	if loc := l.loc.Load(); loc != nil {
		now = now.In(loc)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = now.AppendFormat(l.buf[:0], "2006/01/02 15:04:05 ")
	l.buf = append(l.buf, p...)
	if _, err := l.out.Write(l.buf); err != nil {
		return 0, err
	}
	return len(p), nil
}
