package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

type savedPlayback struct {
	Track    Track     `json:"track"`
	Pos      float64   `json:"pos"`
	Dur      float64   `json:"dur"`
	Updated  time.Time `json:"updated"`
	Complete bool      `json:"complete"`
}
type playbackHistory struct {
	mu    sync.Mutex
	path  string
	items map[string]savedPlayback
}

func openPlaybackHistory(path string) *playbackHistory {
	p := &playbackHistory{path: path, items: map[string]savedPlayback{}}
	b, err := os.ReadFile(path)
	if err == nil {
		if err = json.Unmarshal(b, &p.items); err != nil {
			log.Printf("YouTube: история позиций повреждена: %v", err)
		}
	}
	if p.items == nil {
		p.items = map[string]savedPlayback{}
	}
	return p
}

func (m *Music) saveProgress(complete bool) {
	if m.progress == nil {
		return
	}
	m.mu.Lock()
	v := savedPlayback{Track: m.cur, Pos: m.pos, Updated: time.Now(), Complete: complete}
	if m.st != nil {
		v.Dur = m.st.dur
	}
	active := m.cancel != nil || m.paused
	m.mu.Unlock()
	if v.Track.ID == "" || (!active && !complete) {
		return
	}
	p := m.progress
	p.mu.Lock()
	defer p.mu.Unlock()
	if old, ok := p.items[v.Track.ID]; ok && !complete && old.Complete && v.Dur > 0 && v.Pos >= v.Dur-20 {
		return
	}
	p.items[v.Track.ID] = v
	if len(p.items) > 100 {
		var oldest string
		var at time.Time
		for id, x := range p.items {
			if oldest == "" || x.Updated.Before(at) {
				oldest, at = id, x.Updated
			}
		}
		delete(p.items, oldest)
	}
	b, err := json.Marshal(p.items)
	if err == nil {
		err = os.WriteFile(p.path+".tmp", b, 0600)
	}
	if err == nil {
		err = os.Rename(p.path+".tmp", p.path)
	}
	if err != nil {
		log.Printf("YouTube: сохранить позицию: %v", err)
	}
}

func (m *Music) watchProgress(ctx context.Context) {
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			m.saveProgress(false)
			return
		case <-tick.C:
			if m.Playing() {
				m.saveProgress(false)
			}
		}
	}
}

func savedResumeQuery(text string) (string, bool) {
	t := strings.TrimSpace(leadRe.ReplaceAllString(normText(text), ""))
	for _, word := range []string{"уборк", "пылесос", "разговор", "ответ", "таймер", "сценари", "свет"} {
		if strings.Contains(t, word) {
			return "", false
		}
	}
	for _, prefix := range []string{"продолжи", "продолжить", "дослушать", "дослушай", "возобнови"} {
		if t == prefix {
			return "", true
		}
		if strings.HasPrefix(t, prefix+" ") {
			return strings.TrimSpace(strings.TrimPrefix(t, prefix)), true
		}
	}
	return "", false
}

func (p *playbackHistory) find(query string) (savedPlayback, error) {
	if p == nil {
		return savedPlayback{}, errors.New("сохранённых позиций пока нет")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var found []savedPlayback
	q := normText(query)
	for _, w := range []string{"вчерашний", "вчерашнюю", "вчерашнее", "вчера", "позавчера", "последний", "последнее", "последнюю", "тот", "ту", "это", "ролик", "видео", "с ютуба", "на ютубе", "youtube", "сохраненный", "сохраненное", "с того места"} {
		q = strings.ReplaceAll(" "+q+" ", " "+w+" ", " ")
		q = strings.TrimSpace(q)
	}
	for _, v := range p.items {
		if v.Complete || v.Pos < 5 || (v.Dur > 0 && v.Pos >= v.Dur-20) {
			continue
		}
		title := normText(v.Track.Title + " " + v.Track.Author)
		match := true
		for _, word := range strings.Fields(q) {
			if !strings.Contains(title, word) {
				match = false
				break
			}
		}
		if match {
			found = append(found, v)
		}
	}
	if len(found) == 0 {
		return savedPlayback{}, errors.New("не нашёл недослушанный ролик с сохранённой позицией по этой просьбе")
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Updated.After(found[j].Updated) })
	return found[0], nil
}

func (m *Music) resumeSaved(ctx context.Context, query string) string {
	m.requestMu.Lock()
	defer m.requestMu.Unlock()
	v, err := m.progress.find(query)
	if err != nil {
		return "Не получилось продолжить: " + err.Error() + "."
	}
	st, info, ok := m.takePrefetched(v.Track.ID)
	if !ok {
		st, info, err = m.audioURL(ctx, v.Track.ID)
	}
	if err != nil {
		return "Не получилось продолжить: " + shortErr(err)
	}
	if ctx.Err() != nil {
		return "Продолжение остановлено."
	}
	if v.Track.Title == "" {
		v.Track.Title, v.Track.Author = info.Title, info.Author
	}
	pos := max(0, v.Pos-3)
	if st.dur > 0 && pos >= st.dur-2 {
		return "Этот ролик уже дослушан."
	}
	m.launch(v.Track, st, pos)
	m.remember(v.Track)
	m.mu.Lock()
	m.results, m.mix, m.proposal = nil, nil, nil
	m.personal = true
	m.mu.Unlock()
	return fmt.Sprintf("Продолжаю «%s» с %s.", v.Track.Title, clock(pos))
}

func (e *Engine) resumeSavedRequest(ctx context.Context, text string) (string, bool) {
	q, ok := savedResumeQuery(text)
	if !ok || e.music == nil {
		return "", false
	}
	if q == "" && e.music.Active() {
		return "", false
	}
	return e.music.resumeSaved(ctx, q), true
}
