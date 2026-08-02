//go:build windows

package bench

import (
	"syscall"

	"golang.org/x/sys/windows"
)

func availableDiskBytes(path string) (uint64, error) {
	dir, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var available, total, free uint64
	if err := windows.GetDiskFreeSpaceEx(dir, &available, &total, &free); err != nil {
		return 0, err
	}
	return available, nil
}
