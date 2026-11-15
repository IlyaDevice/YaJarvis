package main

import (
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

func TestParseVless(t *testing.T) {
	ok := []string{
		"vless://3b1f7c2e-1d2a-4b8e-9c3f-0a1b2c3d4e5f@example.com:443?type=tcp&security=reality&pbk=Z84J2IelR9ch3k8VtlVhhs5ycBUlXA7wHBWcBrjqnAw&sid=6ba85179e30d4fc2&sni=www.microsoft.com&fp=chrome&flow=xtls-rprx-vision#Мой",
		"vless://3b1f7c2e-1d2a-4b8e-9c3f-0a1b2c3d4e5f@1.2.3.4:8443?type=ws&security=tls&path=%2Fws&host=cdn.example.com&sni=cdn.example.com",
	}
	for _, l := range ok {
		v, err := ParseVless(l)
		if err != nil {
			t.Fatalf("%s: %v", l, err)
		}
		if _, err := v.coreConfig(); err != nil {
			t.Fatalf("config: %v", err)
		}
	}
	bad := []string{"vless://3b1f7c2e-1d2a-4b8e-9c3f-0a1b2c3d4e5f@1.2.3.4:443?type=grpc&security=reality&serviceName=grpc&pbk=Z84J2IelR9ch3k8VtlVhhs5ycBUlXA7wHBWcBrjqnAw&sid=ab&sni=a.com", "vmess://x", "vless://@h:1", "vless://3b1f7c2e-1d2a-4b8e-9c3f-0a1b2c3d4e5f@h?security=reality", "vless://3b1f7c2e-1d2a-4b8e-9c3f-0a1b2c3d4e5f@h:443?security=reality&pbk=short"}
	for _, l := range bad {
		if _, err := ParseVless(l); err == nil {
			t.Fatalf("должна быть ошибка: %s", l)
		}
	}
}

// Реальный туннель: JARVIS_VLESS=vless://… go test -run Live -v
func TestVlessLive(t *testing.T) {
	link := os.Getenv("JARVIS_VLESS")
	if link == "" {
		t.Skip("нет JARVIS_VLESS")
	}
	p := &Proxy{}
	if err := p.Apply(link); err != nil {
		t.Fatal(err)
	}
	c := p.Client(15 * time.Second)
	for _, u := range []string{"https://api.openai.com/v1/models", "https://api.elevenlabs.io/v2/voices?page_size=1", "https://ifconfig.me/ip"} {
		t0 := time.Now()
		resp, err := c.Get(u)
		if err != nil {
			t.Fatalf("%s: %v", u, err)
		}
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		resp.Body.Close()
		if u == "https://ifconfig.me/ip" {
			t.Logf("%s → %d за %v, выход через %s", u, resp.StatusCode, time.Since(t0).Round(time.Millisecond), b)
		} else {
			t.Logf("%s → %d за %v", u, resp.StatusCode, time.Since(t0).Round(time.Millisecond))
		}
		if resp.StatusCode != 401 && resp.StatusCode != 200 {
			t.Errorf("%s: неожиданный ответ %d", u, resp.StatusCode)
		}
	}
	_ = http.StatusOK
}
