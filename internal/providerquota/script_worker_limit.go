package providerquota

import "runtime/debug"

func applyScriptWorkerResourceLimits() (func(), error) {
	cleanupHardLimit, err := applyScriptWorkerHardMemoryLimit(scriptWorkerHardMemoryLimit)
	if err != nil {
		return nil, err
	}

	previousSoftLimit := debug.SetMemoryLimit(scriptWorkerSoftMemoryLimit)
	return func() {
		debug.SetMemoryLimit(previousSoftLimit)
		if cleanupHardLimit != nil {
			cleanupHardLimit()
		}
	}, nil
}
