package main

// Вход в ChatGPT (подписка Plus/Pro) — тем же способом, что у Codex на устройствах без браузера:
// колонка просит у OpenAI короткий код, человек открывает auth.openai.com/codex/device, входит
// и вводит код. Колонка получает свой токен и сама его продлевает. Пароль Джарвис не видит.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	chatgptIssuer   = "https://auth.openai.com"
	chatgptClientID = "app_EMoamEEZ73f0CkXaXp7hrann" // публичный идентификатор приложения Codex
)

// ChatGPTAuth — сохранённый вход. Токены не показываются в панели и не пишутся в лог.
type ChatGPTAuth struct {
	Access  string `json:"access,omitempty"`
	Refresh string `json:"refresh,omitempty"`
	Account string `json:"account,omitempty"`
	Plan    string `json:"plan,omitempty"`
	Email   string `json:"email,omitempty"`
	Expires int64  `json:"expires,omitempty"` // unix-время окончания access
}

func (a ChatGPTAuth) SignedIn() bool { return a.Refresh != "" && a.Account != "" }

// ChatGPTLogin — вход, ожидающий, пока человек введёт код на сайте.
type ChatGPTLogin struct {
	mu      sync.Mutex
	Code    string
	URL     string
	Until   time.Time
	Err     string
	running bool
}

func (l *ChatGPTLogin) State() map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.running && time.Now().Before(l.Until) {
		return map[string]any{"code": l.Code, "url": l.URL}
	}
	if l.Err != "" {
		return map[string]any{"error": l.Err}
	}
	return nil
}

