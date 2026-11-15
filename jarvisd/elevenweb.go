package main

import (
	"context"
	"net/http"
	"time"
)

func (w *Web) elevenVoices(rw http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	voices, err := elevenVoices(ctx, w.proxy.ClientFor("elevenlabs", 25*time.Second), w.store.Get().ElevenLabsKey)
	if err != nil {
		bad(rw, shortErr(err))
		return
	}
	writeJSON(rw, 200, map[string]any{"voices": voices, "models": elevenTTSModels})
}
