package main

// Очередь и «радио»: «добавь в очередь …», после трека — следующий из очереди, а когда она пуста —
// похожее (микс YouTube «RD<id>» по последнему треку, без уже сыгранного).

import (
	"context"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"
)

const maxQueue = 50

// Enqueue — найти и поставить в очередь (ссылку — как есть, название — первый результат поиска).
func (m *Music) Enqueue(ctx context.Context, query string) (Track, error) {
	if recommendationRequest(query) {
		return Track{}, errors.New("просьбу подобрать ролик нельзя добавлять в очередь как поисковый запрос")
	}
	var t Track
	if id := youtubeID(query); id != "" {
		t = Track{ID: id}
		if _, info, err := m.audioURL(ctx, id); err == nil {
			t.Title, t.Author = info.Title, info.Author
		}
	} else {
		tracks, err := m.search(ctx, query)
		if err != nil {
			return Track{}, err
		}
		if len(tracks) == 0 {
			return Track{}, fmt.Errorf("на YouTube ничего не нашлось по запросу «%s»", query)
		}
		t = tracks[0]
	}
	m.mu.Lock()
	idle := m.cancel == nil && !m.paused
	if !idle {
		if len(m.queue) >= maxQueue {
			m.mu.Unlock()
			return t, errors.New("очередь полная")
		}
		m.queue = append(m.queue, t)
	}
	m.mu.Unlock()
	if idle { // ничего не играет — «в очередь» значит «включи»
		m.requestMu.Lock()
		defer m.requestMu.Unlock()
		return m.start(ctx, t)
	}
	log.Printf("музыка: в очередь «%s»", t.Title)
	m.prefetchSoon() // если этот трек теперь следующий — подготовим его
	return t, nil
}

// Unqueue — убрать из очереди i-й трек (-1 — всю очередь).
func (m *Music) Unqueue(i int) {
	defer m.prefetchSoon() // следующий мог смениться
	m.mu.Lock()
	defer m.mu.Unlock()
	if i < 0 {
		m.queue = nil
	} else if i < len(m.queue) {
		m.queue = append(m.queue[:i:i], m.queue[i+1:]...)
	}
}

func (m *Music) radioOn() bool { return m.Radio == nil || m.Radio() }

// upNext — что заиграет следующим: очередь, потом похожее (если радио включено), потом остальные результаты поиска.
func (m *Music) upNext(n int) []Track {
	out := append([]Track{}, m.queue...)
	if m.personal {
		out = append(out, m.results...)
		return out[:min(n, len(out))]
	}
	if m.radioOn() {
		out = append(out, m.mix...)
	}
	out = append(out, m.results...)
	return out[:min(n, len(out))]
}

// popNext — снять следующий трек; похожее подгружается, когда очередь пуста.
func (m *Music) popNext(ctx context.Context) (Track, bool) {
	m.mu.Lock()
	if len(m.queue) > 0 {
		t := m.queue[0]
		m.queue = m.queue[1:]
		m.mu.Unlock()
		return t, true
	}
	if m.personal {
		if len(m.results) > 0 {
			t := m.results[0]
			m.results = m.results[1:]
			m.mu.Unlock()
			return t, true
		}
		m.mu.Unlock()
		return Track{}, false
	}
	radio := m.radioOn()
	if radio && len(m.mix) == 0 {
		seed := m.cur.ID
		m.mu.Unlock()
		m.loadMix(ctx, seed)
		m.mu.Lock()
	}
	if radio && len(m.mix) > 0 {
		t := m.mix[0]
		m.mix = m.mix[1:]
		m.mu.Unlock()
		return t, true
	}
	if len(m.results) > 0 {
		t := m.results[0]
		m.results = m.results[1:]
		m.mu.Unlock()
		return t, true
	}
	m.mu.Unlock()
	return Track{}, false
}

