package main

// Чистая установка: что ещё стоит на колонке из известных модов сообщества и сколько места
// можно освободить. Удаляем только то, что точно знаем (каталог ниже), и никогда не трогаем
// root-доступ, ADB, SSH, отключение обновлений, Entware и файлы самой Алисы.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Addon — известный мод с колонки.
type Addon struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	About string   `json:"about"`
	Paths []string `json:"-"` // каталоги и файлы мода
	Init  []string `json:"-"` // его автозапуск в /etc/init.d
	Stop  []string `json:"-"` // как остановить (ошибки не важны)
	Kill  []string `json:"-"` // процессы
	After string   `json:"-"` // что ещё стереть после его собственного удаления
	KB    int      `json:"kb"`
	Mic   bool     `json:"mic"` // тоже берёт микрофон или голос Алисы — мешает Джарвису
}

var addonCatalog = []Addon{
	{ID: "jarvis-server", Name: "Старый серверный Джарвис", About: "прошлая версия через свой сервер",
		Paths: []string{"/data/opt/jarvis"}, Init: []string{"S98jarvis"}, Stop: []string{"/data/opt/jarvis/jarvis.sh stop"}, Mic: true},
	{ID: "alisa-voice", Name: "Alisa Voice", About: "свой голосовой режим поверх Алисы",
		Paths: []string{"/data/opt/alisa-standalone"}, Init: []string{"S99zz-alisa-voice"}, Stop: []string{"/data/opt/alisa-standalone/run.sh stop"}, Mic: true},
	{ID: "alisa-cli", Name: "Alisa CLI", About: "ChatGPT Voice и VPN sing-box для него",
		Paths: []string{"/data/opt/alisa-cli"}, Stop: []string{"/data/opt/alisa-cli/vpn.sh stop"}, Kill: []string{"sing-box"}, Mic: true},
	{ID: "pulsar", Name: "Pulsar", About: "голосовой спутник Home Assistant",
		Paths: []string{"/data/pulsar"}, Init: []string{"S99zpulsar"}, Stop: []string{"/data/pulsar/ctl.sh disable"}, Kill: []string{"pulsar"}, Mic: true},
	{ID: "quasar-bridge", Name: "Quasar Bridge", About: "своя LLM вместо Алисы",
		Paths: []string{"/data/opt/quasar-bridge"}, Stop: []string{"/data/opt/quasar-bridge/app/control.sh stop"}, Mic: true},
	{ID: "yam", Name: "YAM", About: "менеджер плагинов с веб-панелью :8099",
		Paths: []string{"/data/quazar/yam", "/data/yam", "/data/opt/S99zyam"}, Init: []string{"S99zyam"}, Kill: []string{"yamd"}},
	{ID: "betteryam", Name: "BetterYAM", About: "плагины и веб-панель для Мини",
		Paths: []string{"/data/quazar/betteryam"}, Init: []string{"S99zzbetteryam"}, Kill: []string{"betteryamd"}},
	{ID: "midipult", Name: "MidiPult", About: "веб-панель для Станции Миди",
		Paths: []string{"/data/mypult", "/data/myweb"}, Stop: []string{"sh /data/mypult/uninstall.sh --purge"},
		After: "/data/mypult-uninstall-*"}, // его удаление оставляет резервную копию — нам нужно место
	{ID: "alice-led", Name: "Alice LED", About: "свой индикатор на кольце",
		Paths: []string{"/data/opt/alice-led"}},
}

// Никогда не удаляем init-скрипты с такими именами, даже если в них встречается путь мода.
var protectedInit = map[string]bool{"S99quasar": true, "S98jarvisd": true, "S99zdropbear": true, "rcS": true, "rcK": true}

