package main

import "testing"

func TestParseRSSI(t *testing.T) {
	if v := parseRSSI("RSSI=-52\nLINKSPEED=72\nNOISE=9999\n"); v != -52 {
		t.Fatalf("RSSI=-52 → %d", v)
	}
	if v := parseRSSI("FAIL\n"); v != 0 {
		t.Fatalf("FAIL → %d", v)
	}
}
