package main

// Google Диск для длинной записи фона (15 ГБ бесплатно, час фона ≈ 115 МБ).
// Вход — кодом, как на телевизоре (google.com/device). Нужен свой «ключ приложения» Google
// (OAuth-клиент «TVs and Limited Input devices»): ключ YouTube для телевизоров к Диску доступа не даёт.
// Права — только drive.file: колонка видит лишь файлы, которые сама создала, остальной Диск — нет.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"sync"
	"time"
)

const gdriveScope = "https://www.googleapis.com/auth/drive.file"

// ключ приложения Google, вшитый при сборке (build.sh: GDRIVE_CLIENT, GDRIVE_SECRET) — людям ничего настраивать не нужно
var defaultGDriveClient, defaultGDriveSecret = "", ""

// gdriveApp — ключ приложения: из настроек (свой), иначе вшитый.
func gdriveApp(s Settings) (id, secret string) {
	if s.GDriveClient != "" && s.GDriveSecret != "" {
		return s.GDriveClient, s.GDriveSecret
	}
	return defaultGDriveClient, defaultGDriveSecret
}

type GDrive struct {
	store  *Store
	client func() *http.Client

	mu     sync.Mutex
	access string
	exp    time.Time
	code   string
	url    string
	err    string
	cancel context.CancelFunc
}

func (g *GDrive) form(ctx context.Context, endpoint string, v url.Values) (map[string]any, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(v.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := g.client().Do(req)
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

func (g *GDrive) State() map[string]any {
	s := g.store.Get()
	g.mu.Lock()
	defer g.mu.Unlock()
	id, _ := gdriveApp(s)
	return map[string]any{"client_set": id != "", "signed_in": s.GDriveRefresh != "",
		"code": g.code, "url": g.url, "error": g.err}
}

// Begin — код для google.com/device; ждём, пока человек его введёт.
func (g *GDrive) Begin() error {
	s := g.store.Get()
	client, _ := gdriveApp(s)
	if client == "" {
		return errors.New("в этой сборке нет ключа приложения Google — его добавляет разработчик")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	out, err := g.form(ctx, "https://oauth2.googleapis.com/device/code", url.Values{"client_id": {client}, "scope": {gdriveScope}})
	if err != nil {
		cancel()
		return err
	}
	if e := str(out["error"]); e != "" {
		cancel()
		return fmt.Errorf("Google: %s %s", e, str(out["error_description"]))
	}
	interval := time.Duration(max(5, int(num(out["interval"])))) * time.Second
	g.mu.Lock()
	if g.cancel != nil {
		g.cancel()
	}
	g.code, g.url, g.err, g.cancel = str(out["user_code"]), str(out["verification_url"]), "", cancel
	g.mu.Unlock()
	go g.wait(ctx, str(out["device_code"]), interval)
	return nil
}

func (g *GDrive) wait(ctx context.Context, devCode string, interval time.Duration) {
	defer func() { g.mu.Lock(); g.code, g.url = "", ""; g.mu.Unlock() }()
	client, secret := gdriveApp(g.store.Get())
	for {
		select {
		case <-ctx.Done():
			g.setErr("код устарел — нажмите «Войти» ещё раз")
			return
		case <-time.After(interval):
		}
		out, err := g.form(ctx, "https://oauth2.googleapis.com/token", url.Values{"client_id": {client}, "client_secret": {secret},
			"device_code": {devCode}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}})
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
			g.setErr("вход отклонён")
			return
		default:
			g.setErr("Google: " + e)
			return
		}
		g.mu.Lock()
		g.access, g.exp = str(out["access_token"]), time.Now().Add(time.Duration(num(out["expires_in"])-60)*time.Second)
		g.mu.Unlock()
		refresh := str(out["refresh_token"])
		_ = g.store.Update(func(s *Settings) error { s.GDriveRefresh, s.GDriveFolder = refresh, ""; return nil })
		log.Printf("Google Диск: вход выполнен")
		return
	}
}

func (g *GDrive) setErr(e string) { g.mu.Lock(); g.err = e; g.mu.Unlock() }

func (g *GDrive) token(ctx context.Context) (string, error) {
	s := g.store.Get()
	if s.GDriveRefresh == "" {
		return "", errors.New("не выполнен вход в Google Диск")
	}
	g.mu.Lock()
	if g.access != "" && time.Now().Before(g.exp) {
		t := g.access
		g.mu.Unlock()
		return t, nil
	}
	g.mu.Unlock()
	client, secret := gdriveApp(s)
	out, err := g.form(ctx, "https://oauth2.googleapis.com/token", url.Values{"client_id": {client}, "client_secret": {secret},
		"refresh_token": {s.GDriveRefresh}, "grant_type": {"refresh_token"}})
	if err != nil {
		return "", err
	}
	if e := str(out["error"]); e != "" {
		if e == "invalid_grant" {
			_ = g.store.Update(func(s *Settings) error { s.GDriveRefresh = ""; return nil })
			return "", errors.New("вход в Google Диск истёк — войдите заново")
		}
		if client == "" || secret == "" {
			return "", errors.New("в этой сборке нет ключа приложения Google — обновите Джарвиса")
		}
		return "", fmt.Errorf("Google: %s %s", e, str(out["error_description"]))
	}
	g.mu.Lock()
	g.access, g.exp = str(out["access_token"]), time.Now().Add(time.Duration(num(out["expires_in"])-60)*time.Second)
	t := g.access
	g.mu.Unlock()
	return t, nil
}

func (g *GDrive) api(ctx context.Context, method, endpoint, ctype string, body io.Reader) (map[string]any, error) {
	tok, err := g.token(ctx)
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequestWithContext(ctx, method, endpoint, body)
	req.Header.Set("Authorization", "Bearer "+tok)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	resp, err := g.client().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out)
	if resp.StatusCode/100 != 2 {
		msg := ""
		if e, ok := out["error"].(map[string]any); ok {
			msg = str(e["message"])
		}
		return nil, fmt.Errorf("Google Диск ответил %d %s", resp.StatusCode, msg)
	}
	return out, nil
}

