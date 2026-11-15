package main

// Следующий трек — заранее. Пока играет текущий, узнаём ссылку на следующий (очередь, похожее или
// следующий результат поиска). YouTube не отдаёт звук, пока «идёт» реклама перед видео (ytStream.ready),
// и ещё секунду-две уходит на запрос /player и расшифровку ссылки — всё это проходит в фоне,
// и «дальше» включается сразу.

import (
	"context"
	"log"
	"time"
)

const prefetchTTL = time.Hour // ссылки YouTube живут ~6 ч; час — с запасом

type preStream struct {
	id   string
	st   *ytStream
	info Track
	at   time.Time
}

// takePrefetched — готовая ссылка на трек id, если она есть и не устарела (забирается один раз).
func (m *Music) takePrefetched(id string) (*ytStream, Track, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.pre
	if p.st == nil || p.id != id || time.Since(p.at) > prefetchTTL {
		return nil, Track{}, false
	}
	m.pre = preStream{}
	return p.st, p.info, true
}

// prefetchSoon — подготовить следующий трек в фоне (повторный вызов, пока идёт подготовка, ничего не делает).
func (m *Music) prefetchSoon() {
	if m.playback != nil { // в тестах звук ненастоящий — и сеть не трогаем
		return
	}
	m.mu.Lock()
	if m.prefetching {
		m.mu.Unlock()
		return
	}
	m.prefetching = true
	m.mu.Unlock()
	go func() {
		defer func() { m.mu.Lock(); m.prefetching = false; m.mu.Unlock() }()
		// пусть сначала разгонится текущий трек (и Play успеет запомнить остальные результаты поиска)
		time.Sleep(4 * time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		m.prefetch(ctx)
	}()
}

func (m *Music) prefetch(ctx context.Context) {
	m.mu.Lock()
	seed := m.cur.ID
	empty := m.radioOn() && len(m.queue) == 0 && seed != ""
	needPersonal := empty && m.personal && len(m.results) == 0 && m.Account != nil
	needMix := empty && len(m.mix) == 0 && (!m.personal || (len(m.results) == 0 && m.Account == nil))
	m.mu.Unlock()
	if needPersonal && !m.loadPersonalNext(ctx, seed) {
		needMix = true
	}
	if needMix { // похожее понадобится — загрузим его сейчас, а не по «дальше»
		m.loadMix(ctx, seed)
	}
	m.mu.Lock()
	nx := m.upNext(1)
	have := m.pre.st != nil && len(nx) > 0 && m.pre.id == nx[0].ID && time.Since(m.pre.at) < prefetchTTL/2
	m.mu.Unlock()
	if len(nx) == 0 || have {
		return
	}
	t0 := time.Now()
	st, info, err := m.audioURL(ctx, nx[0].ID)
	if err != nil {
		log.Printf("музыка: заранее подготовить «%s» не вышло: %v", nx[0].Title, err)
		return
	}
	m.mu.Lock()
	m.pre = preStream{id: nx[0].ID, st: st, info: info, at: time.Now()}
	m.mu.Unlock()
	log.Printf("музыка: следующий трек «%s» готов заранее (%.1f с)", nx[0].Title, time.Since(t0).Seconds())
}

// loadPersonalNext — рекомендации YouTube под «личным» видео (из твоей истории): что включится дальше или будет
// предложено в конце. Подбираем, пока играет текущее, — в конце ролика предложение звучит сразу.
func (m *Music) loadPersonalNext(ctx context.Context, seed string) bool {
	tracks, err := m.watchRecommendations(ctx, seed)
	if err != nil {
		log.Printf("музыка: рекомендации под текущим видео: %v — беру похожее", err)
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cur.ID != seed || !m.personal {
		return true // уже играет другое — подберётся заново
	}
	played := map[string]bool{seed: true}
	for _, t := range m.history {
		played[t.ID] = true
	}
	var next []Track
	for _, t := range tracks {
		if !played[t.ID] && len(next) < 20 {
			played[t.ID] = true
			next = append(next, t)
		}
	}
	if len(next) == 0 {
		return false
	}
	m.results, m.nextSeed = next, seed
	return true
}