// probeAddons — найденные моды и их размер (одна команда на колонке). du одним вызовом:
// моды иногда делят один файл жёсткими ссылками (sing-box у старого Джарвиса и Alisa CLI) — не считаем дважды.
func probeAddons(sh func(string) string) []Addon {
	var all []string
	for _, a := range addonCatalog {
		all = append(all, a.Paths...)
	}
	sizes := map[string]int{}
	for _, line := range strings.Split(sh("set --; for p in "+strings.Join(all, " ")+"; do [ -e \"$p\" ] && set -- \"$@\" \"$p\"; done; [ $# -gt 0 ] && du -sk \"$@\" 2>/dev/null"), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			if kb, err := strconv.Atoi(f[0]); err == nil {
				sizes[f[1]] = kb
			}
		}
	}
	var found []Addon
	for _, a := range addonCatalog {
		present := false
		for _, p := range a.Paths {
			if kb, ok := sizes[p]; ok {
				present = true
				a.KB += kb
			}
		}
		if present {
			found = append(found, a)
		}
	}
	return found
}

// Кэш и старые копии самого Джарвиса — их можно стереть всегда, настройки и записи остаются.
const junkPaths = remoteDir + "/voices " + remoteDir + "/jarvisd.new " + remoteDir + "/jarvisd.bak " + remoteDir + "/wake.bin.bak " + remoteDir + "/wake.bin.prev"

func probeJunkKB(sh func(string) string) int {
	kb := 0
	for _, line := range strings.Split(sh("for p in "+junkPaths+"; do [ -e \"$p\" ] && du -sk \"$p\" 2>/dev/null; done"), "\n") {
		if f := strings.Fields(line); len(f) == 2 {
			n, _ := strconv.Atoi(f[0])
			kb += n
		}
	}
	return kb
}

// planCleanup — что удалить: всё найденное при чистой установке, иначе — сколько нужно для места
// (сначала кэш Джарвиса, потом моды, которые мешают микрофону, потом самые большие).
func planCleanup(sp *Speaker, clean bool) (junk bool, remove []Addon) {
	if clean {
		return sp.JunkKB > 0, append([]Addon(nil), sp.Addons...)
	}
	short := sp.NeedMB*1024 - sp.FreeMB*1024
	if short <= 0 {
		return false, nil
	}
	if sp.JunkKB > 0 {
		junk = true
		short -= sp.JunkKB
	}
	order := append([]Addon(nil), sp.Addons...)
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].Mic != order[j].Mic {
			return order[i].Mic
		}
		return order[i].KB > order[j].KB
	})
	for _, a := range order {
		if short <= 0 {
			break
		}
		remove = append(remove, a)
		short -= a.KB
	}
	return junk, remove
}

// removeScript — остановить мод, убрать его автозапуск и файлы.
func removeScript(a Addon) string {
	var b strings.Builder
	for _, s := range a.Stop { // только если скрипт мода на месте; зависнуть установке не даём
		target := ""
		for _, t := range strings.Fields(s) {
			if strings.HasPrefix(t, "/") {
				target = t
				break
			}
		}
		// timeout в busybox Станции нет — ждём сами не дольше 40 с
		fmt.Fprintf(&b, "if [ -e %q ]; then (%s) >/dev/null 2>&1 & p=$!; i=0; while kill -0 $p 2>/dev/null && [ $i -lt 40 ]; do sleep 1; i=$((i+1)); done; kill $p 2>/dev/null; fi; ", target, s)
	}
	for _, k := range a.Kill {
		fmt.Fprintf(&b, "killall %s >/dev/null 2>&1; ", k)
	}
	b.WriteString("mount -o remount,rw / 2>/dev/null; ")
	for _, n := range a.Init {
		if !protectedInit[n] {
			fmt.Fprintf(&b, "rm -f /etc/init.d/%s; ", n)
		}
	}
	// чужие init-скрипты, которые запускают этот мод (кроме защищённых)
	for _, p := range a.Paths {
		fmt.Fprintf(&b, "for f in $(grep -lE '%s(/|\"|$| )' /etc/init.d/* 2>/dev/null); do case ${f##*/} in %s) ;; *) rm -f \"$f\" ;; esac; done; ",
			strings.ReplaceAll(p, ".", "\\."), protectedCase())
	}
	b.WriteString("mount -o remount,ro / 2>/dev/null; ")
	b.WriteString("rm -rf " + strings.Join(a.Paths, " ") + " " + a.After + "; true")
	return b.String()
}

func protectedCase() string {
	names := make([]string, 0, len(protectedInit))
	for n := range protectedInit {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, "|")
}
