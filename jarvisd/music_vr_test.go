package main

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

// Пускает ли клиент VR с visitorData из поиска: JARVIS_NET=1 go test -run TestMusicVRVisitor -v
func TestMusicVRVisitor(t *testing.T) {
	if os.Getenv("JARVIS_NET") == "" {
		t.Skip("нужна сеть: JARVIS_NET=1")
	}
	client := &http.Client{Timeout: 30 * time.Second}
	m := NewMusic(func() *http.Client { return client }, func() float64 { return 1 })
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	body := map[string]any{"context": map[string]any{"client": map[string]any{"clientName": "WEB_REMIX", "clientVersion": "1.20250310.01.00", "hl": "ru", "gl": "RU"}},
		"query": "Макс Корж Жить в кайф"}
	out, err := m.post(ctx, "https://music.youtube.com/youtubei/v1/search?prettyPrint=false", body, "Mozilla/5.0")
	if err != nil {
		t.Fatal(err)
	}
	rc, _ := out["responseContext"].(map[string]any)
	vd := str(rc["visitorData"])
	t.Logf("visitorData: %d символов", len(vd))
	for _, c := range []map[string]any{
		{"clientName": "ANDROID_VR", "clientVersion": ytVRVersion, "deviceMake": "Oculus", "deviceModel": "Quest 3", "androidSdkVersion": 32, "osName": "Android", "osVersion": "12L"},
		{"clientName": "ANDROID_VR", "clientVersion": "1.62.27", "deviceMake": "Oculus", "deviceModel": "Quest 3", "androidSdkVersion": 32, "osName": "Android", "osVersion": "12L"},
	} {
		for _, withVD := range []bool{false, true} {
			cl := map[string]any{"hl": "ru", "gl": "RU"}
			for k, v := range c {
				cl[k] = v
			}
			if withVD {
				cl["visitorData"] = vd
			}
			b := map[string]any{"context": map[string]any{"client": cl}, "videoId": "Kx7B-XvmFtE", "contentCheckOk": true, "racyCheckOk": true}
			res, err := m.post(ctx, "https://www.youtube.com/youtubei/v1/player?prettyPrint=false", b, ytVRAgent)
			ps, _ := res["playabilityStatus"].(map[string]any)
			t.Logf("%v visitorData=%v: %v %v", c["clientVersion"], withVD, str(ps["status"]), err)
		}
	}
}
