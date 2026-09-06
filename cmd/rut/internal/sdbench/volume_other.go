//go:build !darwin && !linux

package sdbench

import "errors"

type volume struct {
	MountPoint, Device, Filesystem string
	TotalBytes, AvailableBytes     uint64
}

func inspectVolume(path string) (volume, error) {
	return volume{}, errors.New("sdbench is supported on macOS and Linux")
}
