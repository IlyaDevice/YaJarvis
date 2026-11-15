package main

// Вход в YouTube куками (cookies.txt из браузера) — только для видео, которые без входа не играют:
// 18+ и «войдите, чтобы подтвердить, что вы не бот». Обычные видео запрашиваются анонимно, чтобы
// аккаунт лишний раз не светился. Файл лежит только на колонке: <папка настроек>/youtube_cookies.txt.

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// ytCookies — заголовок Cookie для youtube.com и значения для подписи SAPISIDHASH.
type ytCookies struct {
	header          string
	sapisid, p1, p3 string // SAPISID, __Secure-1PAPISID, __Secure-3PAPISID
	session         bool   // есть куки самого входа (SID / __Secure-1PSID), а не только «сторонние» 3P
}

const ytWebVersion = "2.20260708.00.00"

// CheckLogin — принимает ли YouTube вход по этим кукам (веб-клиент отвечает loggedOut=false).
func (m *Music) CheckLogin(ctx context.Context, c *ytCookies) error {
	if c == nil || !c.session {
		return errors.New("в файле нет входа в аккаунт (нет кук SID и SAPISID). Экспортируйте куки со страницы youtube.com, где вы вошли в аккаунт")
	}
	hdr := append([]string{"X-YouTube-Client-Name", "1", "X-YouTube-Client-Version", ytWebVersion, "Origin", "https://www.youtube.com"}, c.headers()...)
	body := map[string]any{"context": map[string]any{"client": map[string]any{"clientName": "WEB", "clientVersion": ytWebVersion, "hl": "ru", "gl": "RU"}},
		"videoId": "jNQXAC9IVRw"}
	out, err := m.post(ctx, "https://www.youtube.com/youtubei/v1/player?prettyPrint=false", body,
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/138.0.0.0 Safari/537.36", hdr...)
	if err != nil {
		return err
	}
	rc, _ := out["responseContext"].(map[string]any)
	ma, _ := rc["mainAppWebResponseContext"].(map[string]any)
	if lo, ok := ma["loggedOut"].(bool); !ok || lo {
		return errors.New("YouTube не принял вход: куки устарели или выгружены не полностью. Выгрузите заново")
	}
	return nil
}

func loadYTCookies(path string) *ytCookies {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return parseYTCookies(string(data))
}

// parseYTCookies — куки youtube.com из текста cookies.txt (формат Netscape).
func parseYTCookies(data string) *ytCookies {
	c := &ytCookies{}
	var parts []string
	now := time.Now().Unix()
	for _, line := range strings.Split(data, "\n") {
		line = strings.TrimRight(line, "\r")
		line = strings.TrimPrefix(line, "#HttpOnly_")
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 7 || !strings.HasSuffix(strings.TrimPrefix(f[0], "."), "youtube.com") {
			continue
		}
		var exp int64
		fmt.Sscan(f[4], &exp)
		if exp > 0 && exp < now {
			continue
		}
		name, value := f[5], f[6]
		parts = append(parts, name+"="+value)
		switch name {
		case "SID", "__Secure-1PSID", "LOGIN_INFO":
			c.session = true
		case "SAPISID":
			c.sapisid = value
		case "__Secure-1PAPISID":
			c.p1 = value
		case "__Secure-3PAPISID":
			c.p3 = value
		}
	}
	if c.sapisid == "" {
		c.sapisid = c.p3
	}
	if len(parts) == 0 || c.sapisid == "" {
		return nil
	}
	c.header = strings.Join(parts, "; ")
	return c
}

// headers — заголовки авторизованного запроса к youtubei (как у браузера).
func (c *ytCookies) headers() []string {
	const origin = "https://www.youtube.com"
	ts := time.Now().Unix()
	hash := func(v string) string {
		return fmt.Sprintf("%d_%x", ts, sha1.Sum([]byte(fmt.Sprintf("%d %s %s", ts, v, origin))))
	}
	auth := "SAPISIDHASH " + hash(c.sapisid)
	if c.p1 != "" {
		auth += " SAPISID1PHASH " + hash(c.p1)
	}
	if c.p3 != "" {
		auth += " SAPISID3PHASH " + hash(c.p3)
	}
	return []string{"Cookie", c.header, "Authorization", auth, "X-Origin", origin, "X-Goog-AuthUser", "0"}
}

// ytNeedsLogin — YouTube не даёт видео без входа (возраст, «вы не бот»).
func ytNeedsLogin(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	for _, w := range []string{"login_required", "age", "sign in", "войдите", "возраст", "bot", "бот", "unplayable", "content_check"} {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}