// folder — папка «Jarvis — фон комнаты» (создаётся при первой отправке).
func (g *GDrive) folder(ctx context.Context) (string, error) {
	if id := g.store.Get().GDriveFolder; id != "" {
		return id, nil
	}
	meta, _ := json.Marshal(map[string]any{"name": "Jarvis — фон комнаты", "mimeType": "application/vnd.google-apps.folder"})
	out, err := g.api(ctx, http.MethodPost, "https://www.googleapis.com/drive/v3/files?fields=id", "application/json", bytes.NewReader(meta))
	if err != nil {
		return "", err
	}
	id := str(out["id"])
	_ = g.store.Update(func(s *Settings) error { s.GDriveFolder = id; return nil })
	return id, nil
}

// Upload — положить файл в папку фона.
func (g *GDrive) Upload(ctx context.Context, name string, data []byte) error {
	dir, err := g.folder(ctx)
	if err != nil {
		return err
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Type", "application/json; charset=UTF-8")
	pw, _ := mw.CreatePart(h)
	_ = json.NewEncoder(pw).Encode(map[string]any{"name": name, "parents": []string{dir}})
	h = textproto.MIMEHeader{}
	h.Set("Content-Type", "audio/wav")
	pw, _ = mw.CreatePart(h)
	_, _ = pw.Write(data)
	_ = mw.Close()
	_, err = g.api(ctx, http.MethodPost, "https://www.googleapis.com/upload/drive/v3/files?uploadType=multipart&fields=id",
		"multipart/related; boundary="+mw.Boundary(), &buf)
	if err != nil && strings.Contains(err.Error(), " 404 ") { // папку удалили на Диске — создадим заново
		_ = g.store.Update(func(s *Settings) error { s.GDriveFolder = ""; return nil })
	}
	return err
}

func (g *GDrive) Logout() {
	g.mu.Lock()
	if g.cancel != nil {
		g.cancel()
	}
	g.access, g.code, g.url, g.err = "", "", "", ""
	g.mu.Unlock()
	_ = g.store.Update(func(s *Settings) error { s.GDriveRefresh, s.GDriveFolder = "", ""; return nil })
}

// ---- веб

func (w *Web) putGDrive(rw http.ResponseWriter, r *http.Request) {
	var req struct{ Client, Secret string }
	if readJSON(r, &req) != nil {
		bad(rw, "пустой запрос")
		return
	}
	req.Client, req.Secret = strings.TrimSpace(req.Client), strings.TrimSpace(req.Secret)
	if !strings.HasSuffix(req.Client, ".apps.googleusercontent.com") || req.Secret == "" {
		bad(rw, "Client ID — вида …apps.googleusercontent.com, и нужен Client secret")
		return
	}
	_ = w.store.Update(func(s *Settings) error {
		s.GDriveClient, s.GDriveSecret, s.GDriveRefresh, s.GDriveFolder = req.Client, req.Secret, "", ""
		return nil
	})
	writeJSON(rw, 200, w.engine.bg.drive.State())
}

func (w *Web) gdriveLogin(rw http.ResponseWriter, r *http.Request) {
	if err := w.engine.bg.drive.Begin(); err != nil {
		bad(rw, err.Error())
		return
	}
	writeJSON(rw, 200, w.engine.bg.drive.State())
}

func (w *Web) gdriveLogout(rw http.ResponseWriter, r *http.Request) {
	w.engine.bg.drive.Logout()
	writeJSON(rw, 200, w.engine.bg.drive.State())
}
