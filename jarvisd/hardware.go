package main

import (
	"os"
	"regexp"
	"sync"

	"github.com/newfpv/YaJavis/hardware"
)

var quasarTypeRe = regexp.MustCompile(`"deviceType"\s*:\s*"([^"]+)"`)

var stationModel = sync.OnceValue(func() hardware.Model {
	read := func(path string) string { b, _ := os.ReadFile(path); return string(b) }
	// тип устройства Алисы (quasar.cfg: "deviceType") — запасной признак, если дерево устройств ничего не говорит
	product := ""
	if m := quasarTypeRe.FindStringSubmatch(read("/system/vendor/quasar/quasar.cfg")); m != nil {
		product = m[1]
	}
	return hardware.Detect(read("/proc/device-tree/compatible"), read("/proc/device-tree/amlogic-dt-id"), product, read("/proc/device-tree/model"))
})
