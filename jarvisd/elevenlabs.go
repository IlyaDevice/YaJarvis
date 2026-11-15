package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const elevenBase = "https://api.elevenlabs.io"
const elevenRate = 24000
const elevenDefaultModel = "eleven_flash_v2_5"

var elevenTTSModels = []string{"eleven_v4_turbo", "eleven_v4", "eleven_v3_conversational", "eleven_v3", "eleven_flash_v2_5", "eleven_multilingual_v2"}

type ElevenVoice struct {
	ID       string            `json:"voice_id"`
	Name     string            `json:"name"`
	Category string            `json:"category"`
	Labels   map[string]string `json:"labels"`
}

func elevenGet(ctx context.Context, client *http.Client, key, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, elevenBase+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("xi-api-key", key)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return elevenError(resp, key)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}
func elevenError(resp *http.Response, key string) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var out struct {
		Detail any `json:"detail"`
	}
	_ = json.Unmarshal(b, &out)
	msg, status := "", ""
	if d, ok := out.Detail.(map[string]any); ok {
		msg, status = str(d["message"]), str(d["status"])
	} else if s, ok := out.Detail.(string); ok {
		msg = s
	}
	if resp.StatusCode == http.StatusPaymentRequired || ((resp.StatusCode == 401 || resp.StatusCode == 403 || resp.StatusCode == 429) &&
		(quotaText(status) || quotaText(msg))) {
		why := status
		if why == "" {
			why = fmt.Sprintf("ответ %d", resp.StatusCode)
		}
		elevenQuota(key, why)
	}
	if len(msg) > 350 {
		msg = msg[:350]
	}
	if key != "" {
		msg = strings.ReplaceAll(msg, key, "[ключ скрыт]")
	}
	return fmt.Errorf("ElevenLabs ответил %d: %s", resp.StatusCode, msg)
}
func elevenVoices(ctx context.Context, client *http.Client, key string) ([]ElevenVoice, error) {
	if key == "" {
		return nil, errors.New("добавь ключ ElevenLabs в подключениях")
	}
	var out []ElevenVoice
	next := ""
	for page := 0; page < 4; page++ {
		path := "/v2/voices?page_size=100"
		if next != "" {
			path += "&next_page_token=" + url.QueryEscape(next)
		}
		var r struct {
			Voices  []ElevenVoice `json:"voices"`
			HasMore bool          `json:"has_more"`
			Next    string        `json:"next_page_token"`
		}
		if err := elevenGet(ctx, client, key, path, &r); err != nil {
			return nil, err
		}
		out = append(out, r.Voices...)
		if !r.HasMore || r.Next == "" {
			break
		}
		next = r.Next
	}
	return out, nil
}
func elevenSpeak(ctx context.Context, client *http.Client, key string, p Profile, text string, emit func([]int16) error) error {
	if p.TTSVoice == "" {
		return errors.New("выбери голос ElevenLabs в настройках ассистента")
	}
	model := p.TTSModel
	if model == "" {
		model = elevenDefaultModel
	}
	if model == "eleven_v4_turbo" || model == "eleven_v4" || model == "eleven_v3_conversational" {
		p.TTSModel = model
		return elevenDialogueSpeak(ctx, client, key, p, text, emit)
	}
	body := map[string]any{"text": text, "model_id": model}
	if model != "eleven_multilingual_v2" {
		body["language_code"] = "ru"
	}
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, elevenBase+"/v1/text-to-speech/"+url.PathEscape(p.TTSVoice)+"/stream?output_format=pcm_24000", bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("xi-api-key", key)
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return elevenError(resp, key)
	}
	const frame = elevenRate / 10
	buf := make([]byte, frame*2)
	total := 0
	for {
		n, err := io.ReadFull(resp.Body, buf)
		if n%2 != 0 {
			return errors.New("ElevenLabs прислал неполный PCM-сэмпл")
		}
		if n > 0 {
			total += n
			if total > elevenRate*2*120 {
				return errors.New("ответ ElevenLabs слишком длинный")
			}
			pcm := make([]int16, n/2)
			for i := range pcm {
				pcm[i] = int16(binary.LittleEndian.Uint16(buf[2*i:]))
			}
			if e := emit(pcm); e != nil {
				return e
			}
		}
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			if total == 0 {
				return errors.New("ElevenLabs вернул пустой звук")
			}
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func isClassic(p Profile) bool {
	return p.Engine != "eleven_realtime" && (p.Mode == "classic" || p.Engine == "elevenlabs")
}
func classicDefaults(p *Profile) {
	p.Mode, p.Engine, p.STTEngine = "classic", "elevenlabs", "elevenlabs"
	if p.LLMModel == "" {
		p.LLMModel = p.BackendModel
	}
	if p.LLMModel == "" {
		p.LLMModel = BackendModels[0]
	}
	p.BackendModel = p.LLMModel
	if p.TTSModel == "" {
		p.TTSModel = elevenDefaultModel
	}
}
