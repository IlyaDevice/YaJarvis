//go:build linux

package main

import "syscall"

func diskSpace(path string) (freeMB, totalMB int) {
	var st syscall.Statfs_t
	if syscall.Statfs(path, &st) != nil {
		return 0, 0
	}
	bs := uint64(st.Bsize)
	return int(st.Bavail * bs >> 20), int(st.Blocks * bs >> 20)
}
