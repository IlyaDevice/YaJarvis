package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type mediaProposal struct {
	items         []Track
	query, source string
	expires       time.Time
}

// proposalReply — ответ на «Послушаем?»: accept, reject (следующий), cancel (ничего не надо) или "" — не ответ.
// Понимает живую речь: «погнали», «мне не нравится», «нет, херня», «давай другое», «да ну нафиг».
func proposalReply(text string) string {
	t := leadRe.ReplaceAllString(normText(text), "")
	words := strings.Fields(t)
	for len(words) > 0 && proposalFiller[words[0]] {
		words = words[1:]
	}
	if len(words) == 0 || len(words) > 8 { // длинная фраза — это уже не ответ «да/нет», пусть разбирается модель
		return ""
	}
	t = " " + strings.Join(words, " ") + " "
	has := func(list []string) bool {
		for _, p := range list {
			if strings.Contains(t, p) {
				return true
			}
		}
		return false
	}
	word := func(list ...string) bool {
		for _, w := range words {
			for _, x := range list {
				if w == x {
					return true
				}
			}
		}
		return false
	}
	switch {
	case has(proposalCancel):
		return "cancel"
	case has(proposalAcceptNeg): // «не против», «почему бы и нет» — согласие, хоть и с «не»
		return "accept"
	case has(proposalReject) || words[0] == "нет" || words[0] == "неа" || words[0] == "не":
		return "reject"
	case word("не"): // «я не говорил да» — не согласие
		return ""
	case has(proposalAccept):
		return "accept"
	}
	return ""
}

var proposalFiller = map[string]bool{"ну": true, "так": true, "ой": true, "слушай": true, "братан": true, "бро": true, "брат": true,
	"братишка": true, "джарвис": true, "блин": true, "короче": true, "это": true, "а": true, "мм": true, "хм": true}

var proposalCancel = []string{" отмена ", " отбой ", " ничего не надо", " не надо ничего", " ничего не включай", " не включай ", " не включай",
	" передумал", " забей ", " забей", " проехали", " не сейчас", " потом "}

var proposalAcceptNeg = []string{" не против", " почему бы и нет", " почему нет", " не вопрос", " а почему бы нет"}

var proposalReject = []string{" не нрав", " нрав не", " хуйн", " херн", " фигн", " хрен", " дерьм", " говн", " отстой", " скучн", " дальше",
	" следующ", " друг", " не то ", " не то", " не это", " не подход", " не интерес", " неинтерес", " не хочу", " пропус", " мимо", " листай",
	" не зашло", " фу ", " нахуй", " нафиг", " на хер", " нахер", " не катит", " не годится", " не пойдёт", " не пойдет", " некайф", " не кайф",
	" херово", " хуево", " хуёво", " отврат", " ерунд", " чушь", " бред", " ещё вариант", " еще вариант", " покажи ещё", " покажи еще"}

var proposalAccept = []string{" да ", " ага ", " угу ", " ок ", " окей ", " го ", " давай", " погнали", " поехали", " включай", " включи ", " запускай",
	" запусти", " врубай", " вруби ", " норм", " хорошо", " годится", " пойдёт", " пойдет", " идёт ", " отлично", " супер", " круто", " согласен",
	" буду ", " хочу ", " интересно", " заебись", " зашибись", " кайф", " топ ", " ладно", " валяй", " конечно", " можно", " звучит", " ставь",
	" врубай", " жги", " погнали"}

func (m *Music) hasProposal() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.proposal != nil && time.Now().After(m.proposal.expires) {
		m.proposal = nil
	}
	return m.proposal != nil && len(m.proposal.items) > 0
}

func describeProposal(t Track, source string) string {
	from := "В твоей ленте"
	if source == "search" {
		from = "В поиске YouTube с твоим входом"
	}
	if source == "related" {
		from = "В рекомендациях YouTube под текущим роликом"
	}
	channel := ""
	if t.Author != "" {
		channel = " Канал «" + t.Author + "»."
	}
	return fmt.Sprintf("%s нашёл разговорный ролик «%s».%s Послушаем? Скажи «да» или «дальше». Пока не включаю.", from, t.Title, channel)
}

