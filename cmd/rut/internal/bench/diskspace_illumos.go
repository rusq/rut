//go:build illumos && cgo

package bench

/*
#include <errno.h>
#include <stdint.h>
#include <stdlib.h>
#include <sys/statvfs.h>

static int available_disk_bytes(const char *path, uint64_t *available) {
	struct statvfs stat;
	if (statvfs(path, &stat) == -1) {
		return errno;
	}
	*available = (uint64_t)stat.f_bavail * (uint64_t)stat.f_frsize;
	return 0;
}
*/
import "C"

import (
	"syscall"
	"unsafe"
)

func availableDiskBytes(path string) (uint64, error) {
	cpath := C.CString(path)
	defer C.free(unsafe.Pointer(cpath))

	var available C.uint64_t
	if errno := C.available_disk_bytes(cpath, &available); errno != 0 {
		return 0, syscall.Errno(errno)
	}
	return uint64(available), nil
}
