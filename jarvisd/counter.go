package main

// Счётчик установок без своего сервера. В репозитории есть служебный релиз «stats» с крошечными
// файлами; GitHub сам считает скачивания каждого файла. Колонка скачивает:
//   - new-<модель>         один раз за всю жизнь (сколько колонок поставили Джарвиса);
//   - v<версия>-<модель>   один раз на каждую версию (сколько колонок дошло до этой версии).
// Никаких идентификаторов и данных о человеке — только модель и версия в имени файла.
// Выключается в панели («Безопасность» → обновления) — настройка no_stats.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// counterModels — модели, для которых в релизе stats есть файлы (как в hardware.Detect). Остальное — other.
var counterModels = map[string]bool{"lite": true, "lite2": true, "mini1": true, "mini2": true, "mini3": true, "mini3pro": true,
	"midi": true, "station2": true, "station3": true, "street": true, "fiero": true}

func counterModel(id string) string {
	if counterModels[id] {
		return id
	}
	return "other"
}

type counterState struct {
	New bool   `json:"new"` // «новая колонка» уже отмечена
	Ver string `json:"ver"` // версия, которую уже отметили
}

// RunCounter — отметиться в счётчике (через 3 минуты после старта, повтор раз в 6 часов при ошибке сети).
func RunCounter(store *Store, client func() *http.Client, dataDir string) {
	if version == "dev" || version == "" {
		return
	}
	path := counterPath(dataDir)
	time.Sleep(3 * time.Minute)
	for {
		if store.Get().NoStats {
			return
		}
		var st counterState
		if b, err := os.ReadFile(path); err == nil {
			_ = json.Unmarshal(b, &st)
		}
		model := counterModel(stationModel().ID)
		failed := false
		ping := func(name string) bool { // 404 — файла ещё нет (сборка не из релиза): попробуем при следующем запуске
			ok, err := counterPing(client(), defaultRepo, name)
			if err != nil {
				log.Printf("счётчик установок: %v", err)
				failed = true
			}
			return ok
		}
		if !st.New && ping("new-"+model) {
			st.New = true
		}
		if st.Ver != version && !failed && ping("v"+version+"-"+model) {
			st.Ver = version
		}
		if b, err := json.Marshal(st); err == nil {
			_ = os.WriteFile(path, b, 0o600)
		}
		if !failed {
			return
		}
		time.Sleep(6 * time.Hour)
	}
}

// counterPath — отметка лежит вне папки Джарвиса: «Удалить Джарвиса» стирает /data/opt/jarvisd,
// а повторная установка не должна посчитать ту же колонку второй раз. Пропадает только при сбросе колонки.
func counterPath(dataDir string) string {
	if dataDir == "/data/opt/jarvisd" {
		return "/data/.yajavis-counter"
	}
	return filepath.Join(dataDir, "counter.json") // локальная копия для разработки
}

// counterPing скачивает файл счётчика (GitHub прибавляет ему скачивание). false — файла нет.
func counterPing(c *http.Client, repo, name string) (bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://github.com/"+repo+"/releases/download/stats/"+name, nil)
	req.Header.Set("User-Agent", "jarvisd/"+version)
	resp, err := c.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	switch {
	case resp.StatusCode == 200:
		return true, nil
	case resp.StatusCode == 404:
		return false, nil
	}
	return false, fmt.Errorf("GitHub ответил %d", resp.StatusCode)
}
