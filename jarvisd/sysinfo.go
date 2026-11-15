package main

// Состояние самой колонки для главной страницы настроек: температура, память, Wi-Fi, место на диске.

import (
	"os"
	"strconv"
	"strings"
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
				s.WifiDBm = int(lvl)
			}
		}
	}
	s.DiskFreeMB, s.DiskTotalMB = diskSpace(dataDir)
	return s
}
