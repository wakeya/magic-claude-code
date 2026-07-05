//go:build windows

package bootstrap

import "golang.org/x/sys/windows"

// privilegedByOS 报告当前进程是否持有 elevated token（UAC 提权 / 管理员）。
// 无法判定（OpenProcessToken 失败）时返回 false，避免误拒功能。
func privilegedByOS() bool {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()
	return token.IsElevated()
}
