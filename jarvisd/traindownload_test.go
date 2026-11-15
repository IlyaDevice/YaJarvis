package main

import (
	"archive/zip"
	"bytes"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// архив для дообучения: все записи (с колонки и с Диска), скрипт и модель
func TestDownloadTrainArchive(t *testing.T) {
	dir := t.TempDir()
	st := &Store{s: defaultSettings(), path: filepath.Join(dir, "settings.json")}
	st.s.TrainMode, st.s.TrainLimitMB = true, 1
	tr := &Trainer{dataDir: dir, store: st, cloud: newFakeCloud()}
	pcm := make([]int16, 16000*2)
	for i := 0; i < 20; i++ {
		tr.Save("near", pcm, fmt.Sprintf("n%02d", i))
		time.Sleep(15 * time.Millisecond)
	}
	deadline := time.Now().Add(3 * time.Second)
	for off, _ := tr.remoteState(); off || len(tr.remoteClips()) == 0; off, _ = tr.remoteState() {
		if time.Now().After(deadline) {
			t.Fatal("записи не ушли на Диск")
		}
		time.Sleep(20 * time.Millisecond)
	}
	_ = os.WriteFile(filepath.Join(dir, "wake.bin"), []byte("JKWS1test"), 0o644)
	w := &Web{dataDir: dir, engine: &Engine{train: tr}}
	rec := httptest.NewRecorder()
	w.downloadTrain(rec, httptest.NewRequest("GET", "/api/train/download", nil))
	z, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	wavs, remote, extra := 0, len(tr.remoteClips()), map[string]bool{}
	for _, f := range z.File {
		if strings.HasSuffix(f.Name, ".wav") {
			wavs++
			if f.UncompressedSize64 != uint64(44+2*len(pcm)) {
				t.Fatalf("%s: %d байт", f.Name, f.UncompressedSize64)
			}
		} else {
			extra[f.Name] = true
		}
	}
	if remote == 0 || wavs != 20 || !extra["finetune.py"] || !extra["wake.bin"] || !extra["README.txt"] {
		t.Fatalf("в архиве %d записей (на Диске %d) и %v", wavs, remote, extra)
	}
}
