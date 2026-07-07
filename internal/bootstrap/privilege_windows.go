//go:build windows

package bootstrap

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// privilegedByOS 报告当前进程是否持有 elevated token（UAC 提权 / 管理员）。
// 显式调用 GetTokenInformation 而非 Token.IsElevated()，以便上抛查询错误做
// fail-closed 决策（见 decidePrivileged）。任何无法确定权限状态的情况——
// token 打开失败、elevation 查询失败、返回长度异常——都返回 error。
func privilegedByOS() (bool, error) {
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false, fmt.Errorf("open process token: %w", err)
	}
	defer token.Close()
	var elevated uint32
	var returned uint32
	err := windows.GetTokenInformation(token, windows.TokenElevation, (*byte)(unsafe.Pointer(&elevated)), uint32(unsafe.Sizeof(elevated)), &returned)
	if err != nil {
		return false, fmt.Errorf("get token elevation: %w", err)
	}
	if returned != uint32(unsafe.Sizeof(elevated)) {
		return false, fmt.Errorf("token elevation returned %d bytes, want %d", returned, unsafe.Sizeof(elevated))
	}
	return elevated != 0, nil
}
