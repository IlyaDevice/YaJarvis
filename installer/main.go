// Установка Джарвиса на Яндекс Станцию. Запускаете — открывается страница в браузере:
// программа сама находит колонку в сети, проверяет её и ставит/обновляет/удаляет Джарвиса по ADB.
// Нужна уже разлоченная колонка с ADB по сети (порт 5555) и правами root.
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

//go:embed ui payload
var files embed.FS

var version = "dev"

const remoteDir = "/data/opt/jarvisd"

type Speaker struct {
	IP        string `json:"ip"`
	Model     string `json:"model"`
	Arch      string `json:"arch"`     // arm64 | armv7
	Userland  string `json:"userland"` // armhf | arm64 — для vqe_tap.so (как у maind)
	Root      bool   `json:"root"`
	Android   bool   `json:"android"`
	Installed string `json:"installed"` // версия jarvisd или ""
	FreeMB    int    `json:"free_mb"`
	RAMMB     int    `json:"ram_mb"`
	OldJarvis bool   `json:"old_jarvis"` // серверный Джарвис (jarvis.sh)
	OldAlisa  bool   `json:"old_alisa"`  // Alisa Voice (rison) с собственным vqe_tap
	Problems  []string `json:"problems"`
	Notes     []string `json:"notes"`
}

type job struct {
	mu    sync.Mutex
	lines []string
	done  bool
	err   string
	url   string
}

func (j *job) log(format string, a ...any) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.lines = append(j.lines, fmt.Sprintf(format, a...))
}

var (
	jobMu sync.Mutex
	cur   *job
)

func main() {
	addr := "127.0.0.1:0"
	if v := os.Getenv("JARVIS_SETUP_ADDR"); v != "" { // для проверки: фиксированный порт
		addr = v
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatal(err)
	}
	ui, _ := fs.Sub(files, "ui")
	token := randToken()
	mux := http.NewServeMux()
	mux.Handle("GET /", http.FileServerFS(ui))
	api := func(path string, h http.HandlerFunc) {
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			// страница открыта только этой программой: чужие сайты не могут дёргать установщик
			if r.Header.Get("X-Token") != token {
				http.Error(w, "forbidden", 403)
				return
			}
			h(w, r)
		})
	}
	mux.HandleFunc("GET /token.js", func(w http.ResponseWriter, r *http.Request) {
		if host, _, _ := net.SplitHostPort(r.Host); host != "127.0.0.1" && host != "localhost" {
			http.Error(w, "forbidden", 403)
			return
		}
		w.Header().Set("Content-Type", "text/javascript")
		fmt.Fprintf(w, "window.TOKEN=%q;window.VERSION=%q;", token, version)
	})
	api("GET /api/scan", handleScan)
	api("POST /api/check", handleCheck)
	api("POST /api/install", handleInstall)
	api("POST /api/uninstall", handleUninstall)
	api("GET /api/job", handleJob)
	url := "http://" + ln.Addr().String() + "/"
	fmt.Println("Установка Джарвиса:", url)
	fmt.Println("Окно браузера откроется само. Не закрывайте это окно, пока идёт установка.")
	if os.Getenv("JARVIS_SETUP_NOBROWSER") == "" {
		openBrowser(url)
	}
	log.Fatal(http.Serve(ln, mux))
}

func randToken() string {
	b := make([]byte, 16)
	_, _ = cryptoRand(b)
	return fmt.Sprintf("%x", b)
}

func openBrowser(url string) {
	switch runtime.GOOS {
	case "windows":
		_ = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		_ = exec.Command("open", url).Start()
	default:
		_ = exec.Command("xdg-open", url).Start()
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// ---------------------------------------------------------------- поиск колонок

func localNets() []*net.IPNet {
	var out []*net.IPNet
	ifs, _ := net.Interfaces()
	for _, i := range ifs {
		if i.Flags&net.FlagUp == 0 || i.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, _ := i.Addrs()
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok || n.IP.To4() == nil || !n.IP.IsPrivate() {
				continue
			}
			ones, _ := n.Mask.Size()
			if ones < 22 { // большие сети целиком не сканируем — берём /24 вокруг себя
				n = &net.IPNet{IP: n.IP.Mask(net.CIDRMask(24, 32)), Mask: net.CIDRMask(24, 32)}
			}
			out = append(out, &net.IPNet{IP: n.IP.Mask(n.Mask), Mask: n.Mask})
		}
	}
	return out
}

func handleScan(w http.ResponseWriter, r *http.Request) {
	var mu sync.Mutex
	var found []string
	var wg sync.WaitGroup
	sem := make(chan struct{}, 128)
	for _, n := range localNets() {
		ip := n.IP.To4()
		ones, _ := n.Mask.Size()
		count := 1 << (32 - ones)
		base := uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
		for i := 1; i < count-1; i++ {
			v := base + uint32(i)
			addr := fmt.Sprintf("%d.%d.%d.%d", v>>24, v>>16&255, v>>8&255, v&255)
			wg.Add(1)
			sem <- struct{}{}
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				c, err := net.DialTimeout("tcp", addr+":5555", 600*time.Millisecond)
				if err != nil {
					return
				}
				c.Close()
				mu.Lock()
				found = append(found, addr)
				mu.Unlock()
			}()
		}
	}
	wg.Wait()
	sort.Strings(found)
	var res []Speaker
	for _, ip := range found {
		sp, err := inspect(ip)
		if err != nil {
			res = append(res, Speaker{IP: ip, Problems: []string{err.Error()}})
			continue
		}
		res = append(res, *sp)
	}
	writeJSON(w, 200, map[string]any{"speakers": res})
}

