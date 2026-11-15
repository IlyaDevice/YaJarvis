package main

import (
	"encoding/binary"
	"math"
	"os"
	"testing"
)

func streamOWW(t testing.TB) *OWW {
	t.Helper()
	feat, err := os.ReadFile("../models/oww.bin")
	if err != nil {
		t.Fatal(err)
	}
	head, err := os.ReadFile("../models/wake_oww.bin")
	if err != nil {
		t.Fatal(err)
	}
	o, err := LoadOWW(feat, head, func() int { return 5 })
	if err != nil {
		t.Fatal(err)
	}
	return o
}

// Fixed input and pre-optimization embeddings catch history-buffer aliasing.
func TestOWWStreamGolden(t *testing.T) {
	o := streamOWW(t)
	var values []float32
	pcm := make([]int16, owwStep)
	seed := uint32(17)
	for step := 0; step < 80; step++ {
		for i := range pcm {
			seed = 1664525*seed + 1013904223
			pcm[i] = int16(seed >> 16)
		}
		o.Feed(pcm)
		if len(o.ring) > 0 {
			values = append(values, o.ring[len(o.ring)-1]...)
			values = append(values, o.LastProb)
		}
	}
	path := "testdata/oww-stream.bin"
	if os.Getenv("UPDATE_OWW_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0755); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err = binary.Write(f, binary.LittleEndian, values); err != nil {
			t.Fatal(err)
		}
		return
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	want := make([]float32, len(values))
	if err = binary.Read(f, binary.LittleEndian, want); err != nil {
		t.Fatal(err)
	}
	for i, v := range values {
		if math.IsNaN(float64(v)) || math.Abs(float64(v-want[i])) > 0.001 {
			t.Fatalf("embedding/probability %d: got %g want %g", i, v, want[i])
		}
	}
}

func BenchmarkOWWFeed(b *testing.B) {
	o := streamOWW(b)
	pcm := make([]int16, owwStep)
	for i := 0; i < 80; i++ {
		o.Feed(pcm)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		o.Feed(pcm)
	}
}

func TestOWWSteadyStateAllocations(t *testing.T) {
	o := streamOWW(t)
	pcm := make([]int16, owwStep)
	for i := 0; i < 80; i++ {
		o.Feed(pcm)
	}
	if n := testing.AllocsPerRun(20, func() { o.Feed(pcm) }); n != 0 {
		t.Fatalf("steady-state allocations per 80 ms: %g", n)
	}
}
