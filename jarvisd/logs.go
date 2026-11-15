package main

// Страница «Логи»: последние строки журналов Джарвиса и Алисы (её служб maind/mediad) с фильтром.

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

var logSources = map[string]string{
	"jarvis": "/tmp/jarvisd/jarvisd.log",
	"alice":  "/data/quasar/daemons_logs/maind.log",
	"media":  "/data/quasar/daemons_logs/mediad.log",
}

// tailLines — последние n строк файла: читаем с конца только окно window байт (не больше 1 МБ).
func tailLines(path string, n int, window int64) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	size := st.Size()
	off := max(0, size-min(window, 1<<20))
	if _, err := f.Seek(off, io.SeekStart); err != nil {
		return nil, err
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if off > 0 && len(lines) > 0 {
		lines = lines[1:] // первая строка обрезана
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines, nil
}

func (w *Web) getLogs(rw http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	src := q.Get("src")
	path, ok := logSources[src]
	if !ok {
		src, path = "jarvis", logSources["jarvis"]
	}
	if src == "jarvis" && w.dataDir != "" { // локальная копия для разработки пишет рядом с данными
		if _, err := os.Stat(path); err != nil {
			path = filepath.Join(w.dataDir, "jarvisd.log")
		}
	}
	n, _ := strconv.Atoi(q.Get("n"))
	if n <= 0 || n > 2000 {
		n = 400
	}
	filter := strings.ToLower(strings.TrimSpace(q.Get("q")))
	// без поиска хватает хвоста ~300 байт на строку: панель опрашивает лог каждые пару секунд,
	// и читать каждый раз мегабайт — лишняя работа процессору и сборщику мусора
	window := int64(1 << 20)
	if filter == "" {
		window = min(window, max(64<<10, int64(n)*300))
	}
	lines, err := tailLines(path, 5000, window)
	if err != nil {
		writeJSON(rw, 200, map[string]any{"src": src, "lines": []string{}, "error": "журнала пока нет"})
		return
	}
	out := make([]string, 0, n)
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		l := lines[i]
		if filter != "" && !strings.Contains(strings.ToLower(l), filter) {
			continue
		}
		if len(l) > 1200 {
			l = l[:1200] + "…"
		}
		out = append(out, l)
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	writeJSON(rw, 200, map[string]any{"src": src, "lines": out})
}
