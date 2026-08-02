//go:build netbsd || (solaris && !illumos)

package bench

// illumos also sets the solaris build tag, but its x/sys API does not expose
// Statvfs. Its implementation is therefore kept separate.

import "golang.org/x/sys/unix"

func availableDiskBytes(path string) (uint64, error) {
	var stat unix.Statvfs_t
	if err := unix.Statvfs(path, &stat); err != nil {
		return 0, err
	}
	return uint64(stat.Bavail) * uint64(stat.Frsize), nil
}
