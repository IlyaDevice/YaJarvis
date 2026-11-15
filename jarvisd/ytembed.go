package main

// Клиент «встроенный плеер» (WEB_EMBEDDED_PLAYER, как видео на чужом сайте). Отдаёт без входа многие
// видео 18+, которые клиент «телевизор» не даёт («Войдите в свой аккаунт»). Ссылки зашифрованы так же —
// расшифровываются тем же скриптом плеера. Как в yt-dlp: страница встраивания с «чужого сайта» даёт
// encryptedHostFlags, без них часть видео не играет.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"
)

const (
	ytEmbedVersion = "2.20260708.00.00"
	ytEmbedAgent   = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/15.5 Safari/605.1.15,gzip(gfe)"
	ytEmbedHost    = "https://www.reddit.com/" // любой сайт не YouTube
)

var (
	ytHostFlagsRe  = regexp.MustCompile(`"encryptedHostFlags":"([^"]+)"`)
	ytEmbedVerRe   = regexp.MustCompile(`"INNERTUBE_CLIENT_VERSION":"([^"]+)"`)
	ytEmbedVisitRe = regexp.MustCompile(`"VISITOR_DATA":"([^"]+)"`)
)

func (m *Music) embedURL(ctx context.Context, id, vd string) (*ytStream, Track, error) {
	info := Track{ID: id}
	sts, err := m.js.prepare(ctx, m.client())
	if err != nil {
		return nil, info, err
	}
	cpc := map[string]any{"html5Preference": "HTML5_PREF_WANTS", "signatureTimestamp": sts}
	ver := ytEmbedVersion
	vd = "" // visitorData — своё, со страницы встраивания: чужое (от поиска) даёт «Видео недоступно»
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://www.youtube.com/embed/"+id+"?html5=1", nil)
	req.Header.Set("User-Agent", ytEmbedAgent)
	req.Header.Set("Referer", ytEmbedHost)
	if resp, err := m.client().Do(req); err == nil {
		page, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if f := ytHostFlagsRe.FindSubmatch(page); f != nil {
			cpc["encryptedHostFlags"] = string(f[1])
		}
		if f := ytEmbedVerRe.FindSubmatch(page); f != nil {
			ver = string(f[1])
		}
		if f := ytEmbedVisitRe.FindSubmatch(page); f != nil {
			vd = string(f[1])
		}
	}
	client := map[string]any{"clientName": "WEB_EMBEDDED_PLAYER", "clientVersion": ver, "userAgent": ytEmbedAgent, "hl": "ru", "gl": "RU"}
	hdr := []string{"X-YouTube-Client-Name", "56", "X-YouTube-Client-Version", ver, "Origin", "https://www.youtube.com",
		"Referer", "https://www.youtube.com/embed/" + id + "?html5=1"}
	if vd != "" {
		client["visitorData"] = vd
		hdr = append(hdr, "X-Goog-Visitor-Id", vd)
	}
	body := map[string]any{"context": map[string]any{"client": client, "thirdParty": map[string]any{"embedUrl": ytEmbedHost}},
		"videoId": id, "contentCheckOk": true, "racyCheckOk": true, "playbackContext": map[string]any{"contentPlaybackContext": cpc}}
	t0 := time.Now()
	out, err := m.post(ctx, "https://www.youtube.com/youtubei/v1/player?prettyPrint=false", body, ytEmbedAgent, hdr...)
	if err != nil {
		return nil, info, err
	}
	if d, ok := out["videoDetails"].(map[string]any); ok {
		info = Track{ID: id, Title: str(d["title"]), Author: str(d["author"])}
	}
	if ps, ok := out["playabilityStatus"].(map[string]any); ok && str(ps["status"]) != "OK" {
		return nil, info, fmt.Errorf("%s %s", str(ps["status"]), str(ps["reason"]))
	}
	sd, _ := out["streamingData"].(map[string]any)
	fm, _ := sd["adaptiveFormats"].([]any)
	if st := m.pickSound(fm, m.tvLink, t0.Add(ytAdWait(out)), "WEB_EMBEDDED_PLAYER", ytEmbedAgent); st != nil {
		return st, info, nil
	}
	return nil, info, errors.New("нет звуковой дорожки Opus")
}
