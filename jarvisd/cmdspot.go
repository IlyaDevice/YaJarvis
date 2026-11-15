package main

// Мгновенные команды во время разговора: «стоп», «хватит», «пауза», «громче», «тише». Их слышит
// штатный распознаватель команд Станции (программа spotcmd рядом с jarvisd) прямо на колонке,
// без интернета и без расшифровки. Запущен он только пока Джарвис занят: в остальное время
// процессор не тратится, а музыкой Алисы управляет сама Алиса.

import (
	"bufio"
	"context"
	"encoding/binary"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type CmdSpotter struct {
	path   string
	onHit  func(string)
	mu     sync.Mutex
	cmd    *exec.Cmd
	feed   chan []int16
	lastOn time.Time
	broken time.Time // последний неудачный запуск: не перезапускать чаще раза в минуту
}

func NewCmdSpotter(dataDir string, onHit func(string)) *CmdSpotter {
	p := filepath.Join(dataDir, "spotcmd")
	if _, err := os.Stat(p); err != nil {
		return nil
	}
	return &CmdSpotter{path: p, onHit: onHit}
}

// Feed — кадр микрофона, пока Джарвис занят. Запускает распознаватель, если он ещё не работает.
func (c *CmdSpotter) Feed(pcm []int16) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.lastOn = time.Now()
	if c.cmd == nil && time.Since(c.broken) > time.Minute {
		c.start()
	}
	if c.feed != nil {
		select {
		case c.feed <- pcm:
		default: // не успевает — пропускаем кадр, Джарвис не ждёт
		}
	}
	c.mu.Unlock()
}

// Idle — Джарвис свободен: через 15 с тишины распознаватель выключается.
func (c *CmdSpotter) Idle() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cmd != nil && time.Since(c.lastOn) > 15*time.Second {
		close(c.feed)
		c.cmd, c.feed = nil, nil
	}
}

func (c *CmdSpotter) start() {
	cmd := exec.Command(c.path)
	in, err := cmd.StdinPipe()
	if err != nil {
		c.broken = time.Now()
		return
	}
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		log.Printf("команды: распознаватель не запустился: %v", err)
		c.broken = time.Now()
		return
	}
	feed := make(chan []int16, 64)
	c.cmd, c.feed = cmd, feed
	started := time.Now()
	go func() {
		buf := make([]byte, 0, 640)
		for pcm := range feed {
			buf = buf[:0]
			for _, s := range pcm {
				buf = binary.LittleEndian.AppendUint16(buf, uint16(s))
			}
			if _, err := in.Write(buf); err != nil {
				break
			}
		}
		in.Close()
	}()
	go func() {
		sc := bufio.NewScanner(out)
		for sc.Scan() {
			if p, ok := strings.CutPrefix(sc.Text(), "HIT "); ok {
				c.onHit(strings.TrimSpace(p))
			}
		}
		err := cmd.Wait()
		c.mu.Lock()
		if c.cmd == cmd {
			c.cmd, c.feed = nil, nil
			close(feed)
			if time.Since(started) < 5*time.Second {
				log.Printf("команды: распознаватель сразу завершился (%v) — попробую через минуту", err)
				c.broken = time.Now()
			}
		}
		c.mu.Unlock()
	}()
}

// onCommand — распознаватель услышал команду, пока Джарвис занят.
func (e *Engine) onCommand(phrase string) {
	w := strings.ToLower(phrase)
	if !e.Talking() {
		if !e.music.Playing() {
			return
		}
		// играет музыка с YouTube, Джарвис молчит: команды относятся к ней
		switch {
		case strings.Contains(w, "пауза"):
			e.music.Pause()
			return
		case strings.Contains(w, "стоп") || strings.Contains(w, "хватит"):
			log.Printf("музыка: «%s» — стоп", phrase)
			e.music.Stop()
			return
		case strings.Contains(w, "дальше") || strings.Contains(w, "следующ"):
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				if _, err := e.music.Next(ctx); err != nil {
					log.Printf("музыка: «%s»: %v", phrase, err)
				}
			}()
			return
		case strings.Contains(w, "громче") || strings.Contains(w, "тише"):
		default:
			return
		}
	}
	switch {
	case strings.Contains(w, "стоп") || strings.Contains(w, "хватит") || strings.Contains(w, "пауза"):
		log.Printf("Джарвис: «%s» — стоп (встроенный распознаватель)", phrase)
		e.Stop("сказали «" + phrase + "»")
		e.journal.note("стоп", phrase, "")
	case strings.Contains(w, "громче") || strings.Contains(w, "тише"):
		step := 10
		if strings.Contains(w, "тише") {
			step = -10
		}
		v := 0
		_ = e.store.Update(func(s *Settings) error { // громкость голоса активного ассистента
			for i := range s.Profiles {
				if s.Profiles[i].ID == s.ActiveProfile().ID {
					s.Profiles[i].Volume = max(10, min(100, s.Profiles[i].Volume+step))
					v = s.Profiles[i].Volume
				}
			}
			return nil
		})
		log.Printf("Джарвис: «%s» — громкость %d%% (встроенный распознаватель)", phrase, v)
	default:
		log.Printf("Джарвис: распознаватель услышал «%s» — не моя команда", phrase)
	}
}
