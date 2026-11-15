package main

// Состояние самой колонки для главной страницы настроек: температура, память, Wi-Fi, место на диске.

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

type SysInfo struct {
	TempC       float64 `json:"temp_c"`       // температура процессора, 0 — неизвестно
	MemTotalMB  int     `json:"mem_total_mb"` // вся память
	MemAvailMB  int     `json:"mem_avail_mb"` // сколько можно занять
	Load1       float64 `json:"load1"`        // средняя загрузка за минуту
	WifiDBm     int     `json:"wifi_dbm"`     // уровень сигнала Wi-Fi, 0 — неизвестно
	UptimeS     int     `json:"uptime_s"`     // сколько колонка работает без перезагрузки
	DiskFreeMB  int     `json:"disk_free_mb"` // свободно на разделе с настройками
	DiskTotalMB int     `json:"disk_total_mb"`
}

func readSys(dataDir string) SysInfo {
	var s SysInfo
	if n := readInt("/sys/class/thermal/thermal_zone0/temp"); n > 0 {
		s.TempC = float64(n) / 1000
	}
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 2 {
				continue
			}
			kb, _ := strconv.Atoi(f[1])
			switch f[0] {
			case "MemTotal:":
				s.MemTotalMB = kb / 1024
			case "MemAvailable:":
				s.MemAvailMB = kb / 1024
			}
		}
	}
	if b, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			s.Load1, _ = strconv.ParseFloat(f[0], 64)
		}
	}
	if b, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(b)); len(f) > 0 {
			up, _ := strconv.ParseFloat(f[0], 64)
			s.UptimeS = int(up)
		}
	}
	if b, err := os.ReadFile("/proc/net/wireless"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) >= 4 && strings.HasSuffix(f[0], ":") { // «wlan0: 0000 60. -50. -256 …» — третье число и есть уровень
				lvl, _ := strconv.ParseFloat(strings.TrimSuffix(f[3], "."), 64)
				if lvl < 0 {
					s.WifiDBm = int(lvl)
				}
			}
		}
	}
	if s.WifiDBm == 0 {
		s.WifiDBm = wifiRSSI()
	}
	s.DiskFreeMB, s.DiskTotalMB = diskSpace(dataDir)
	return s
}

// wifiRSSI — уровень Wi-Fi от wpa_supplicant («RSSI=-52»): у части драйверов (Мини 3) /proc/net/wireless
// пустой. Ответ запоминаем на 10 с, чтобы не запускать wpa_cli на каждый опрос панели.
var wifiCache struct {
	sync.Mutex
	at  time.Time
	dbm int
}

func wifiRSSI() int {
	wifiCache.Lock()
	defer wifiCache.Unlock()
	if time.Since(wifiCache.at) < 10*time.Second {
		return wifiCache.dbm
	}
	wifiCache.at, wifiCache.dbm = time.Now(), 0
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "wpa_cli", "-i", "wlan0", "signal_poll").Output()
	if err != nil {
		return 0
	}
	wifiCache.dbm = parseRSSI(string(out))
	return wifiCache.dbm
}

func parseRSSI(out string) int {
	for _, l := range strings.Split(out, "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), "RSSI="); ok {
			if n, err := strconv.Atoi(v); err == nil && n < 0 && n > -120 {
				return n
			}
		}
	}
	return 0
}
