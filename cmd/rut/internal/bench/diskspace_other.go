//go:build !aix && !android && !darwin && !dragonfly && !freebsd && !hurd && !illumos && !ios && !linux && !netbsd && !openbsd && !solaris && !windows

package bench

import "errors"

func availableDiskBytes(string) (uint64, error) {
	return 0, errors.New("filesystem capacity is unavailable on this platform")
}
