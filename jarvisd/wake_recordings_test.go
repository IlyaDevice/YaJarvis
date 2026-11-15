package main

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestWakeRecentRecordings(t *testing.T) {
	feat, e1 := os.ReadFile("../models/oww.bin")
	head, e2 := os.ReadFile("../models/wake_oww.bin")
	if e1 != nil || e2 != nil {
		t.Skip("local weights unavailable")
	}
	for _, name := range []string{"20261114-125839.wav", "20261114-125858.wav", "20261114-125926.wav"} {
		raw, err := os.ReadFile(filepath.Join("testdata/private", name))
		if err != nil {
			t.Skip("local recordings unavailable")
		}
		if len(raw) < 44 {
			t.Fatal("invalid recording")
		}
		pcm := make([]int16, (len(raw)-44)/2+8000)
		for i := 0; i < (len(raw)-44)/2; i++ {
			pcm[i] = int16(binary.LittleEndian.Uint16(raw[44+i*2:]))
		}
		o, err := LoadOWW(feat, head, func() int { return 8 })
		if err != nil {
			t.Fatal(err)
		}
		hit, confidence := false, float32(0)
		for i := 0; i+160 <= len(pcm); i += 160 {
			if o.Feed(pcm[i : i+160]) {
				hit, confidence = true, max(confidence, o.LastWakeProb)
			}
		}
		t.Logf("%s: detected=%v peak=%.4f", name, hit, confidence)
		if !hit || (name != "20261114-125926.wav" && confidence < .97) {
			t.Fatal("missed a confirmed call")
		}
	}
}