// loadMix — похожее на трек id: микс YouTube, без того, что уже звучало.
func (m *Music) loadMix(ctx context.Context, id string) {
	if id == "" {
		return
	}
	tracks, err := m.related(ctx, id)
	if err != nil {
		log.Printf("музыка: похожее на %s: %v", id, err)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	played := map[string]bool{m.cur.ID: true}
	for _, t := range m.history {
		played[t.ID] = true
	}
	for _, t := range m.queue {
		played[t.ID] = true
	}
	m.mix = m.mix[:0]
	for _, t := range tracks {
		if !played[t.ID] && len(m.mix) < 20 {
			played[t.ID] = true
			m.mix = append(m.mix, t)
		}
	}
}

func (m *Music) related(ctx context.Context, id string) ([]Track, error) {
	body := map[string]any{"context": map[string]any{"client": map[string]any{"clientName": "WEB", "clientVersion": "2.20261112.01.00", "hl": "ru", "gl": "RU"}},
		"videoId": id, "playlistId": "RD" + id}
	out, err := m.post(ctx, "https://www.youtube.com/youtubei/v1/next?prettyPrint=false", body,
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/132.0 Safari/537.36")
	if err != nil {
		return nil, err
	}
	return mixTracks(out), nil
}

// mixTracks — треки плейлиста-микса из ответа /next.
func mixTracks(out map[string]any) []Track {
	var tracks []Track
	seen := map[string]bool{}
	walk(out, func(key string, obj map[string]any) {
		if key != "playlistPanelVideoRenderer" {
			return
		}
		t := Track{ID: str(obj["videoId"]), Title: youtubeText(obj["title"]), Author: youtubeText(obj["shortBylineText"])}
		if t.ID != "" && t.Title != "" && !seen[t.ID] {
			seen[t.ID] = true
			tracks = append(tracks, t)
		}
	})
	return tracks
}

// ended — трек доиграл сам: включаем следующий. Обрыв в середине — не повод перескакивать.
func (m *Music) ended(gen int, pos, dur float64) {
	if dur > 0 && pos < dur-20 {
		log.Printf("музыка: оборвалось на %s из %s — дальше не переключаю", clock(pos), clock(dur))
		return
	}
	m.mu.Lock()
	current := m.gen == gen
	m.mu.Unlock()
	if current {
		m.saveProgress(true)
	}
	m.requestMu.Lock()
	defer m.requestMu.Unlock()
	m.mu.Lock()
	still := m.gen == gen && m.cancel == nil && !m.paused
	if still && m.personal && len(m.queue) == 0 && m.radioOn() && m.OnRelatedEnd != nil {
		seed, offer := m.cur.ID, m.OnRelatedEnd
		m.paused = true
		m.mu.Unlock()
		go offer(seed)
		return
	}
	m.mu.Unlock()
	if !still {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	m.mu.Lock()
	m.loadingCancel = cancel
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.loadingCancel = nil; m.mu.Unlock() }()
	for tries := 0; tries < 3; tries++ {
		t, ok := m.popNext(ctx)
		if !ok {
			m.mu.Lock()
			m.st, m.cur = nil, Track{}
			m.mu.Unlock()
			log.Printf("музыка: всё доиграло")
			return
		}
		m.mu.Lock()
		still = m.gen == gen && m.cancel == nil
		m.mu.Unlock()
		if !still || ctx.Err() != nil {
			return
		}
		if _, err := m.start(ctx, t); err == nil {
			return
		} else {
			log.Printf("музыка: следующее «%s»: %v", t.Title, err)
		}
	}
}

// queueRequest — «добавь в очередь Кино», «поставь в очередь …», «… следующим».
func queueRequest(text string) (string, bool) {
	t := strings.ToLower(strings.TrimSpace(strings.Trim(text, " ,.!?")))
	t = strings.ReplaceAll(t, "ё", "е")
	m := queueRe.FindStringSubmatch(t)
	if m == nil {
		return "", false
	}
	q := strings.TrimSpace(m[1] + " " + m[3])
	q = youtubeRe.ReplaceAllString(q, " ")
	for _, w := range []string{"песню", "песня", "трек", "видео", "пожалуйста"} {
		q = strings.ReplaceAll(" "+q+" ", " "+w+" ", " ")
	}
	q = strings.Trim(strings.Join(strings.Fields(q), " "), " ,.!?")
	return q, q != ""
}

var queueRe = regexp.MustCompile(`^(?:джарвис\S*\s+)?(?:добавь|поставь|закинь|кинь|включи)\s+(?:в очередь|следующ\S*|после этого|потом)\s*(.*?)()$|^(?:джарвис\S*\s+)?(?:добавь|закинь|кинь)\s+(.+?)\s+в очередь()$`)
