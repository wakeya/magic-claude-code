//go:build !race

package providerquota

const (
	scriptWorkerHardMemoryLimit uint64 = 128 * 1024 * 1024
	scriptWorkerSoftMemoryLimit int64  = 96 * 1024 * 1024
	scriptWorkerRaceBuild              = false
)
