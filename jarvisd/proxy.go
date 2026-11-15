package main

// VLESS прямо внутри jarvisd: ссылка vless://… → конфиг ядра Xray → net.Conn для HTTP и WebSocket.
// Никакого TUN и отдельного sing-box: трафик через туннель идёт только у самого Джарвиса,
// штатная Алиса и музыка Яндекса работают напрямую, как раньше.

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xtls/xray-core/app/dispatcher"
	xlog "github.com/xtls/xray-core/app/log"
	"github.com/xtls/xray-core/app/proxyman"
	clog "github.com/xtls/xray-core/common/log"
	xnet "github.com/xtls/xray-core/common/net"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/common/uuid"
	"github.com/xtls/xray-core/core"
	"github.com/xtls/xray-core/proxy/vless"
	vlessout "github.com/xtls/xray-core/proxy/vless/outbound"
	"github.com/xtls/xray-core/transport/internet"
	"github.com/xtls/xray-core/transport/internet/reality"
	"github.com/xtls/xray-core/transport/internet/tcp"
	xtls "github.com/xtls/xray-core/transport/internet/tls"
	"github.com/xtls/xray-core/transport/internet/websocket"

	// регистрация нужных частей ядра (только клиент VLESS — меньше бинарник)
	_ "github.com/xtls/xray-core/app/dispatcher"
	_ "github.com/xtls/xray-core/app/proxyman/outbound"
	_ "github.com/xtls/xray-core/proxy/vless/outbound"
	_ "github.com/xtls/xray-core/transport/internet/reality"
	_ "github.com/xtls/xray-core/transport/internet/tcp"
	_ "github.com/xtls/xray-core/transport/internet/tls"
	_ "github.com/xtls/xray-core/transport/internet/websocket"
)

