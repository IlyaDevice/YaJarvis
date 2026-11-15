package main

import (
	"os"
	"sync"

	"github.com/newfpv/YaJavis/hardware"
)

var stationModel = sync.OnceValue(func() hardware.Model {
	read := func(path string) string { b, _ := os.ReadFile(path); return string(b) }
	return hardware.Detect(read("/proc/device-tree/compatible"), read("/proc/device-tree/amlogic-dt-id"), "", read("/proc/device-tree/model"))
})
