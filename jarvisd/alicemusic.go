package main

// Что играет у Алисы (Яндекс Музыка, подкасты, книги). Входить в аккаунт не нужно: колонка сама пишет
// в свои логи id трека (maind.log: event=track_start/track_finished) и паузы с позицией (mediad.log),
// а название, исполнителя, обложку и длительность по id отдаёт открытый API Яндекс Музыки.
// Управление — беззвучными командами Алисе: «пауза», «продолжи», «следующий трек»…

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ymTrack struct {
	Title, Artist, Album, Cover string
	Dur                         float64
}

type AliceMusic struct {
	client  func() *http.Client
	OnStart func() // у Алисы зазвучал трек

	mu      sync.Mutex
	id      string
	info    ymTrack
	state   string // "" | playing | paused
	pos     float64
	posAt   time.Time
	pausedT time.Time
	cache   map[string]ymTrack
}

func NewAliceMusic(client func() *http.Client) *AliceMusic {
	return &AliceMusic{client: client, cache: map[string]ymTrack{}}
}

var (
	amPayloadRe = regexp.MustCompile(`event=(track_start|track_finished), payload=(\{.*\})\s*$`)
	amStreamRe  = regexp.MustCompile(`\[audio stream=(\d+)`)
	amPosRe     = regexp.MustCompile(`\] pause \(position (\d+)`)
	amNoteRe    = regexp.MustCompile(`Notification: (\w+)`)
)

// Run — следить за логами Алисы.
func (a *AliceMusic) Run(ctx context.Context) {
	// maind читает Engine.watchAlice: кнопка и запуск трека обрабатываются по порядку.
	tailLog(ctx, "/data/quasar/daemons_logs/mediad.log", a.onMedia)
}

