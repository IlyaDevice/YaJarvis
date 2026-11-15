package main

// Вход в YouTube кодом, как на телевизоре: колонка показывает код, человек вводит его на
// google.com/device со своего телефона — пароль колонка не видит. Хранится только refresh-токен,
// он нужен лишь для видео, которые без входа не играют (18+, «вы не бот»).
// Идентификатор и «секрет» — общедоступные данные приложения YouTube для телевизоров.

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"sync"
	"time"
)

const (
	ytOAuthClient = "861556708454-d6dlm3lh05idd8npek18k6be8ba3oc68.apps.googleusercontent.com"
	ytOAuthSecret = "SboVhoG9s0rNafixCSGGKXAT" // gitleaks:allow -- public upstream YouTube TV application identifier, not a user credential
	ytOAuthScope  = "http://gdata.youtube.com https://www.googleapis.com/auth/youtube"
)

type YTAccount struct {
	client func() *http.Client
	store  *Store

	mu     sync.Mutex
	access string
	exp    time.Time
	code   string // код для google.com/device, пока идёт вход
	url    string
	err    string
	cancel context.CancelFunc
}

func NewYTAccount(store *Store, client func() *http.Client) *YTAccount {
	return &YTAccount{store: store, client: client}
}

type YTAccountState struct {
	SignedIn bool   `json:"signed_in"`
	Name     string `json:"name"`
	Code     string `json:"code"`
	URL      string `json:"url"`
	Error    string `json:"error"`
}

func (a *YTAccount) State() YTAccountState {
	s := a.store.Get()
	a.mu.Lock()
	defer a.mu.Unlock()
	return YTAccountState{SignedIn: s.YouTubeRefresh != "", Name: s.YouTubeName, Code: a.code, URL: a.url, Error: a.err}
}

func (a *YTAccount) form(ctx context.Context, path string, v url.Values) (map[string]any, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, "https://www.youtube.com/o/oauth2/"+path, bytes.NewBufferString(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", ytTVAgent)
	resp, err := a.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return nil, fmt.Errorf("Google ответил %d", resp.StatusCode)
	}
	return out, nil
}

// Begin — получить код и ждать, пока человек введёт его на google.com/device.
func (a *YTAccount) Begin() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	var dev [16]byte
	_, _ = rand.Read(dev[:])
	out, err := a.form(ctx, "device/code", url.Values{"client_id": {ytOAuthClient}, "scope": {ytOAuthScope},
		"device_id": {fmt.Sprintf("%x", dev)}, "device_model": {"ytlr::"}})
	if err != nil {
		cancel()
		return err
	}
	code, devCode := str(out["user_code"]), str(out["device_code"])
	if code == "" || devCode == "" {
		cancel()
		return fmt.Errorf("Google не выдал код: %s", str(out["error"]))
	}
	interval := time.Duration(max(5, int(num(out["interval"])))) * time.Second
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
	}
	a.code, a.url, a.err, a.cancel = code, str(out["verification_url"]), "", cancel
	a.mu.Unlock()
	go a.wait(ctx, devCode, interval)
	return nil
}

func (a *YTAccount) wait(ctx context.Context, devCode string, interval time.Duration) {
	defer func() {
		a.mu.Lock()
		a.code, a.url = "", ""
		a.mu.Unlock()
	}()
	for {
		select {
		case <-ctx.Done():
			a.setErr("код устарел — нажмите «Войти» ещё раз")
			return
		case <-time.After(interval):
		}
		out, err := a.form(ctx, "token", url.Values{"client_id": {ytOAuthClient}, "client_secret": {ytOAuthSecret},
			"code": {devCode}, "grant_type": {"http://oauth.net/grant_type/device/1.0"}})
		if err != nil {
			continue
		}
		switch e := str(out["error"]); e {
		case "":
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "access_denied":
			a.setErr("вход отклонён")
			return
		default:
			a.setErr("Google: " + e)
			return
		}
		refresh := str(out["refresh_token"])
		if refresh == "" {
			a.setErr("Google не выдал токен")
			return
		}
		a.mu.Lock()
		a.access, a.exp = str(out["access_token"]), time.Now().Add(time.Duration(num(out["expires_in"])-60)*time.Second)
		access := a.access
		a.mu.Unlock()
		name := a.channelName(ctx, access)
		_ = a.store.Update(func(s *Settings) error { s.YouTubeRefresh, s.YouTubeName = refresh, name; return nil })
		log.Printf("YouTube: вход выполнен (%s)", name)
		return
	}
}

func (a *YTAccount) setErr(e string) {
	a.mu.Lock()
	a.err = e
	a.mu.Unlock()
}

// channelName — имя канала аккаунта, чтобы в настройках было видно, кто вошёл.
func (a *YTAccount) channelName(ctx context.Context, access string) string {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.googleapis.com/youtube/v3/channels?part=snippet&mine=true", nil)
	req.Header.Set("Authorization", "Bearer "+access)
	resp, err := a.client().Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var out struct {
		Items []struct {
			Snippet struct{ Title string } `json:"snippet"`
		} `json:"items"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if len(out.Items) > 0 {
		return out.Items[0].Snippet.Title
	}
	return "аккаунт Google"
}

// Token — действующий токен доступа ("" — входа нет).
func (a *YTAccount) Token(ctx context.Context) (string, error) {
	refresh := a.store.Get().YouTubeRefresh
	if refresh == "" {
		return "", nil
	}
	a.mu.Lock()
	if a.access != "" && time.Now().Before(a.exp) {
		t := a.access
		a.mu.Unlock()
		return t, nil
	}
	a.mu.Unlock()
	out, err := a.form(ctx, "token", url.Values{"client_id": {ytOAuthClient}, "client_secret": {ytOAuthSecret},
		"refresh_token": {refresh}, "grant_type": {"refresh_token"}})
	if err != nil {
		return "", err
	}
	if e := str(out["error"]); e != "" {
		if e == "invalid_grant" { // вход отозван в аккаунте Google
			_ = a.store.Update(func(s *Settings) error { s.YouTubeRefresh, s.YouTubeName = "", ""; return nil })
		}
		return "", errors.New("Google: " + e)
	}
	a.mu.Lock()
	a.access, a.exp = str(out["access_token"]), time.Now().Add(time.Duration(num(out["expires_in"])-60)*time.Second)
	t := a.access
	a.mu.Unlock()
	return t, nil
}

func (a *YTAccount) Logout() {
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
	}
	a.access, a.code, a.url, a.err = "", "", "", ""
	a.mu.Unlock()
	_ = a.store.Update(func(s *Settings) error { s.YouTubeRefresh, s.YouTubeName = "", ""; return nil })
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

func (w *Web) youtubeLogin(rw http.ResponseWriter, r *http.Request) {
	if w.engine.music == nil || w.engine.music.Account == nil {
		bad(rw, "музыка с YouTube выключена")
		return
	}
	if err := w.engine.music.Account.Begin(); err != nil {
		bad(rw, err.Error())
		return
	}
	writeJSON(rw, 200, w.engine.music.Account.State())
}

func (w *Web) youtubeLogout(rw http.ResponseWriter, r *http.Request) {
	if w.engine.music != nil && w.engine.music.Account != nil {
		w.engine.music.Account.Logout()
	}
	writeJSON(rw, 200, map[string]any{"ok": true})
}
