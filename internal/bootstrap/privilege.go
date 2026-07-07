package bootstrap

import "errors"

// 提权检测与 profile 安全写入相关的哨常量错误。
//
// 透明模式首次配置（hosts/CA 安装）通常需要 root/administrator 权限。但 Node
// 客户端（Claude Code）运行在真实用户会话里，读自己的 profile/HKCU。高权限
// 运行时若写 profile：
//  1. 功能无效：写的是 root/administrator 的 profile，真实用户的 Node 客户端读不到；
//  2. 越权风险：HOME 等用户可控路径可能指向高权限文件，os.ReadFile/WriteFile 会
//     跟随 symlink 越权读写（CWE-59）。
//
// 故高权限运行时拒绝写用户 profile（ErrPrivilegedRun），让用户非特权重启 mcc
// 来配置 Node 客户端 CA 信任。
var (
	// ErrPrivilegedRun 表示当前进程以高权限（root/administrator）运行，
	// 拒绝修改用户 profile/HKCU/session。
	ErrPrivilegedRun = errors.New("refuse profile mutation under privileged run")
	// ErrUnsafeProfile 表示 profile 路径不安全（symlink/非常规文件），在高权限
	// 运行下不读取/写入以避免越权跟随链接（CWE-59）。
	ErrUnsafeProfile = errors.New("unsafe profile path under privileged run (symlink or non-regular)")
)

// isPrivilegedRun 报告当前进程是否应以高权限对待（拒绝 profile/HKCU 修改）。
// Unix: euid==0（root）。Windows: elevated token（administrator）。
// 任何无法确定权限状态的错误都 fail-closed 为"视为特权"（见 decidePrivileged）。
// 测试通过覆盖此变量模拟特权/非特权场景。
var isPrivilegedRun = func() bool {
	return decidePrivileged(privilegedByOS())
}

// decidePrivileged 把 (elevated, err) 翻译为 fail-closed 布尔。
// err != nil（无法确定权限状态）时视为特权 → 拒绝 profile/HKCU 修改，避免在
// 未知权限下放行越权写。明确 elevated → 拒绝；明确 non-elevated → 允许。
// 抽为纯函数便于跨平台单测所有错误路径（无需真实 Windows token）。
func decidePrivileged(elevated bool, err error) bool {
	return err != nil || elevated
}