func (a *AliceMusic) onMain(line string) {
	m := amPayloadRe.FindStringSubmatch(line)
	if m == nil {
		return
	}
	var p struct {
		ID     string `json:"id"`
		Reason string `json:"reason"`
	}
	if json.Unmarshal([]byte(m[2]), &p) != nil || p.ID == "" {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	switch m[1] {
	case "track_start":
		pos := 0.0
		if p.ID == a.id && a.state == "paused" && time.Since(a.pausedT) < 2*time.Hour {
			pos = a.pos // «продолжи» после паузы: Алиса запускает тот же трек с того же места
		}
		if p.ID != a.id {
			a.info = ymTrack{}
			go a.lookup(p.ID)
		}
		a.id, a.state, a.pos, a.posAt = p.ID, "playing", pos, time.Now()
		if a.OnStart != nil {
			go a.OnStart()
		}
	case "track_finished":
		if p.ID != a.id {
			return
		}
		if p.Reason == "cancel" { // «пауза» у Алисы — это остановка; помним место, чтобы показать паузу
			a.pos = a.curPos()
			a.state, a.pausedT = "paused", time.Now()
		} else {
			a.state = ""
		}
	}
}

func (a *AliceMusic) onMedia(line string) {
	s := amStreamRe.FindStringSubmatch(line)
	if s == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if s[1] != a.id {
		return
	}
	if p := amPosRe.FindStringSubmatch(line); p != nil {
		ms, _ := strconv.Atoi(p[1])
		a.pos, a.posAt, a.state, a.pausedT = float64(ms)/1000, time.Now(), "paused", time.Now()
		return
	}
	if n := amNoteRe.FindStringSubmatch(line); n != nil {
		switch n[1] {
		case "Start", "Resumed", "Playing":
			if a.state != "playing" {
				a.state, a.posAt = "playing", time.Now()
				if a.OnStart != nil {
					go a.OnStart()
				}
			}
		case "Paused":
			if a.state == "playing" {
				a.pos = a.curPos()
			}
			a.state, a.pausedT = "paused", time.Now()
		}
	}
}

func (a *AliceMusic) curPos() float64 {
	if a.state != "playing" {
		return a.pos
	}
	p := a.pos + time.Since(a.posAt).Seconds()
	if a.info.Dur > 0 {
		p = min(p, a.info.Dur)
	}
	return p
}

// lookup — название и обложка трека по id (открытый API Яндекс Музыки, без входа).
func (a *AliceMusic) lookup(id string) {
	a.mu.Lock()
	if t, ok := a.cache[id]; ok {
		if a.id == id {
			a.info = t
		}
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	t, err := ymLookup(a.client(), id)
	if err != nil {
		log.Printf("Яндекс Музыка: трек %s: %v", id, err)
		return
	}
	a.mu.Lock()
	if len(a.cache) > 200 {
		a.cache = map[string]ymTrack{}
	}
	a.cache[id] = t
	if a.id == id {
		a.info = t
	}
	a.mu.Unlock()
}

func ymLookup(client *http.Client, id string) (ymTrack, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.music.yandex.net/tracks/"+id, nil)
	resp, err := client.Do(req)
	if err != nil {
		return ymTrack{}, err
	}
	defer resp.Body.Close()
	return parseYMTrack(io.LimitReader(resp.Body, 1<<20), resp.StatusCode)
}

func parseYMTrack(r io.Reader, status int) (ymTrack, error) {
	var out struct {
		Result []struct {
			Title      string  `json:"title"`
			DurationMs float64 `json:"durationMs"`
			CoverURI   string  `json:"coverUri"`
			Artists    []struct {
				Name string `json:"name"`
			} `json:"artists"`
			Albums []struct {
				Title string `json:"title"`
			} `json:"albums"`
		} `json:"result"`
	}
	if err := json.NewDecoder(r).Decode(&out); err != nil || len(out.Result) == 0 {
		return ymTrack{}, fmt.Errorf("ответ %d", status)
	}
	x := out.Result[0]
	t := ymTrack{Title: x.Title, Dur: x.DurationMs / 1000}
	var names []string
	for _, ar := range x.Artists {
		names = append(names, ar.Name)
	}
	t.Artist = strings.Join(names, ", ")
	if len(x.Albums) > 0 {
		t.Album = x.Albums[0].Title
	}
	if x.CoverURI != "" {
		t.Cover = "https://" + strings.Replace(x.CoverURI, "%%", "400x400", 1)
	}
	return t, nil
}

// Active — у Алисы что-то играет или стоит на паузе (паузу показываем полчаса).
func (a *AliceMusic) Active() bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state == "playing" || (a.state == "paused" && time.Since(a.pausedT) < 30*time.Minute)
}

func (a *AliceMusic) Snapshot() MusicState {
	a.mu.Lock()
	defer a.mu.Unlock()
	s := MusicState{Source: "yandex", Playing: a.state == "playing", Paused: a.state == "paused",
		Track: Track{ID: a.id, Title: a.info.Title, Author: a.info.Artist}, Pos: a.curPos(), Dur: a.info.Dur,
		Cover: a.info.Cover, Album: a.info.Album}
	if a.id != "" {
		s.Link = "https://music.yandex.ru/track/" + a.id
	}
	return s
}

// Control — управление музыкой Алисы её же голосовыми командами, без ответа вслух.
func (a *AliceMusic) Control(action string, sec float64) error {
	cmd := map[string]string{"pause": "пауза", "resume": "продолжи", "next": "следующий трек", "prev": "предыдущий трек", "stop": "стоп"}[action]
	if action == "seek" {
		a.mu.Lock()
		d := int(sec - a.curPos())
		a.mu.Unlock()
		switch {
		case d > 0:
			cmd = fmt.Sprintf("перемотай вперёд на %d секунд", d)
		case d < 0:
			cmd = fmt.Sprintf("перемотай назад на %d секунд", -d)
		default:
			return nil
		}
		a.mu.Lock()
		a.pos, a.posAt = sec, time.Now()
		a.mu.Unlock()
	}
	if cmd == "" {
		return fmt.Errorf("для Яндекс Музыки так нельзя")
	}
	return AliceText(cmd, true)
}

// tailLog — читать новые строки лога. Лог ротируется: при смене файла новый читаем с начала.
func tailLog(ctx context.Context, path string, fn func(string)) {
	var f *os.File
	var ino uint64
	var rd *bufio.Reader
	for ctx.Err() == nil {
		time.Sleep(250 * time.Millisecond)
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		if cur := inode(st); f == nil || cur != ino {
			if f != nil {
				f.Close()
			}
			if f, err = os.Open(path); err != nil {
				f = nil
				continue
			}
			if ino == 0 {
				_, _ = f.Seek(0, io.SeekEnd) // при запуске старые события не переигрываем
			}
			ino = cur
			rd = bufio.NewReaderSize(f, 16384)
		}
		for {
			line, err := rd.ReadString('\n')
			if err != nil {
				if len(line) > 0 {
					_, _ = f.Seek(-int64(len(line)), io.SeekCurrent)
					rd.Reset(f)
				}
				break
			}
			fn(line)
		}
	}
	if f != nil {
		f.Close()
	}
}

// Playing — у Алисы звучит музыка прямо сейчас.
func (a *AliceMusic) Playing() bool {
	if a == nil {
		return false
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.state == "playing"
}

// PausedSince — когда Алиса встала на паузу (нулевое время — не на паузе).
func (a *AliceMusic) PausedSince() time.Time {
	if a == nil {
		return time.Time{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.state != "paused" {
		return time.Time{}
	}
	return a.pausedT
}
