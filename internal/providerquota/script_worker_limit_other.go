//go:build !linux && !darwin && !windows

package providerquota

import "fmt"

func applyScriptWorkerHardMemoryLimit(uint64) (func(), error) {
	return nil, fmt.Errorf("script worker hard memory limit is unsupported")
}
