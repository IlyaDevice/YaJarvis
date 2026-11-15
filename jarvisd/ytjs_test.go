package main

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
)

// Расшифровка на сохранённом плеере: JARVIS_PLAYER=путь/base.js go test -run TestYtJSLocal -v
func TestYtJSLocal(t *testing.T) {
	path := os.Getenv("JARVIS_PLAYER")
	if path == "" {
		t.Skip("нужен JARVIS_PLAYER")
	}
	src, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t0 := time.Now()
	code, err := ytPreprocess(string(src))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("подготовка %.2f с, кусков %d", time.Since(t0).Seconds(), len(code))
	_ = os.WriteFile(path+".pre.js", []byte(strings.Join(code, "\n//----\n")), 0o644)
	code = nil
	runtime.GC()
	t1 := time.Now()
	j := &ytJS{}
	j.vm, j.solve, err = ytBuildSolver(string(src))
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("выполнение %.2f с, кандидатов %d", time.Since(t1).Seconds(), len(j.solve))
	var mst runtime.MemStats
	runtime.ReadMemStats(&mst)
	t.Logf("память: куча %d МБ, взято у системы %d МБ", mst.HeapInuse>>20, mst.Sys>>20)
	t2 := time.Now()
	s, n, err := j.decipher("0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefghijklmnopqrstuvwxyzABCDEFGH", "dJLHV9W_nG4lgpFc")
	t.Logf("sig %q n %q err %v за %v", s, n, err, time.Since(t2))
	if err != nil {
		t.Fatal(err)
	}
	if s != "O56789ab3defghijklmnopqrstuvwxyzABCDEFGHIJKLMN4PQRSTUVWXYZ0123456782abcdefghijklmnopqrstuvwxyzABCDEFGH" || n != "OlP5W7gaeaS45" {
		t.Error("расшифровка не совпала с эталоном (плеер fad7e4e9)")
	}
}
