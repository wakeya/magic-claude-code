//go:build !race

package providerquota

const (
	scriptWorkerHardMemoryLimit uint64 = 512 * 1024 * 1024
	scriptWorkerSoftMemoryLimit int64  = 384 * 1024 * 1024
	scriptWorkerRaceBuild              = false
)
