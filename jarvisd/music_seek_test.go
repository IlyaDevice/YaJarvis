package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func seekFixture() []byte {
	var data []byte
	for sec := 0; sec < 180; sec++ {
		ms := sec * 1000
		// Cluster, Timecode (3 bytes), SimpleBlock (track, relative time, flags, fake frame).
		data = append(data, 0x1f, 0x43, 0xb6, 0x75, 0xff, 0xe7, 0x83, byte(ms>>16), byte(ms>>8), byte(ms))
		data = append(data, 0xa3, 0x48, 0x04, 0x81, 0, 0, 0)
		data = append(data, make([]byte, 2048)...)
	}
	return data
}

func TestMusicSeekFrames(t *testing.T) {
	data := seekFixture()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var start, end int
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &start, &end); err != nil || start >= len(data) {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		end = min(end, len(data)-1)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, end, len(data)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(data[start : end+1])
	}))
	defer srv.Close()
	m := NewMusic(func() *http.Client { return srv.Client() }, func() float64 { return 1 })
	st := &ytStream{url: srv.URL, length: int64(len(data)), dur: 180}
	for _, target := range []int64{120000, 15000} {
		src, w, err := m.openAudio(context.Background(), "test", st, float64(target)/1000)
		if err != nil {
			t.Fatal(err)
		}
		f := &musicFrames{src: src, w: w, target: target}
		for {
			_, ms, err := f.next()
			if err != nil {
				t.Fatal(err)
			}
			if ms >= target {
				if ms != target {
					t.Fatalf("seek %d landed at %d", target, ms)
				}
				break
			}
		}
		_ = src.Close()
	}
	// A variable bitrate estimate can land after the target: restart from byte zero.
	src := &ytReader{ctx: context.Background(), client: srv.Client(), url: srv.URL, pos: 100}
	w := &webm{r: bufio.NewReader(bytes.NewReader(data[150*(len(data)/180):]))}
	f := &musicFrames{src: src, w: w, target: 120000}
	_, ms, err := f.next()
	if err != nil || ms != 0 {
		t.Fatalf("overshoot fallback: ms=%d err=%v", ms, err)
	}
	_ = src.Close()
}