// VlessLink — разобранная ссылка; поля показываются в веб-настройках, чтобы было видно, что поняли.
type VlessLink struct {
	Name     string `json:"name"`
	UUID     string `json:"-"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Network  string `json:"network"`  // tcp, ws, grpc, xhttp
	Security string `json:"security"` // none, tls, reality
	SNI      string `json:"sni"`
	Flow     string `json:"flow"`
	Finger   string `json:"fp"`
	PubKey   string `json:"-"`
	ShortID  string `json:"-"`
	SpiderX  string `json:"-"`
	Path     string `json:"path"`
	HostHdr  string `json:"-"`
	Service  string `json:"-"`
	ALPN     string `json:"-"`
	Mode     string `json:"-"`
	Insecure bool   `json:"insecure"` // allowInsecure=1: сертификат сервера не проверяется
}

func ParseVless(link string) (*VlessLink, error) {
	link = strings.TrimSpace(link)
	if !strings.HasPrefix(strings.ToLower(link), "vless://") {
		return nil, errors.New("ссылка должна начинаться с vless://")
	}
	u, err := url.Parse(link)
	if err != nil {
		return nil, fmt.Errorf("не разобрать ссылку: %v", err)
	}
	if u.User == nil || u.User.Username() == "" {
		return nil, errors.New("в ссылке нет UUID (часть до @)")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port <= 0 || port > 65535 {
		return nil, errors.New("в ссылке нет порта сервера")
	}
	q := u.Query()
	get := func(k, def string) string {
		if v := q.Get(k); v != "" {
			return v
		}
		return def
	}
	v := &VlessLink{
		Name: u.Fragment, UUID: u.User.Username(), Host: u.Hostname(), Port: port,
		Network: strings.ToLower(get("type", "tcp")), Security: strings.ToLower(get("security", "none")),
		SNI: get("sni", get("peer", "")), Flow: q.Get("flow"), Finger: get("fp", "chrome"),
		PubKey: get("pbk", ""), ShortID: q.Get("sid"), SpiderX: q.Get("spx"),
		Path: q.Get("path"), HostHdr: q.Get("host"), Service: q.Get("serviceName"), ALPN: q.Get("alpn"), Mode: q.Get("mode"),
		Insecure: q.Get("allowInsecure") == "1" || q.Get("insecure") == "1" || q.Get("allowInsecure") == "true",
	}
	if v.Network == "raw" {
		v.Network = "tcp"
	}
	switch v.Network {
	case "tcp", "ws":
	case "grpc":
		// gRPC убран: он один добавлял 2,4 МБ к программе, а на флешке колонки всего 87 МБ
		return nil, errors.New("транспорт gRPC не поддерживается — возьмите ссылку с type=tcp (REALITY) или ws")
	default:
		return nil, fmt.Errorf("транспорт %q пока не поддерживается (есть tcp, ws)", v.Network)
	}
	if e := q.Get("encryption"); e != "" && e != "none" {
		return nil, errors.New("постквантовое шифрование VLESS (encryption) пока не поддерживается")
	}
	if _, err := uuid.ParseString(v.UUID); err != nil {
		return nil, errors.New("UUID в ссылке неправильный")
	}
	if v.Flow != "" && v.Flow != vless.XRV && v.Flow != vless.XRV+"-udp443" {
		return nil, fmt.Errorf("flow %q не поддерживается", v.Flow)
	}
	if v.Security == "reality" {
		if k, err := base64.RawURLEncoding.DecodeString(v.PubKey); err != nil || len(k) != 32 {
			return nil, errors.New("pbk (ключ REALITY) в ссылке неправильный")
		}
		if len(v.ShortID) > 16 || len(v.ShortID)%2 != 0 {
			return nil, errors.New("sid (short id REALITY) в ссылке неправильный")
		}
		if _, err := hex.DecodeString(v.ShortID); err != nil {
			return nil, errors.New("sid (short id REALITY) в ссылке неправильный")
		}
		if v.Network == "ws" {
			return nil, errors.New("REALITY не работает с ws — проверьте ссылку")
		}
	}
	switch v.Security {
	case "none", "tls", "reality":
	default:
		return nil, fmt.Errorf("шифрование %q не поддерживается (есть tls, reality)", v.Security)
	}
	if v.Security == "reality" && v.PubKey == "" {
		return nil, errors.New("для REALITY в ссылке нужен pbk (публичный ключ)")
	}
	return v, nil
}

func (v *VlessLink) coreConfig() (*core.Config, error) {
	sni := v.SNI
	if sni == "" && net.ParseIP(v.Host) == nil {
		sni = v.Host
	}
	fp := strings.ToLower(v.Finger)
	if fp == "" {
		fp = "chrome"
	}
	if xtls.GetFingerprint(fp) == nil {
		fp = "chrome"
	}
	stream := &internet.StreamConfig{ProtocolName: map[string]string{"tcp": "tcp", "ws": "websocket"}[v.Network]}
	switch v.Network {
	case "tcp":
		stream.TransportSettings = append(stream.TransportSettings, &internet.TransportConfig{ProtocolName: "tcp", Settings: serial.ToTypedMessage(&tcp.Config{})})
	case "ws":
		path := v.Path
		if path == "" {
			path = "/"
		}
		stream.TransportSettings = append(stream.TransportSettings, &internet.TransportConfig{ProtocolName: "websocket",
			Settings: serial.ToTypedMessage(&websocket.Config{Path: path, Host: v.HostHdr})})
	}
	switch v.Security {
	case "tls":
		t := &xtls.Config{ServerName: sni, Fingerprint: fp, AllowInsecure: v.Insecure}
		if v.ALPN != "" {
			t.NextProtocol = strings.Split(v.ALPN, ",")
		}
		tm := serial.ToTypedMessage(t)
		stream.SecuritySettings, stream.SecurityType = []*serial.TypedMessage{tm}, tm.Type
	case "reality":
		pk, _ := base64.RawURLEncoding.DecodeString(v.PubKey)
		sid := make([]byte, 8)
		_, _ = hex.Decode(sid, []byte(v.ShortID))
		spx := v.SpiderX
		if spx == "" || spx[0] != '/' {
			spx = "/"
		}
		r := &reality.Config{ServerName: sni, Fingerprint: fp, PublicKey: pk, ShortId: sid, SpiderX: spx, SpiderY: make([]int64, 10)}
		tm := serial.ToTypedMessage(r)
		stream.SecuritySettings, stream.SecurityType = []*serial.TypedMessage{tm}, tm.Type
	}
	id, _ := uuid.ParseString(v.UUID)
	account := &vless.Account{Id: id.String(), Flow: v.Flow, Encryption: "none"}
	out := &core.OutboundHandlerConfig{
		Tag:            "proxy",
		SenderSettings: serial.ToTypedMessage(&proxyman.SenderConfig{StreamSettings: stream}),
		ProxySettings: serial.ToTypedMessage(&vlessout.Config{Vnext: &protocol.ServerEndpoint{
			Address: xnet.NewIPOrDomain(xnet.ParseAddress(v.Host)), Port: uint32(v.Port),
			User: &protocol.User{Account: serial.ToTypedMessage(account)}}}),
	}
	return &core.Config{
		App: []*serial.TypedMessage{
			serial.ToTypedMessage(&xlog.Config{ErrorLogType: xlog.LogType_Console, ErrorLogLevel: clog.Severity_Warning, AccessLogType: xlog.LogType_None}),
			serial.ToTypedMessage(&dispatcher.Config{}), serial.ToTypedMessage(&proxyman.OutboundConfig{})},
		Outbound: []*core.OutboundHandlerConfig{out},
	}, nil
}

// Proxy — общий выход в интернет для облачных сервисов. Без ссылки — напрямую.
type Proxy struct {
	mu   sync.RWMutex
	inst *core.Instance
	link *VlessLink
	err  string

	warmMu sync.Mutex
	warm   map[string]*warmConn // готовые TLS-соединения по адресу «хост:443»

	use func(svc string) bool // галочка «через VPN» у подключения (netroute.go)
}

type warmConn struct {
	c  net.Conn
	at time.Time
}

type freshKey struct{}

// FreshDial — контекст, с которым готовое соединение не берётся: повтор после неудачи на «остывшем».
func FreshDial(ctx context.Context) context.Context { return context.WithValue(ctx, freshKey{}, true) }

// Замер на колонке (13.11.2026): сервер OpenAI закрывает соединение, по которому ничего не прислали,
// где-то между 8 и 15 секундами. Поэтому готовое соединение меняем каждые 5–6 секунд и не берём старше 8.
const (
	warmRefresh = 5500 * time.Millisecond
	warmMaxAge  = 8 * time.Second
)

func (p *Proxy) dialTLS(ctx context.Context, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	// Соединение через туннель живёт, пока жив контекст, с которым его открыли: отмена таймера закрыла бы и его.
	// Поэтому открываем с неотменяемым контекстом, а время ожидания ограничиваем сами.
	type dialed struct {
		c   net.Conn
		err error
	}
	ch := make(chan dialed, 1)
	go func() {
		c, err := p.DialContext(context.WithoutCancel(ctx), "tcp", addr)
		ch <- dialed{c, err}
	}()
	var raw net.Conn
	select {
	case d := <-ch:
		if d.err != nil {
			return nil, d.err
		}
		raw = d.c
	case <-ctx.Done():
		go func() {
			if d := <-ch; d.c != nil {
				d.c.Close()
			}
		}()
		return nil, ctx.Err()
	}
	c := tls.Client(raw, &tls.Config{ServerName: host, NextProtos: []string{"http/1.1"}, MinVersion: tls.VersionTLS12})
	if err := c.HandshakeContext(ctx); err != nil {
		raw.Close()
		return nil, err
	}
	return c, nil
}

// KeepWarm — держать наготове одно TLS-соединение с адресом, пока want() возвращает true.
// Разговор тогда открывается без ожидания туннеля и TLS: в среднем около 1,0 с вместо 1,2–2,3 с
// (само соединение занимает 0,15–1,2 с). Соединение, по которому ничего не отправлено, бесплатное.
func (p *Proxy) KeepWarm(addr string, want func() bool) {
	go func() {
		for {
			time.Sleep(500 * time.Millisecond)
			p.warmMu.Lock()
			w := p.warm[addr]
			p.warmMu.Unlock()
			if !want() {
				if w != nil {
					p.dropWarm(addr)
				}
				continue
			}
			if w != nil && time.Since(w.at) < warmRefresh {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
			c, err := p.dialTLS(ctx, addr)
			cancel()
			if err != nil {
				time.Sleep(10 * time.Second) // нет сети или туннеля — не долбим
				continue
			}
			p.warmMu.Lock() // новое готово — старое закрываем
			if p.warm == nil {
				p.warm = map[string]*warmConn{}
			}
			if old := p.warm[addr]; old != nil {
				old.c.Close()
			}
			p.warm[addr] = &warmConn{c: c, at: time.Now()}
			p.warmMu.Unlock()
		}
	}()
}

func (p *Proxy) dropWarm(addr string) {
	p.warmMu.Lock()
	if w := p.warm[addr]; w != nil {
		w.c.Close()
		delete(p.warm, addr)
	}
	p.warmMu.Unlock()
}

func (p *Proxy) takeWarm(addr string) net.Conn {
	p.warmMu.Lock()
	defer p.warmMu.Unlock()
	w := p.warm[addr]
	if w == nil {
		return nil
	}
	delete(p.warm, addr)
	if time.Since(w.at) > warmMaxAge {
		w.c.Close()
		return nil
	}
	log.Printf("сеть: беру готовое соединение с %s (ждало %.0f с)", addr, time.Since(w.at).Seconds())
	return w.c
}

// LiveClient — HTTP-клиент для открытия разговора (WebSocket): берёт готовое TLS-соединение, если оно есть.
func (p *Proxy) LiveClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		DialContext: p.DialContext,
		DialTLSContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if ctx.Value(freshKey{}) == nil {
				if c := p.takeWarm(addr); c != nil {
					return c, nil
				}
			}
			return p.dialTLS(ctx, addr)
		},
		MaxIdleConns: 2, IdleConnTimeout: 30 * time.Second,
	}}
}

func (p *Proxy) Apply(link string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.inst != nil {
		p.inst.Close()
		p.inst, p.link = nil, nil
	}
	p.warmMu.Lock() // готовые соединения шли через старый туннель
	for a, w := range p.warm {
		w.c.Close()
		delete(p.warm, a)
	}
	p.warmMu.Unlock()
	p.err = ""
	if strings.TrimSpace(link) == "" {
		return nil
	}
	v, err := ParseVless(link)
	if err != nil {
		p.err = err.Error()
		return err
	}
	cfg, err := v.coreConfig()
	if err != nil {
		p.err = "Xray не принял настройки: " + err.Error()
		return errors.New(p.err)
	}
	inst, err := core.New(cfg)
	if err == nil {
		err = inst.Start()
	}
	if err != nil {
		p.err = "Xray не запустился: " + err.Error()
		return errors.New(p.err)
	}
	p.inst, p.link = inst, v
	return nil
}

func (p *Proxy) Status() (link *VlessLink, errText string) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.link, p.err
}

// DialContext — для http.Transport: через VLESS, если он настроен, иначе напрямую.
func (p *Proxy) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	p.mu.RLock()
	inst := p.inst
	p.mu.RUnlock()
	if inst == nil {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, err := xnet.PortFromString(portStr)
	if err != nil {
		return nil, err
	}
	return core.Dial(ctx, inst, xnet.TCPDestination(xnet.ParseAddress(host), port))
}

// Client — HTTP-клиент для облака (OpenAI, ElevenLabs). Яндекс ходит напрямую (DirectClient).
func (p *Proxy) Client(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: &http.Transport{
		DialContext: p.DialContext, ForceAttemptHTTP2: true, MaxIdleConns: 8,
		IdleConnTimeout: 90 * time.Second, TLSHandshakeTimeout: 10 * time.Second,
	}}
}

var DirectClient = &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
	DialContext: (&net.Dialer{Timeout: 3 * time.Second}).DialContext, MaxIdleConns: 4, IdleConnTimeout: 120 * time.Second,
}}
