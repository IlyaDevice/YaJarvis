package main

import "testing"

// Часы Мини 3: горит только средняя линия, цифры гаснут; громче голос — шире линия.
func TestClockLineFollowsVoice(t *testing.T) {
	l := &Leds{mode: LedListening}
	for _, seg := range []string{"a0", "b2", "dh", "dl", "degree", "dm"} {
		if v := l.clockShape(seg, 1); v != 0 {
			t.Fatalf("%s горит (%.2f) — должен погаснуть", seg, v)
		}
	}
	l.hear = 0
	if l.clockShape("g1", 1) <= 0 || l.clockShape("g0", 1) != 0 {
		t.Fatal("в тишине должна гореть короткая линия по центру")
	}
	l.hear = 1
	if l.clockShape("g0", 1) < 0.99 || l.clockShape("g3", 1) < 0.99 || l.clockShape("g2", 1) < 0.99 {
		t.Fatal("громкий голос — линия на всю ширину")
	}
	quiet := (&Leds{mode: LedListening, hear: 0.1}).clockShape("g1", 1)
	if loud := l.clockShape("g1", 1); loud <= quiet {
		t.Fatalf("громче голос — ярче линия: %.2f <= %.2f", loud, quiet)
	}
}
