//go:build !windows

package bootstrap

import "os"

// privilegedByOS 报告 euid==0（root）。Unix 无错误路径（geteuid 总成功），
// fail-closed 决策由 decidePrivileged 统一处理。
func privilegedByOS() (bool, error) {
	return os.Geteuid() == 0, nil
}
