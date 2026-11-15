package main

import (
	"context"
	"sort"
	"strings"
	"sync/atomic"
	"time"
)

const speakerContentRules = "\n\nПостоянное правило колонки: когда сам подбираешь видео, выбирай то, что полноценно понятно БЕЗ экрана. " +
	"Подходят стендап, апвоут, рассказы и аудиокниги, подкасты, интервью, беседы, разговорные истории и лекции. " +
	"Не подбирай геймплей, демонстрации графики, распаковки, визуальные обзоры, клипы, челленджи или ролики, смысл которых надо смотреть. " +
	"Для личного подбора вызывай play_music с запросом «включи что-нибудь интересное из моих рекомендаций», сохраняя пожелания хозяина о жанре. " +
	"Если в ленте такого нет, используй поиск с его входом. Не выдумывай, что видео взято из рекомендаций, если оно найдено поиском. " +
	"Если хозяин просит подобрать самому, сначала вызови подбор и предложи ОДИН вариант: кратко о чём он, затем «Послушаем?». До явного «да» НЕ включай видео. " +
	"«Нет», «дальше», «хуйня» означают предложить другой вариант. Для ответа используй music_control с командой «да» или «дальше». " +
	"Когда ролик уже выбран и хозяин просит следующий, вызывай music_control с командой «дальше»: подбор должен идти из рекомендаций YouTube ПОД ТЕКУЩИМ роликом, с его входом, с приоритетом пригодности для прослушивания. Не начинай заново подбор с главной и не ищи буквальные слова «следующий ролик». " +
	"Никогда не превращай слова «рекомендации YouTube», «рекомендованные» или «подбери сам» в буквальное название видео и не добавляй их в очередь. " +
	"Для «продолжи вчерашний ролик/апвоут/стендап» вызывай music_control с исходной просьбой: позиция сохранена на колонке, повторный поиск не нужен. " +
	"Если хозяин прямо назвал конкретную песню или ролик и попросил включить, выполни его точную просьбу."

const speakerWaitRules = "\n\nКогда реально думаешь или выполняешь поиск, не оставляй хозяина в тишине. " +
	"Перед долгим действием скажи одну короткую разговорную реплику, например «Братан, сейчас гляну, подожди», «Так, поищу тебе что-нибудь послушать», «Секунду, проверю». " +
	"Такая реплика — одна на весь ответ, даже если инструментов несколько: вторую и третью «секунду, проверю» не говори. " +
	"Не выдавай внутренние рассуждения, не выдумывай результат и не обещай успех заранее. Как только результат готов, сообщи его; при остановке сразу замолчи."

func listenScore(t Track) int {
	n := normText(t.Title + " " + t.Author)
	for _, s := range []string{"геймплей", "gameplay", "minecraft", "майнкрафт", "прохождение", "распаков", "видеоклип", "official video", "music video", "трейлер", "parkour", "паркyр", "rtx", "сравнение графики", "челлендж", "голодные игры в реальной"} {
		if strings.Contains(n, s) {
			return -1
		}
	}
	for _, s := range []string{"апвоут", "upvote", "стендап", "standup", "stand up", "комиссаренко", "алексей щербаков", "нурлан сабуров", "иван абрамов"} {
		if strings.Contains(n, s) {
			return 100
		}
	}
	for _, s := range []string{"подкаст", "podcast", "интервью", "аудиокниг", "аудиорассказ", "рассказ", "бесед", "разговор", "лекци", "истории", "история", "сегодня я узнал", "исторические факты"} {
		if strings.Contains(n, s) {
			return 70
		}
	}
	return 0
}

func listeningTracks(tracks []Track) []Track {
	out := make([]Track, 0, len(tracks))
	for _, t := range tracks {
		if listenScore(t) > 0 {
			out = append(out, t)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return listenScore(out[i]) > listenScore(out[j]) })
	return out
}

var waitLineCounter atomic.Uint32

func nextWaitLine() string {
	return nextWaitLineFor("search")
}

func nextWaitLineFor(action string) string {
	lines := []string{"Братан, сейчас гляну, подожди.", "Секунду, поищу этот ролик на YouTube.", "Сейчас проверю, что нашлось по твоей просьбе."}
	switch action {
	case "related":
		lines = []string{"Сейчас гляну, что YouTube предлагает под этим роликом.", "Секунду, выберу из похожих то, что удобно слушать.", "Братан, проверю рекомендации под текущим видео."}
	case "recommendations":
		lines = []string{"Сейчас посмотрю твою ленту и подберу что-нибудь послушать.", "Братан, гляну твои рекомендации.", "Секунду, поищу в твоей ленте разговорный ролик."}
	case "resume":
		lines = []string{"Сейчас найду, где ты остановился.", "Секунду, проверю сохранённую позицию.", "Братан, посмотрю, что осталось недослушанным."}
	case "home":
		lines = []string{"Сейчас проверю пылесос и передам команду.", "Секунду, обращусь к Home Assistant.", "Братан, проверю подключение пылесоса."}
	}
	return lines[(waitLineCounter.Add(1)-1)%uint32(len(lines))]
}

func (e *Engine) mediaWithChatter(ctx context.Context, work func() string) string {
	return e.mediaWithChatterFor(ctx, "search", work)
}

func (e *Engine) mediaWithChatterFor(ctx context.Context, action string, work func() string) string {
	result := make(chan string, 1)
	go func() { result <- work() }()
	said := make(chan struct{})
	go func() { defer close(said); e.sayMusicStatusContext(ctx, nextWaitLineFor(action)) }()
	var timer *time.Timer
	var tick <-chan time.Time
	defer func() {
		if timer != nil {
			timer.Stop()
		}
	}()
	for {
		select {
		case answer := <-result:
			if said != nil {
				select {
				case <-said:
				case <-ctx.Done():
				}
			}
			return answer
		case <-said:
			said = nil
			timer = time.NewTimer(8 * time.Second)
			tick = timer.C
		case <-tick:
			tick = nil
			line := "Ещё проверяю, братан. Скоро скажу, что получилось."
			if action == "related" || action == "recommendations" {
				line = "Ещё подбираю, братан. Хочу найти то, что нормально слушается без экрана."
			}
			if action == "resume" {
				line = "Позицию проверяю и готовлю продолжение, подожди ещё немного."
			}
			e.sayMusicStatusContext(ctx, line)
		case <-ctx.Done():
			return "Не получилось: поиск остановлен."
		}
	}
}