func (m *Music) propose(ctx context.Context, query string) string {
	m.requestMu.Lock()
	defer m.requestMu.Unlock()
	m.Pause()
	m.mu.Lock()
	m.results, m.mix = nil, nil
	m.personal = true
	m.mu.Unlock()
	tracks, err := m.recommendations(ctx)
	source := "recommendations"
	if err != nil {
		return "Не получилось подобрать: " + shortErr(err)
	}
	tracks = recommendationFilter(query, tracks)
	m.mu.Lock()
	played := map[string]bool{m.cur.ID: true}
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
		tracks, err = m.accountSearch(ctx, query)
		source = "search"
		if err != nil {
			return "Не получилось подобрать: " + shortErr(err)
		}
	}
	if len(tracks) == 0 {
		return "Не получилось подобрать подходящий разговорный ролик. Уточни тему, братан."
	}
	if ctx.Err() != nil {
		return "Подбор остановлен."
	}
	m.mu.Lock()
	m.lastQuery = query
	m.proposal = &mediaProposal{items: append([]Track(nil), tracks...), query: query, source: source, expires: time.Now().Add(3 * time.Minute)}
	m.mu.Unlock()
	return describeProposal(tracks[0], source)
}

func (m *Music) respondProposal(ctx context.Context, text string) (string, bool) {
	if !m.hasProposal() {
		return "", false
	}
	action := proposalReply(text)
	if action == "" {
		return "", false
	}
	m.requestMu.Lock()
	defer m.requestMu.Unlock()
	m.mu.Lock()
	p := m.proposal
	if p == nil || len(p.items) == 0 {
		m.mu.Unlock()
		return "Вариант уже не выбран. Попроси подобрать снова.", true
	}
	if action == "cancel" {
		m.proposal = nil
		m.mu.Unlock()
		return "Ладно, не включаю.", true
	}
	if action == "reject" {
		p.items = p.items[1:]
		p.expires = time.Now().Add(3 * time.Minute)
		if len(p.items) == 0 {
			m.proposal = nil
			m.mu.Unlock()
			return "Эти варианты закончились. Назови другую тему, подберу ещё.", true
		}
		t, source := p.items[0], p.source
		m.mu.Unlock()
		return describeProposal(t, source), true
	}
	t := p.items[0]
	source := p.source
	m.proposal = nil
	m.mu.Unlock()
	tr, err := m.start(ctx, t)
	if err != nil {
		return "Не получилось включить выбранный ролик: " + shortErr(err) + ". Можешь попросить подобрать другой.", true
	}
	m.mu.Lock()
	m.results, m.mix, m.personal, m.personalSearch = nil, nil, true, source == "search"
	m.mu.Unlock()
	return "Включаю: " + tr.Title + ".", true
}

// Continue listening on the existing microphone feed while a proposal awaits a reply.
func (e *Engine) speakSelection(ctx context.Context, answer string) {
	if e.stt != nil {
		e.stt.End()
	}
	e.sayMusicStatusContext(ctx, answer)
	e.awaitProposal(ctx)
}

func (e *Engine) awaitProposal(ctx context.Context) {
	if e.stt == nil || !e.stt.Ready() {
		return
	}
	for e.music.hasProposal() && ctx.Err() == nil {
		e.stt.End()
		ch, err := e.stt.Begin()
		if err != nil {
			return
		}
		timer := time.NewTimer(45 * time.Second)
		answered := false
		for !answered {
			select {
			case text := <-ch:
				answer, ok := e.music.respondProposal(ctx, text)
				if !ok {
					continue
				}
				e.journal.note("выбор ролика", text, answer)
				e.stt.End()
				e.sayMusicStatusContext(ctx, answer)
				answered = true
			case <-timer.C:
				timer.Stop()
				return
			case <-ctx.Done():
				timer.Stop()
				return
			}
		}
		timer.Stop()
	}
}
