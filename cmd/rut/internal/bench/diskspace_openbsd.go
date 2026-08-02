//go:build openbsd

package bench

import "golang.org/x/sys/unix"

func availableDiskBytes(path string) (uint64, error) {
	var stat unix.Statfs_t
	if err := unix.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.F_bavail) * uint64(stat.F_bsize), nil
}
