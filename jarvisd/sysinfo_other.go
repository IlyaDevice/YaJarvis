//go:build !linux

package main

func diskSpace(path string) (freeMB, totalMB int) { return 0, 0 }
