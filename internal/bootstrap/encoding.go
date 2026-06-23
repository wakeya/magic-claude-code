package bootstrap

import (
	"runtime"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

// decodeCmdOutput 把子进程输出解码为 UTF-8 字符串，用于拼接到错误信息或日志。
//
// Windows 中文系统的 certutil、setx 等命令按活动代码页（通常是 GBK/CP936）
// 输出本地化文本。Go 的 exec 捕获到的是原始字节，直接 string(out) 会把 GBK
// 字节当作 UTF-8 拼接，在日志和错误信息里产生乱码（例如 certutil -addstore
// 权限失败时的中文提示）。
//
// 行为：
//   - 非 Windows：直接返回 UTF-8 原样（Linux/macOS 命令输出已是 UTF-8）。
//   - 已是合法 UTF-8：原样返回，避免对 UTF-8 代码页下的输出做误转。
//   - Windows 非 UTF-8：按 GBK 解码；解码失败时安全回退到原始字节，绝不丢失信息。
func decodeCmdOutput(out []byte) string {
	return decodeOutputForOS(out, runtime.GOOS)
}

// decodeOutputForOS 是 decodeCmdOutput 的可测试形式，按指定 GOOS 解码。
func decodeOutputForOS(out []byte, goos string) string {
	if goos != "windows" || utf8.Valid(out) {
		return string(out)
	}
	if decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(out); err == nil {
		return string(decoded)
	}
	return string(out)
}
