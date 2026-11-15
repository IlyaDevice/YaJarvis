package main

import "testing"

func TestCurrentTrigger(t *testing.T) {
	for raw, want := range map[string]string{
		"none timer [led_ring_fb] heartbeat\n": "led_ring_fb",
		"[none] timer heartbeat":                "none",
		"timer heartbeat":                       "",
	} {
		if got := currentTrigger(raw); got != want {
			t.Fatalf("%q: %q, want %q", raw, got, want)
		}
	}
}
