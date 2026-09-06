//go:build linux

package sdbench

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

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
	v := volume{MountPoint: path, Filesystem: "unknown", TotalBytes: uint64(st.Blocks) * uint64(st.Bsize), AvailableBytes: uint64(st.Bavail) * uint64(st.Bsize)}
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return v, nil
	}
	defer f.Close()
	cleanPath, best := filepath.Clean(path), ""
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		separator := -1
		for i, field := range fields {
			if field == "-" {
				separator = i
				break
			}
		}
		if separator < 6 || separator+2 >= len(fields) {
			continue
		}
		mount := decodeMountField(fields[4])
		if (cleanPath == mount || strings.HasPrefix(cleanPath, mount+string(os.PathSeparator))) && len(mount) > len(best) {
			best = mount
			v.MountPoint, v.Filesystem, v.Device = mount, fields[separator+1], decodeMountField(fields[separator+2])
		}
	}
	return v, nil
}

func decodeMountField(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+3 < len(s) && s[i+1] >= '0' && s[i+1] <= '7' && s[i+2] >= '0' && s[i+2] <= '7' && s[i+3] >= '0' && s[i+3] <= '7' {
			b.WriteByte((s[i+1]-'0')*64 + (s[i+2]-'0')*8 + s[i+3] - '0')
			i += 3
			continue
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
