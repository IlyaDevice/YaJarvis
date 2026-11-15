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
	needMix := !m.personal && m.radioOn() && len(m.queue) == 0 && len(m.mix) == 0 && m.cur.ID != ""
	seed := m.cur.ID
	m.mu.Unlock()
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
