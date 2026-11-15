//go:build !linux

package main

import "os"

func inode(st os.FileInfo) uint64 { return uint64(st.ModTime().UnixNano()) }
