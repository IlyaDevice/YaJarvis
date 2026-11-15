package main

import "testing"

func TestWakeDecisionUsesPeakAndKeepsContext(t *testing.T) {
	o := &OWW{seq: 2, ring: [][]float32{{1}, {2}}, buf: []float32{3}, thresholds: [10]float32{.4, .4, .4, .4, .4}}
	for i, p := range []float32{.2, .43, .82, .99, .87} {
		hit := o.detectProb(p)
		if hit != (i == 4) {
			t.Fatalf("step %d: hit=%v", i, hit)
		}
	}
	if o.LastWakeProb != .99 {
		t.Fatalf("decision must use peak, got %v", o.LastWakeProb)
	}
	o.Reset()
	if len(o.ring) != 2 || len(o.buf) != 1 || o.pending != 0 {
		t.Fatal("reset must keep the streaming context to hear the next call")
	}
	if o.detectProb(.99) {
		t.Fatal("the same word tail must not trigger twice")
	}
}
