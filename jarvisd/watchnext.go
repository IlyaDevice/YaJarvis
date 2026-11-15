package main

import (
	"context"
	"errors"
	"strings"
	"time"
)

// Ordinary watch-page recommendations, not YouTube Music's RD playlist.
func (m *Music) watchRecommendations(ctx context.Context, id string) ([]Track, error) {
	if id == "" {
		return nil, errors.New("нет текущего ролика для рекомендаций")
	}
	if m.Account == nil {
		return nil, errors.New("для рекомендаций с твоим входом войди в YouTube")
	}
	tok, err := m.Account.Token(ctx)
	if err != nil {
		return nil, err
	}
	if tok == "" {
		return nil, errors.New("нужно заново войти в YouTube")
	}
	const version = "7.20260114.12.00"
	body := map[string]any{"context": map[string]any{"client": map[string]any{"clientName": "TVHTML5", "clientVersion": version, "hl": "ru", "gl": "RU", "platform": "TV", "deviceMake": "Samsung", "deviceModel": "SmartTV"}}, "videoId": id}
	api := &Music{client: m.Account.client}
	out, err := api.post(ctx, "https://www.youtube.com/youtubei/v1/next?prettyPrint=false", body, ytTVAgent, "Authorization", "Bearer "+tok, "X-YouTube-Client-Name", "7", "X-YouTube-Client-Version", version)
	if err != nil {
		return nil, err
	}
	tracks := listeningTracks(recommendationTracks(out["contents"]))
	return tracks, nil
}

func (m *Music) proposeRelated(ctx context.Context) string {
	return m.proposeRelatedFor(ctx, "")
}

func (m *Music) proposeRelatedFor(ctx context.Context, expected string) string {
	m.requestMu.Lock()
	defer m.requestMu.Unlock()
	m.mu.Lock()
	seed := m.cur.ID
	query := m.lastQuery
	stale := expected != "" && (seed != expected || !m.paused)
	m.mu.Unlock()
	if stale {
		return ""
	}
	if seed == "" {
		return "Сначала выбери ролик, братан: пока не к чему искать похожее."
	}
	m.mu.Lock()
	var ready []Track
	if m.nextSeed == seed && len(m.results) > 0 {
		ready = append([]Track(nil), m.results...)
	}
	m.mu.Unlock()
	m.Pause()
	ctx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.proposal = nil
	m.results, m.mix = nil, nil
	m.personal = true
	m.loadingCancel = cancel
	m.mu.Unlock()
	defer func() { cancel(); m.mu.Lock(); m.loadingCancel = nil; m.mu.Unlock() }()
	tracks, err := ready, error(nil)
	if len(tracks) == 0 {
		tracks, err = m.watchRecommendations(ctx, seed)
	}
	if err != nil {
		return "Не получилось получить рекомендации под текущим роликом: " + shortErr(err)
	}
	m.mu.Lock()
	played := map[string]bool{seed: true}
	for _, t := range m.history {
		played[t.ID] = true
	}
	m.mu.Unlock()
	fresh := tracks[:0]
	for _, t := range tracks {
		if !played[t.ID] {
			fresh = append(fresh, t)
		}
	}
	tracks = fresh
	if len(tracks) == 0 {
		return "Под этим роликом YouTube пока не предложил подходящего нового видео для прослушивания. Могу подобрать другую тему."
	}
	if ctx.Err() != nil {
		return "Подбор остановлен."
	}
	m.mu.Lock()
	m.results, m.mix = nil, nil
	m.personal = true
	m.proposal = &mediaProposal{items: append([]Track(nil), tracks...), query: query, source: "related", expires: time.Now().Add(3 * time.Minute)}
	m.mu.Unlock()
	return describeProposal(tracks[0], "related")
}

func relatedProbeArg(arg string) string { return strings.TrimPrefix(arg, "related:") }

// A finished selection may offer a continuation only when no conversation is active.
func (e *Engine) offerNextAfterEnd(seed string) {
	e.mu.Lock()
	if e.busy || e.live != nil {
		e.mu.Unlock()
		return
	}
	e.busy, e.lastWake = true, time.Now()
	e.mu.Unlock()
	defer func() {
		if e.stt != nil {
			e.stt.End()
		}
		e.mu.Lock()
		queued := e.pendingWake
		e.pendingWake = false
		e.busy, e.aborted = false, false
		e.mu.Unlock()
		if e.wake != nil {
			e.wake.Reset()
		}
		if e.leds != nil {
			e.leds.Set(LedOff)
		}
		if queued {
			e.Trigger("слово")
		}
	}()
	ctx, finish := e.beginMedia()
	defer finish()
	answer := e.mediaWithChatterFor(ctx, "related", func() string { return e.music.proposeRelatedFor(ctx, seed) })
	if answer == "" || ctx.Err() != nil {
		return
	}
	if e.journal != nil {
		e.journal.note("следующий ролик", "ролик закончился", answer)
	}
	e.speakSelection(ctx, answer)
}
