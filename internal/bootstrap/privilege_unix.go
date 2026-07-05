//go:build !windows

package bootstrap

import "os"

func privilegedByOS() bool {
	return os.Geteuid() == 0
}
