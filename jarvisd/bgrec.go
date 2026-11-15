package main

// Длинная запись фона комнаты для обучения слова: часы звука «как есть» (ТВ, разговоры, музыка),
// на которых модель учится НЕ срабатывать. На колонке места нет, поэтому каждая минута сразу
// уходит файлом WAV на сервер: HTTP PUT по адресу — так умеют WebDAV (Яндекс Диск, Nextcloud)
// и приёмник kws/bg_receiver.py на компьютере.
// Минуту, в которой прозвучал «Джарвис» или шёл разговор, выбрасываем — в фоне имени быть не должно.

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

const bgChunk = 60 * MicRate // минута звука — один файл (~1,9 МБ)

type BgRecorder struct {
	store  *Store
	client func() *http.Client
	drive  *GDrive

	mu        sync.Mutex
	running   bool
	until     time.Time
	buf       []int16
	sent      int
	sentBytes int64
	dropped   int // минуты, выброшенные из-за оклика/разговора или медленной сети
	pending   int
	err       string
}

func wavBytes(pcm []int16) []byte {
	b := make([]byte, 44+2*len(pcm))
	copy(b, "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(36+2*len(pcm)))
	copy(b[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(b[16:], 16)
	binary.LittleEndian.PutUint16(b[20:], 1)
	binary.LittleEndian.PutUint16(b[22:], 1)
	binary.LittleEndian.PutUint32(b[24:], MicRate)
	binary.LittleEndian.PutUint32(b[28:], MicRate*2)
	binary.LittleEndian.PutUint16(b[32:], 2)
	binary.LittleEndian.PutUint16(b[34:], 16)
	copy(b[36:], "data")
	binary.LittleEndian.PutUint32(b[40:], uint32(2*len(pcm)))
	for i, v := range pcm {
		binary.LittleEndian.PutUint16(b[44+2*i:], uint16(v))
	}
	return b
}

// put — отправить файл name на сервер из настроек.
func (b *BgRecorder) put(ctx context.Context, name string, data []byte) error {
	s := b.store.Get()
	if s.BgTo == "gdrive" {
		return b.drive.Upload(ctx, name, data)
	}
	url := strings.TrimRight(s.BgURL, "/") + "/" + name
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, url, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.ContentLength = int64(len(data))
	req.Header.Set("Content-Type", "audio/wav")
	if s.BgUser != "" {
		req.SetBasicAuth(s.BgUser, s.BgPass)
	}
	resp, err := b.client().Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return errors.New("сервер не пустил: проверьте логин и пароль")
	case resp.StatusCode == 404 || resp.StatusCode == 409:
		return errors.New("на сервере нет такой папки — создайте её")
	case resp.StatusCode/100 != 2:
		return fmt.Errorf("сервер ответил %d", resp.StatusCode)
	}
	return nil
}

// Start — писать фон hours часов. Сначала проверяем, что сервер принимает файлы.
func (b *BgRecorder) Start(ctx context.Context, hours float64) error {
	s := b.store.Get()
	if s.BgTo == "gdrive" {
		if s.GDriveRefresh == "" {
			return errors.New("сначала войдите в Google Диск")
		}
	} else if !strings.HasPrefix(s.BgURL, "http://") && !strings.HasPrefix(s.BgURL, "https://") {
		return errors.New("сначала укажите адрес сервера")
	} else if strings.HasPrefix(s.BgURL, "https://webdav.") || s.BgUser != "" { // WebDAV: папку создаём, если её нет
		req, _ := http.NewRequestWithContext(ctx, "MKCOL", strings.TrimRight(s.BgURL, "/")+"/", nil)
		if s.BgUser != "" {
			req.SetBasicAuth(s.BgUser, s.BgPass)
		}
		if resp, err := b.client().Do(req); err == nil {
			resp.Body.Close()
		}
	}
	if s.BgTo != "gdrive" { // на Диск проверочный файл не кладём — проверит первая же минута
		if err := b.put(ctx, "jarvis-bg-check.wav", wavBytes(make([]int16, MicRate/4))); err != nil {
			return err
		}
	} else if _, err := b.drive.folder(ctx); err != nil {
		return err
	}
	hours = max(0.25, min(24, hours))
	b.mu.Lock()
	b.running, b.until, b.buf, b.err = true, time.Now().Add(time.Duration(hours*float64(time.Hour))), nil, ""
	b.sent, b.sentBytes, b.dropped = 0, 0, 0
	b.mu.Unlock()
	log.Printf("фон: запись на %.1f ч → %s", hours, map[bool]string{true: "Google Диск", false: s.BgURL}[s.BgTo == "gdrive"])
	return nil
}

func (b *BgRecorder) Stop() {
	b.mu.Lock()
	was := b.running
	b.running, b.buf = false, nil
	b.mu.Unlock()
	if was {
		log.Printf("фон: запись остановлена")
	}
}

// Feed — кадр микрофона. busy — идёт разговор: эту минуту не отправляем.
func (b *BgRecorder) Feed(pcm []int16, busy bool) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.running {
		return
	}
	if time.Now().After(b.until) {
		b.running, b.buf = false, nil
		log.Printf("фон: запись закончена — отправлено %d мин", b.sent)
		return
	}
	if busy {
		if len(b.buf) > 0 {
			b.buf, b.dropped = nil, b.dropped+1
		}
		return
	}
	if b.buf == nil {
		b.buf = make([]int16, 0, bgChunk)
	}
	b.buf = append(b.buf, pcm...)
	if len(b.buf) < bgChunk {
		return
	}
	chunk := b.buf
	b.buf = nil
	if b.pending >= 2 { // сеть не успевает — памяти колонки жалко, пропускаем минуту
		b.dropped++
		return
	}
	b.pending++
	st := b.store.Get()
	name := "jarvis-bg-" + st.Now().Format("20060102-150405") + ".wav"
	go func() {
		data := wavBytes(chunk)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		err := b.put(ctx, name, data)
		cancel()
		b.mu.Lock()
		b.pending--
		if err != nil {
			b.err = err.Error()
			b.dropped++
		} else {
			b.err = ""
			b.sent++
			b.sentBytes += int64(len(data))
		}
		b.mu.Unlock()
	}()
}

// Discard — прозвучал «Джарвис» (сработало слово): текущую минуту не отправляем.
func (b *BgRecorder) Discard() {
	if b == nil {
		return
	}
	b.mu.Lock()
	if b.running && len(b.buf) > 0 {
		b.buf, b.dropped = nil, b.dropped+1
	}
	b.mu.Unlock()
}

func (b *BgRecorder) State() map[string]any {
	s := b.store.Get()
	b.mu.Lock()
	defer b.mu.Unlock()
	left := 0
	if b.running {
		left = int(time.Until(b.until).Minutes())
	}
	return map[string]any{"running": b.running, "left_min": left, "sent": b.sent, "sent_mb": float64(b.sentBytes) / (1 << 20),
		"dropped": b.dropped, "error": b.err, "url": s.BgURL, "user": s.BgUser, "pass_set": s.BgPass != "", "to": s.BgTo,
		"gdrive": b.drive.State()}
}

// ---- веб

func (w *Web) getBgRec(rw http.ResponseWriter, r *http.Request) {
	writeJSON(rw, 200, w.engine.bg.State())
}

func (w *Web) putBgRec(rw http.ResponseWriter, r *http.Request) {
	var req struct {
		URL  *string `json:"url"`
		User *string `json:"user"`
		Pass *string `json:"pass"`
		To   *string `json:"to"`
	}
	if readJSON(r, &req) != nil {
		bad(rw, "пустой запрос")
		return
	}
	err := w.store.Update(func(s *Settings) error {
		if req.URL != nil {
			u := strings.TrimSpace(*req.URL)
			if u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
				return errors.New("адрес — начиная с http:// или https://")
			}
			s.BgURL = u
		}
		if req.User != nil {
			s.BgUser = strings.TrimSpace(*req.User)
		}
		if req.Pass != nil {
			s.BgPass = *req.Pass
		}
		if req.To != nil && (*req.To == "gdrive" || *req.To == "server") {
			s.BgTo = *req.To
		}
		return nil
	})
	if err != nil {
		bad(rw, err.Error())
		return
	}
	writeJSON(rw, 200, w.engine.bg.State())
}

func (w *Web) startBgRec(rw http.ResponseWriter, r *http.Request) {
	var req struct{ Hours float64 }
	_ = readJSON(r, &req)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := w.engine.bg.Start(ctx, req.Hours); err != nil {
		bad(rw, err.Error())
		return
	}
	writeJSON(rw, 200, w.engine.bg.State())
}

func (w *Web) stopBgRec(rw http.ResponseWriter, r *http.Request) {
	w.engine.bg.Stop()
	writeJSON(rw, 200, w.engine.bg.State())
}
