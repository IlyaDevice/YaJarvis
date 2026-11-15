package main

import (
	"encoding/json"
	"strconv"
	"strings"
)

// The microphone library must match maind's ELF machine, not the kernel's ABI.
func elfUserland(dump string) string {
	fields := strings.Fields(dump)
	if len(fields) < 20 {
		return ""
	}
	b := make([]byte, 20)
	for i := range b {
		n, err := strconv.Atoi(fields[i])
		if err != nil || n < 0 || n > 255 {
			return ""
		}
		b[i] = byte(n)
	}
	if string(b[:4]) != "\x7fELF" || b[5] != 1 {
		return ""
	}
	machine := int(b[18]) | int(b[19])<<8
	if b[4] == 1 && machine == 40 {
		return "armhf"
	}
	if b[4] == 2 && machine == 183 {
		return "arm64"
	}
	return ""
}

func supportsLaunch(raw string) bool {
	var doc any
	if json.Unmarshal([]byte(raw), &doc) != nil {
		return false
	}
	var walk func(any) bool
	walk = func(value any) bool {
		switch v := value.(type) {
		case map[string]any:
			path, _ := v["path"].(string)
			args, _ := v["arg"].([]any)
			if strings.HasSuffix(path, "/maind") {
				for _, arg := range args {
					if arg == "--platformd" {
						return true
					}
				}
			}
			for _, child := range v {
				if walk(child) {
					return true
				}
			}
		case []any:
			for _, child := range v {
				if walk(child) {
					return true
				}
			}
		}
		return false
	}
	return walk(doc)
}
