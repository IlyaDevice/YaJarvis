package main

import "testing"

func TestAliceMissesTap(t *testing.T) {
	mounted := "/dev/root / squashfs ro 0 0\ntmpfs /system/vendor/quasar/launch.json tmpfs rw 0 0\n"
	if !aliceMissesTap(mounted, []string{"7f00 r-xp /system/vendor/quasar/libs/libc.so"}) {
		t.Fatal("Алиса без tap при подложенном профиле — надо перезапустить")
	}
	if aliceMissesTap(mounted, []string{"7f00 r-xp /data/opt/jarvisd/vqe_tap.so"}) {
		t.Fatal("tap уже загружен — перезапуск не нужен")
	}
	if aliceMissesTap("/dev/root / squashfs ro 0 0\n", []string{"libc.so"}) {
		t.Fatal("профиль не подложен (без микрофона) — не трогаем Алису")
	}
	if aliceMissesTap(mounted, nil) {
		t.Fatal("Алиса не запущена — не трогаем")
	}
}
