package main

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"github.com/coder/websocket"
	"net/http"
	"net/url"
)

func elevenDialogueSpeak(ctx context.Context, client *http.Client, key string, p Profile, text string, emit func([]int16) error) error {
	q := url.Values{"model_id": {p.TTSModel}, "output_format": {"pcm_24000"}}
	ws, resp, err := websocket.Dial(ctx, "wss://api.elevenlabs.io/v1/text-to-dialogue/stream-input?"+q.Encode(), &websocket.DialOptions{HTTPClient: client, HTTPHeader: http.Header{"xi-api-key": {key}}})
	if err != nil {
		if resp != nil {
			return fmt.Errorf("ElevenLabs Dialogue: HTTP %d", resp.StatusCode)
		}
		return err
	}
	defer ws.CloseNow()
	ws.SetReadLimit(8 << 20)
	for _, v := range []any{map[string]any{"voices": []string{p.TTSVoice}}, map[string]any{"inputs": []any{map[string]any{"text": text, "voice_id": p.TTSVoice, "new_turn": false}}}, map[string]any{"close_socket": true}} {
		b, _ := json.Marshal(v)
		if err = ws.Write(ctx, websocket.MessageText, b); err != nil {
			return err
		}
	}
	total := 0
	for {
		_, raw, err := ws.Read(ctx)
		if err != nil {
			return err
		}
		var event struct {
			Audio   string `json:"audio"`
			Final   bool   `json:"is_final"`
			Error   any    `json:"error"`
			Message string `json:"message"`
		}
		if err = json.Unmarshal(raw, &event); err != nil {
			return err
		}
		if event.Error != nil {
			return fmt.Errorf("ElevenLabs Dialogue: %v %s", event.Error, event.Message)
		}
		if event.Audio != "" {
			b, err := base64.StdEncoding.DecodeString(event.Audio)
			if err != nil {
				return err
			}
			if len(b)%2 != 0 {
				return fmt.Errorf("неполный PCM ElevenLabs")
			}
			total += len(b)
			if total > elevenRate*2*120 {
				return fmt.Errorf("слишком длинный ответ ElevenLabs")
			}
			for i := 0; i < len(b); i += elevenRate / 10 * 2 {
				part := b[i:min(i+elevenRate/10*2, len(b))]
				pcm := make([]int16, len(part)/2)
				for j := range pcm {
					pcm[j] = int16(binary.LittleEndian.Uint16(part[2*j:]))
				}
				if err = emit(pcm); err != nil {
					return err
				}
			}
		}
		if event.Final {
			if total == 0 {
				return fmt.Errorf("ElevenLabs Dialogue не вернул звук")
			}
			return nil
		}
	}
}