func jwtClaims(tok string) map[string]any {
	parts := strings.Split(tok, ".")
	if len(parts) < 2 {
		return nil
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(parts[1], "="))
	if err != nil {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func postJSON(ctx context.Context, client *http.Client, u string, body any, out any) (int, error) {
	raw, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if out != nil {
		_ = json.Unmarshal(data, out)
	}
	if resp.StatusCode >= 300 {
		return resp.StatusCode, fmt.Errorf("OpenAI ответил %d: %s", resp.StatusCode, strings.TrimSpace(string(data[:min(len(data), 200)])))
	}
	return resp.StatusCode, nil
}

// tokensToAuth — разобрать ответ /oauth/token.
func tokensToAuth(access, refresh, idToken string, old ChatGPTAuth) (ChatGPTAuth, error) {
	a := old
	a.Access = access
	if refresh != "" {
		a.Refresh = refresh
	}
	for _, t := range []string{idToken, access} {
		c := jwtClaims(t)
		if au, ok := c["https://api.openai.com/auth"].(map[string]any); ok {
			if v := str(au["chatgpt_account_id"]); v != "" {
				a.Account = v
			}
			if v := str(au["chatgpt_plan_type"]); v != "" {
				a.Plan = v
			}
		}
		if v := str(c["email"]); v != "" {
			a.Email = v
		}
	}
	if c := jwtClaims(access); c != nil {
		if exp, ok := c["exp"].(float64); ok {
			a.Expires = int64(exp)
		}
	}
	if a.Account == "" {
		return old, errors.New("в ответе OpenAI нет аккаунта ChatGPT")
	}
	return a, nil
}

func exchangeToken(ctx context.Context, client *http.Client, form url.Values) (access, refresh, idToken string, err error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, chatgptIssuer+"/oauth/token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := client.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	var out struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
		ID      string `json:"id_token"`
		Error   any    `json:"error"`
	}
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	_ = json.Unmarshal(data, &out)
	if resp.StatusCode != 200 || out.Access == "" {
		return "", "", "", fmt.Errorf("OpenAI не выдал токен (%d): %s", resp.StatusCode, strings.TrimSpace(string(data[:min(len(data), 200)])))
	}
	return out.Access, out.Refresh, out.ID, nil
}

// StartChatGPTLogin — получить код и в фоне ждать, пока человек его введёт.
func StartChatGPTLogin(l *ChatGPTLogin, store *Store, client *http.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var uc struct {
		DeviceAuthID string `json:"device_auth_id"`
		UserCode     string `json:"user_code"`
		UserCode2    string `json:"usercode"`
		Interval     any    `json:"interval"`
	}
	if _, err := postJSON(ctx, client, chatgptIssuer+"/api/accounts/deviceauth/usercode",
		map[string]string{"client_id": chatgptClientID}, &uc); err != nil {
		return err
	}
	if uc.UserCode == "" {
		uc.UserCode = uc.UserCode2
	}
	if uc.DeviceAuthID == "" || uc.UserCode == "" {
		return errors.New("OpenAI не выдал код для входа")
	}
	interval := 5 * time.Second
	switch v := uc.Interval.(type) {
	case float64:
		interval = time.Duration(v) * time.Second
	case string:
		var n int
		fmt.Sscan(v, &n)
		if n > 0 {
			interval = time.Duration(n) * time.Second
		}
	}
	l.mu.Lock()
	l.Code, l.URL, l.Until, l.Err, l.running = uc.UserCode, chatgptIssuer+"/codex/device", time.Now().Add(15*time.Minute), "", true
	l.mu.Unlock()
	go func() {
		fail := func(msg string) {
			log.Printf("ChatGPT: вход не удался: %s", msg)
			l.mu.Lock()
			l.running, l.Err = false, msg
			l.mu.Unlock()
		}
		for time.Now().Before(l.Until) {
			time.Sleep(interval)
			l.mu.Lock()
			still := l.running && l.Code == uc.UserCode
			l.mu.Unlock()
			if !still {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			var tr struct {
				Code     string `json:"authorization_code"`
				Verifier string `json:"code_verifier"`
			}
			status, err := postJSON(ctx, client, chatgptIssuer+"/api/accounts/deviceauth/token",
				map[string]string{"device_auth_id": uc.DeviceAuthID, "user_code": uc.UserCode}, &tr)
			cancel()
			if status == 403 || status == 404 || (err == nil && tr.Code == "") {
				continue // ещё не ввели код
			}
			if err != nil {
				if status >= 500 || status == 0 {
					continue
				}
				fail(err.Error())
				return
			}
			ctx, cancel = context.WithTimeout(context.Background(), 20*time.Second)
			access, refresh, id, err := exchangeToken(ctx, client, url.Values{
				"grant_type": {"authorization_code"}, "code": {tr.Code}, "client_id": {chatgptClientID},
				"redirect_uri": {chatgptIssuer + "/deviceauth/callback"}, "code_verifier": {tr.Verifier}})
			cancel()
			if err != nil {
				fail(err.Error())
				return
			}
			err = store.Update(func(s *Settings) error {
				a, err := tokensToAuth(access, refresh, id, ChatGPTAuth{})
				if err == nil {
					s.ChatGPT = a
				}
				return err
			})
			if err != nil {
				fail(err.Error())
				return
			}
			log.Printf("ChatGPT: вход выполнен (подписка %s)", store.Get().ChatGPT.Plan)
			l.mu.Lock()
			l.running = false
			l.mu.Unlock()
			return
		}
		fail("код устарел — начните вход заново")
	}()
	return nil
}

var chatgptRefreshMu sync.Mutex

// ChatGPTToken — действующий токен; за 5 минут до окончания продлевается.
func ChatGPTToken(ctx context.Context, store *Store, client *http.Client) (ChatGPTAuth, error) {
	chatgptRefreshMu.Lock()
	defer chatgptRefreshMu.Unlock()
	a := store.Get().ChatGPT
	if !a.SignedIn() {
		return a, errors.New("не выполнен вход в ChatGPT — войдите в «Подключениях»")
	}
	if a.Access != "" && time.Until(time.Unix(a.Expires, 0)) > 5*time.Minute {
		return a, nil
	}
	access, refresh, id, err := exchangeToken(ctx, client, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {a.Refresh}, "client_id": {chatgptClientID},
		"scope": {"openid profile email"}})
	if err != nil {
		return a, fmt.Errorf("не удалось продлить вход в ChatGPT: %w", err)
	}
	na, err := tokensToAuth(access, refresh, id, a)
	if err != nil {
		return a, err
	}
	_ = store.Update(func(s *Settings) error { s.ChatGPT = na; return nil })
	log.Printf("ChatGPT: вход продлён")
	return na, nil
}
