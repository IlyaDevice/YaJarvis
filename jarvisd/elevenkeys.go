package main

// Несколько ключей ElevenLabs: работает первый, у которого есть квота. Кончилась квота — ключ откладывается
// до сброса лимита (дату спрашиваем у ElevenLabs, иначе на 30 дней), дальше говорит следующий.

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"
)

type ElevenKey struct {
	Key   string `json:"key"`
	Until int64  `json:"until,omitempty"` // unix: до этого времени ключ не трогаем (кончилась квота)
	Why   string `json:"why,omitempty"`
}

func (k ElevenKey) resting(now time.Time) bool { return k.Until != 0 && now.Unix() < k.Until }

// pickEleven — ElevenLabsKey = первый ключ с квотой. Если отдыхают все — тот, что освободится раньше:
// вдруг квоту докупили, а ошибка ElevenLabs хотя бы честно скажет, в чём дело.
func (s *Settings) pickEleven() {
	if len(s.ElevenKeys) == 0 {
		s.ElevenLabsKey = ""
		return
	}
	now := time.Now()
	best := 0
	for i, k := range s.ElevenKeys {
		if !k.resting(now) {
			s.ElevenLabsKey = k.Key
			return
		}
		if k.Until < s.ElevenKeys[best].Until {
			best = i
		}
	}
	s.ElevenLabsKey = s.ElevenKeys[best].Key
}

// addElevenKey — новый ключ в конец списка (тот же ключ второй раз не добавляется).
func (s *Settings) addElevenKey(key string) {
	for _, k := range s.ElevenKeys {
		if k.Key == key {
			return
		}
	}
	s.ElevenKeys = append(s.ElevenKeys, ElevenKey{Key: key})
	s.pickEleven()
}

func keyTail(k string) string {
	if len(k) <= 8 {
		return "…"
	}
	return "…" + k[len(k)-4:]
}

// elevenQuotaHook — main: отложить ключ, у которого кончилась квота.
var elevenQuotaHook func(key, why string)

func elevenQuota(key, why string) {
	if elevenQuotaHook != nil && key != "" {
		go elevenQuotaHook(key, why)
	}
}

// quotaText — ответ ElevenLabs говорит, что кончились лимит или деньги (а не что ключ неверный).
func quotaText(s string) bool {
	s = strings.ToLower(s)
	for _, w := range []string{"quota", "credit", "exceeds your", "character limit", "unusual activity", "payment"} {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

// elevenReset — когда у ключа сбросится лимит (начало нового расчётного периода).
func elevenReset(client *http.Client, key string) time.Time {
	fallback := time.Now().AddDate(0, 0, 30)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, elevenBase+"/v1/user/subscription", nil)
	req.Header.Set("xi-api-key", key)
	resp, err := client.Do(req)
	if err != nil {
		return fallback
	}
	defer resp.Body.Close()
	var sub struct {
		Reset int64 `json:"next_character_count_reset_unix"`
	}
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&sub) != nil || sub.Reset <= time.Now().Unix() {
		return fallback
	}
	return time.Unix(sub.Reset, 0)
}

// restElevenKey — отложить ключ до сброса квоты и переключиться на следующий.
func restElevenKey(store *Store, client *http.Client, key, why string) {
	s := store.Get()
	known := false
	for _, k := range s.ElevenKeys {
		if k.Key == key && !k.resting(time.Now()) {
			known = true
		}
	}
	if !known {
		return
	}
	until := elevenReset(client, key)
	var next string
	_ = store.Update(func(s *Settings) error {
		for i := range s.ElevenKeys {
			if s.ElevenKeys[i].Key == key {
				s.ElevenKeys[i].Until, s.ElevenKeys[i].Why = until.Unix(), why
			}
		}
		s.pickEleven()
		next = s.ElevenLabsKey
		return nil
	})
	if next == key {
		log.Printf("ElevenLabs: квота ключа %s кончилась (%s), до %s; других ключей с квотой нет",
			keyTail(key), why, until.Format("02.01"))
		return
	}
	log.Printf("ElevenLabs: квота ключа %s кончилась (%s) — не трогаю до %s, дальше говорит ключ %s",
		keyTail(key), why, until.Format("02.01"), keyTail(next))
}

// watchElevenKeys — у отложенного ключа наступила дата сброса: он снова первый в очереди.
func watchElevenKeys(store *Store) {
	for {
		time.Sleep(10 * time.Minute)
		s := store.Get()
		cur := s.ElevenLabsKey
		s.pickEleven()
		if s.ElevenLabsKey != cur {
			_ = store.Update(func(s *Settings) error { s.pickEleven(); return nil })
			log.Printf("ElevenLabs: ключ %s снова с квотой — говорю им", keyTail(s.ElevenLabsKey))
		}
	}
}

// ElevenKeysState — для панели: ключи без самих ключей.
func (s *Settings) ElevenKeysState() []map[string]any {
	out := []map[string]any{}
	now := time.Now()
	for _, k := range s.ElevenKeys {
		m := map[string]any{"tail": keyTail(k.Key), "active": k.Key == s.ElevenLabsKey}
		if k.resting(now) {
			m["until"], m["why"] = k.Until, k.Why
		}
		out = append(out, m)
	}
	return out
}
