//go:build illumos && !cgo

package bench

import "errors"

func availableDiskBytes(string) (uint64, error) {
	return 0, errors.New("filesystem capacity is unavailable on illumos without cgo")
}
