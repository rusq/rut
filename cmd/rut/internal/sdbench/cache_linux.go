//go:build linux

package sdbench

import (
	"golang.org/x/sys/unix"
	"os"
)

func cacheMethod() string { return "Linux fsync plus POSIX_FADV_DONTNEED" }
func prepareCache(f *os.File) error {
	if err := f.Sync(); err != nil {
		return err
	}
	return unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED)
}
