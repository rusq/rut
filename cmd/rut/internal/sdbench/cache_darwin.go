//go:build darwin

package sdbench

import (
	"golang.org/x/sys/unix"
	"os"
)

func cacheMethod() string           { return "macOS F_NOCACHE" }
func prepareCache(f *os.File) error { _, err := unix.FcntlInt(f.Fd(), unix.F_NOCACHE, 1); return err }
