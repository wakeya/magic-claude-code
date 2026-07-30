//go:build race

package providerquota

const (
	scriptWorkerHardMemoryLimit uint64 = 1024 * 1024 * 1024
	scriptWorkerSoftMemoryLimit int64  = 768 * 1024 * 1024
	scriptWorkerRaceBuild              = true
)
