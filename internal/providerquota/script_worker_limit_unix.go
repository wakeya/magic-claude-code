//go:build linux || darwin

package providerquota

import (
	"fmt"

	"golang.org/x/sys/unix"
)

func applyScriptWorkerHardMemoryLimit(limit uint64) (func(), error) {
	var current unix.Rlimit
	if err := unix.Getrlimit(unix.RLIMIT_DATA, &current); err != nil {
		return nil, fmt.Errorf("get worker data limit: %w", err)
	}

	target := limit
	if current.Cur < target {
		target = current.Cur
	}
	if current.Max < target {
		target = current.Max
	}
	if target == 0 {
		return nil, fmt.Errorf("worker data limit is zero")
	}

	if err := unix.Setrlimit(unix.RLIMIT_DATA, &unix.Rlimit{Cur: target, Max: target}); err != nil {
		return nil, fmt.Errorf("set worker data limit: %w", err)
	}
	return nil, nil
}