func handleCheck(w http.ResponseWriter, r *http.Request) {
	var req struct{ IP string }
	_ = json.NewDecoder(r.Body).Decode(&req)
	ip := strings.TrimSpace(req.IP)
	if net.ParseIP(ip) == nil {
		writeJSON(w, 400, map[string]any{"error": "Это не IP-адрес"})
		return
	}
	sp, err := inspect(ip)
	if err != nil {
		writeJSON(w, 400, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, sp)
}

// inspect — что за колонка и можно ли на неё ставить.
func inspect(ip string) (*Speaker, error) {
	a, err := DialADB(ip+":5555", 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("нет ADB на %s: %v", ip, err)
	}
	defer a.Close()
	sh := func(cmd string) string {
		out, _, _ := a.Shell(cmd, 15*time.Second)
		return strings.TrimSpace(out)
	}
	sp := &Speaker{IP: ip}
	sp.Root = sh("id -u") == "0"
	sp.Android = sh("getprop ro.build.version.sdk 2>/dev/null") != ""
	sp.Model = strings.Trim(sh("cat /proc/device-tree/model 2>/dev/null"), "\x00 ")
	if sp.Model == "" {
		sp.Model = sh("getprop ro.product.model 2>/dev/null")
	}
	switch m := sh("uname -m"); {
	case strings.Contains(m, "aarch64") || strings.Contains(m, "arm64"):
		sp.Arch = "arm64"
	case strings.HasPrefix(m, "arm"):
		sp.Arch = "armv7"
	default:
		sp.Problems = append(sp.Problems, "Неизвестный процессор: "+m)
	}
	// разрядность программ Алисы (vqe_tap.so должен совпадать с maind)
	switch sh("head -c 5 /system/vendor/quasar/maind 2>/dev/null | tail -c 1 | od -An -tu1") {
	case "1":
		sp.Userland = "armhf"
	case "2":
		sp.Userland = "arm64"
	}
	sp.Installed = sh(remoteDir + "/jarvisd version 2>/dev/null")
	if kb, err := strconv.Atoi(sh("df -k /data 2>/dev/null | awk 'NR==2{print $4}'")); err == nil {
		sp.FreeMB = kb / 1024
	}
	if kb, err := strconv.Atoi(sh("awk '/MemTotal/{print $2}' /proc/meminfo")); err == nil {
		sp.RAMMB = kb / 1024
	}
	sp.OldJarvis = sh("[ -f /data/opt/jarvis/enabled ] && echo y") == "y"
	sp.OldAlisa = sh("[ -f /etc/init.d/S99zz-alisa-voice ] && [ -d /data/opt/alisa-standalone ] && echo y") == "y"

	if sp.Android {
		sp.Problems = append(sp.Problems, "Это Станция на Android (Макс или Станция 1-го поколения) — она пока не поддерживается.")
	}
	if !sp.Root {
		sp.Problems = append(sp.Problems, "ADB без прав root. Нужна разлоченная колонка — см. hackalice.me.")
	}
	if sh("[ -x /system/vendor/quasar/maind ] && echo y") != "y" && !sp.Android {
		sp.Problems = append(sp.Problems, "Не найдена штатная программа Алисы (maind) — это точно Яндекс Станция?")
	}
	if sp.Userland == "" && !sp.Android {
		sp.Notes = append(sp.Notes, "Не удалось определить разрядность Алисы — микрофон может не подключиться.")
	}
	need := 22
	if sp.Installed != "" {
		need = 2 // обновление: старый файл заменяется
	}
	if sp.FreeMB > 0 && sp.FreeMB < need {
		sp.Problems = append(sp.Problems, fmt.Sprintf("Мало места: свободно %d МБ, нужно около %d МБ.", sp.FreeMB, need))
	}
	if sp.RAMMB > 0 && sp.RAMMB < 200 {
		sp.Notes = append(sp.Notes, fmt.Sprintf("Мало памяти (%d МБ) — Джарвис будет работать, но без запаса.", sp.RAMMB))
	}
	if sp.OldJarvis {
		sp.Notes = append(sp.Notes, "Найден серверный Джарвис (старая версия через свой сервер). При установке он будет выключен, файлы останутся для отката.")
	}
	if sp.OldAlisa {
		sp.Notes = append(sp.Notes, "Найден Alisa Voice (rison). Оба используют микрофон — при установке его автозапуск будет выключен.")
	}
	return sp, nil
}

// ---------------------------------------------------------------- установка

func handleJob(w http.ResponseWriter, r *http.Request) {
	jobMu.Lock()
	j := cur
	jobMu.Unlock()
	if j == nil {
		writeJSON(w, 200, map[string]any{"lines": []string{}, "done": true})
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	writeJSON(w, 200, map[string]any{"lines": j.lines, "done": j.done, "error": j.err, "url": j.url})
}

func startJob(fn func(j *job) error) error {
	jobMu.Lock()
	defer jobMu.Unlock()
	if cur != nil && !cur.done {
		return errors.New("уже идёт установка")
	}
	j := &job{}
	cur = j
	go func() {
		err := fn(j)
		j.mu.Lock()
		j.done = true
		if err != nil {
			j.err = err.Error()
			j.lines = append(j.lines, "Ошибка: "+err.Error())
		}
		j.mu.Unlock()
	}()
	return nil
}

func payload(name string) []byte {
	b, err := files.ReadFile("payload/" + name)
	if err != nil {
		panic("в установщике нет " + name)
	}
	return b
}

func handleInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IP, Password, OpenAI, Vless, Timezone string
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, 400, map[string]any{"error": "пустой запрос"})
		return
	}
	if req.Password != "" && len([]rune(req.Password)) < 6 {
		writeJSON(w, 400, map[string]any{"error": "Пароль — минимум 6 символов"})
		return
	}
	err := startJob(func(j *job) error { return install(j, req.IP, req.Password, req.OpenAI, req.Vless, req.Timezone) })
	if err != nil {
		writeJSON(w, 409, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func install(j *job, ip, password, openai, vless, tz string) error {
	j.log("Подключаюсь к колонке %s…", ip)
	sp, err := inspect(ip)
	if err != nil {
		return err
	}
	if len(sp.Problems) > 0 {
		return errors.New(strings.Join(sp.Problems, " "))
	}
	a, err := DialADB(ip+":5555", 5*time.Second)
	if err != nil {
		return err
	}
	defer a.Close()
	run := func(cmd string) (string, error) {
		out, rc, err := a.Shell(cmd, 60*time.Second)
		if err != nil {
			return out, err
		}
		if rc != 0 {
			return out, fmt.Errorf("команда на колонке не прошла (%d): %s", rc, strings.TrimSpace(out))
		}
		return out, nil
	}
	upgrade := sp.Installed != ""
	if upgrade {
		j.log("Найден Джарвис %s — обновляю, настройки сохраняются.", sp.Installed)
		_, _ = run(remoteDir + "/jarvisd.sh stop 2>/dev/null; true")
	}
	if _, err := run("mkdir -p " + remoteDir); err != nil {
		return err
	}
	bin := "jarvisd-" + sp.Arch
	tap := "vqe_tap-" + map[string]string{"arm64": "arm64", "armhf": "armhf", "": "armhf"}[sp.Userland] + ".so"
	push := func(name, remote string, mode uint32) error {
		data := payload(name)
		last := -1
		j.log("Копирую %s (%d КБ)…", name, len(data)/1024)
		return a.Push(data, remote, mode, func(done, total int) {
			if p := done * 100 / total; p/25 != last/25 && total > 1<<20 {
				last = p
				j.log("  %d%%", p)
			}
		})
	}
	if err := push(bin, remoteDir+"/jarvisd.new", 0o755); err != nil {
		return err
	}
	if err := push(tap, remoteDir+"/vqe_tap.so", 0o644); err != nil {
		return err
	}
	if err := push("jarvisd.sh", remoteDir+"/jarvisd.sh", 0o755); err != nil {
		return err
	}
	if sp.Userland != "arm64" { // распознаватель команд Станции — 32-битный, как maind; без него Джарвис просто без мгновенных команд
		_ = push("spotcmd", remoteDir+"/spotcmd", 0o755)
	}
	if wake, err := files.ReadFile("payload/wake.bin"); err == nil {
		j.log("Копирую модель слова «Джарвис»…")
		if err := a.Push(wake, remoteDir+"/wake.bin", 0o644, nil); err != nil {
			return err
		}
	}
	// новое слово на признаках openWakeWord: если обе части есть, jarvisd берёт его вместо wake.bin
	for _, name := range []string{"oww.bin", "wake_oww.bin"} {
		if data, err := files.ReadFile("payload/" + name); err == nil {
			if err := a.Push(data, remoteDir+"/"+name, 0o644, nil); err != nil {
				return err
			}
		}
	}
	if _, err := run("mv -f " + remoteDir + "/jarvisd.new " + remoteDir + "/jarvisd && " + remoteDir + "/jarvisd version"); err != nil {
		return fmt.Errorf("Джарвис не запускается на этой колонке: %v", err)
	}
	// начальные настройки (пароль сразу хэшем — открытым он на колонку не попадает)
	seed := map[string]string{}
	if password != "" {
		h, err := bcrypt.GenerateFromPassword([]byte(password), 10)
		if err != nil {
			return err
		}
		seed["password_hash"] = string(h)
	}
	if strings.TrimSpace(openai) != "" {
		seed["openai_key"] = strings.TrimSpace(openai)
	}
	if strings.TrimSpace(vless) != "" {
		seed["vless"] = strings.TrimSpace(vless)
	}
	if tz != "" {
		seed["timezone"] = tz
	}
	if len(seed) > 0 {
		b, _ := json.Marshal(seed)
		if err := a.Push(b, remoteDir+"/seed.json", 0o600, nil); err != nil {
			return err
		}
	}
	if sp.OldJarvis {
		j.log("Выключаю серверного Джарвиса (файлы остаются в /data/opt/jarvis)…")
		_, _ = run("rm -f /data/opt/jarvis/enabled; /data/opt/jarvis/jarvis.sh stop >/dev/null 2>&1; true")
	}
	if sp.OldAlisa {
		j.log("Выключаю автозапуск Alisa Voice…")
		_, _ = run("mount -o remount,rw / && mv -f /etc/init.d/S99zz-alisa-voice /data/opt/alisa-standalone/S99zz-alisa-voice.off-jarvisd; mount -o remount,ro /; true")
	}
	j.log("Добавляю автозапуск…")
	if err := a.Push(payload("S98jarvisd"), "/tmp/S98jarvisd", 0o755, nil); err != nil {
		return err
	}
	if _, err := run("mount -o remount,rw / && cp /tmp/S98jarvisd /etc/init.d/S98jarvisd && chmod 755 /etc/init.d/S98jarvisd; rc=$?; mount -o remount,ro / 2>/dev/null; rm -f /tmp/S98jarvisd " + remoteDir + "/disabled " + remoteDir + "/no-mic; (exit $rc)"); err != nil {
		return err
	}
	_, _ = run("sync")
	j.log("Перезагружаю колонку — Алиса загрузится уже с микрофоном для Джарвиса (около минуты)…")
	_, _, _ = a.Shell("sync; reboot", 5*time.Second) // связь оборвётся — это нормально
	a.Close()
	url := "http://" + ip + ":8080/"
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	time.Sleep(15 * time.Second)
	for ctx.Err() == nil {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				j.log("Готово! Джарвис запущен.")
				j.mu.Lock()
				j.url = url
				j.mu.Unlock()
				return nil
			}
		}
		time.Sleep(3 * time.Second)
	}
	return errors.New("колонка перезагрузилась, но настройки Джарвиса не открылись за 4 минуты — проверьте, что она в сети")
}

func handleUninstall(w http.ResponseWriter, r *http.Request) {
	var req struct{ IP string }
	_ = json.NewDecoder(r.Body).Decode(&req)
	err := startJob(func(j *job) error {
		j.log("Подключаюсь к %s…", req.IP)
		a, err := DialADB(req.IP+":5555", 5*time.Second)
		if err != nil {
			return err
		}
		defer a.Close()
		out, _, _ := a.Shell("[ -x "+remoteDir+"/jarvisd.sh ] && echo y", 10*time.Second)
		if strings.TrimSpace(out) != "y" {
			return errors.New("Джарвис на этой колонке не установлен")
		}
		j.log("Удаляю Джарвиса и перезагружаю колонку — Алиса вернётся как была.")
		_, _, _ = a.Shell(remoteDir+"/jarvisd.sh uninstall", 30*time.Second) // в конце перезагрузка — связь оборвётся
		j.log("Готово.")
		return nil
	})
	if err != nil {
		writeJSON(w, 409, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

