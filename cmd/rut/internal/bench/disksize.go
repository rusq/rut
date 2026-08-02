package bench

import "fmt"

const minDiskBenchmarkSize = int64(1 << 20)

func resolveDiskSize(path string, requested int64, explicit bool) (int64, string, error) {
	available, err := availableDiskBytes(path)
	if err != nil {
		return requested, "", nil
	}
	size, adjusted, err := resolveDiskSizeWithAvailable(requested, explicit, available)
	if err != nil {
		return 0, "", fmt.Errorf("%w in %s", err, path)
	}
	if !adjusted {
		return size, "", nil
	}
	return size, fmt.Sprintf("disk benchmark size reduced from %s to %s (%s available in %s)", formatSize(requested), formatSize(size), formatAvailable(available), path), nil
}

func resolveDiskSizeWithAvailable(requested int64, explicit bool, available uint64) (int64, bool, error) {
	if explicit {
		if uint64(requested) > available {
			return 0, false, fmt.Errorf("disk benchmark requires %s but only %s is available; reduce -size or choose another -disk-dir", formatSize(requested), formatAvailable(available))
		}
		return requested, false, nil
	}
	if uint64(requested) <= available/2 {
		return requested, false, nil
	}
	usable := int64(available / 2)
	usable -= usable % minDiskBenchmarkSize
	if usable < minDiskBenchmarkSize {
		return 0, false, fmt.Errorf("not enough space for a disk benchmark: %s available; use -run to skip disk benchmarks or choose another -disk-dir", formatAvailable(available))
	}
	return usable, true, nil
}

func formatAvailable(available uint64) string {
	const maxInt64 = uint64(^uint64(0) >> 1)
	if available > maxInt64 {
		available = maxInt64
	}
	return formatSize(int64(available))
}
