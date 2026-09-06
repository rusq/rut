//go:build darwin

package sdbench

import (
	"bytes"
	"golang.org/x/sys/unix"
)

type volume struct {
	MountPoint, Device, Filesystem string
	TotalBytes, AvailableBytes     uint64
}

func inspectVolume(path string) (volume, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return volume{}, err
	}
	return volume{MountPoint: cstr(st.Mntonname[:]), Device: cstr(st.Mntfromname[:]), Filesystem: cstr(st.Fstypename[:]), TotalBytes: uint64(st.Blocks) * uint64(st.Bsize), AvailableBytes: uint64(st.Bavail) * uint64(st.Bsize)}, nil
}

func cstr(b []byte) string {
	if i := bytes.IndexByte(b, 0); i >= 0 {
		b = b[:i]
	}
	return string(b)
}
