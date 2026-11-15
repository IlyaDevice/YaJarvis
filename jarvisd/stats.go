package main

// История состояния колонки за сутки — для мини-графиков на главной и страницы «Графики».
// Раз в 15 секунд: процессор, температура, память, Wi-Fi, место на диске. Только в памяти (~250 КБ).

import (
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	statEvery = 15 * time.Second
	statKeep  = 24 * time.Hour
)

type StatPoint struct {
	T    int64   `json:"t"`    // unix, секунды
	CPU  float64 `json:"cpu"`  // %
	Temp float64 `json:"temp"` // °C
	Mem  int     `json:"mem"`  // занято, МБ
	Wifi int     `json:"wifi"` // дБм
	Disk int     `json:"disk"` // свободно на /data, МБ
	Load float64 `json:"load"`
	Me   float64 `json:"me"` // сколько процессора занимает сам Джарвис, % от всей колонки
}

type Stats struct {
	dataDir string
	mu      sync.Mutex
	pts     []StatPoint
	busy    uint64
	total   uint64
	self    uint64
}

func (s *Stats) Run() {
	for {
		s.sample()
		time.Sleep(statEvery)
	}
}

func (s *Stats) sample() {
	sys := readSys(s.dataDir)
	p := StatPoint{T: time.Now().Unix(), Temp: sys.TempC, Mem: sys.MemTotalMB - sys.MemAvailMB, Wifi: sys.WifiDBm,
		Disk: sys.DiskFreeMB, Load: sys.Load1}
	if busy, total, ok := cpuTimes(); ok {
		s.mu.Lock()
		self := selfTicks()
		if s.total > 0 && total > s.total {
			p.CPU = float64(busy-s.busy) * 100 / float64(total-s.total)
			if self >= s.self {
				p.Me = min(100, float64(self-s.self)*100/float64(total-s.total))
			}
		}
		s.busy, s.total, s.self = busy, total, self
		s.mu.Unlock()
	}
	s.mu.Lock()
	s.pts = append(s.pts, p)
	if max := int(statKeep / statEvery); len(s.pts) > max {
		s.pts = append(s.pts[:0:0], s.pts[len(s.pts)-max:]...)
	}
	s.mu.Unlock()
}

// cpuTimes — из первой строки /proc/stat: занятое и всё время процессора.
func cpuTimes() (busy, total uint64, ok bool) {
	b, err := os.ReadFile("/proc/stat")
	if err != nil {
		return 0, 0, false
	}
	line, _, _ := strings.Cut(string(b), "\n")
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0, false
	}
	for i, v := range f[1:] {
		n, _ := strconv.ParseUint(v, 10, 64)
		total += n
		if i != 3 && i != 4 { // idle и iowait
			busy += n
		}
	}
	return busy, total, true
}

// selfTicks — процессорное время самого Джарвиса (utime+stime из /proc/self/stat), в тех же тиках, что /proc/stat.
func selfTicks() uint64 {
	b, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0
	}
	s := string(b)
	if i := strings.LastIndexByte(s, ')'); i >= 0 {
		s = s[i+1:]
	}
	f := strings.Fields(s) // f[0] — состояние (поле 3), utime — поле 14, stime — 15
	if len(f) < 13 {
		return 0
	}
	u, _ := strconv.ParseUint(f[11], 10, 64)
	st, _ := strconv.ParseUint(f[12], 10, 64)
	return u + st
}

// Since — точки за последние sec секунд.
func (s *Stats) Since(sec int64) []StatPoint {
	s.mu.Lock()
	defer s.mu.Unlock()
	from := time.Now().Unix() - sec
	i := len(s.pts)
	for i > 0 && s.pts[i-1].T >= from {
		i--
	}
	return append([]StatPoint{}, s.pts[i:]...)
}

func (w *Web) getStats(rw http.ResponseWriter, r *http.Request) {
	sec, _ := strconv.ParseInt(r.URL.Query().Get("range"), 10, 64)
	if sec <= 0 || sec > int64(statKeep/time.Second) {
		sec = 900
	}
	if w.stats == nil {
		writeJSON(rw, 200, map[string]any{"points": []StatPoint{}, "every": int(statEvery / time.Second)})
		return
	}
	writeJSON(rw, 200, map[string]any{"points": w.stats.Since(sec), "every": int(statEvery / time.Second)})
}
