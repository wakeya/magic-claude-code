package bootstrap

import (
	"testing"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

func TestDecodeOutputForOS_PassesUTF8Through(t *testing.T) {
	in := []byte("certutil -addstore 失败：拒绝访问。")
	for _, goos := range []string{"windows", "linux", "darwin"} {
		got := decodeOutputForOS(in, goos)
		if got != string(in) {
			t.Errorf("goos=%s: expected UTF-8 pass-through, got %q", goos, got)
		}
	}
}

func TestDecodeOutputForOS_DecodesGBKOnWindows(t *testing.T) {
	// 中文 Windows 上 certutil / setx 输出的典型片段（GBK/CP936 编码）
	const want = "拒绝访问。"
	gbk, err := simplifiedchinese.GBK.NewEncoder().Bytes([]byte(want))
	if err != nil {
		t.Fatalf("encode GBK fixture: %v", err)
	}
	if utf8.Valid(gbk) {
		t.Fatalf("fixture must be non-UTF8 GBK to exercise the decode path")
	}
	got := decodeOutputForOS(gbk, "windows")
	if got != want {
		t.Errorf("windows GBK decode: want %q, got %q", want, got)
	}
}

func TestDecodeOutputForOS_NoDecodeOnNonWindows(t *testing.T) {
	// Linux/macOS 命令输出本身是 UTF-8，非 Windows 下不做 GBK 解码
	gbk, _ := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("拒绝访问。"))
	got := decodeOutputForOS(gbk, "linux")
	if got != string(gbk) {
		t.Errorf("linux must not decode GBK; expected raw bytes, got transformed %q", got)
	}
}

func TestDecodeCmdOutput_NeverPanicsOnArbitraryBytes(t *testing.T) {
	// 任意字节（含非法序列）都不应 panic；调用方依赖这个健壮性保证
	dangerous := [][]byte{
		{0xFF, 0xFE, 0x00},
		{0x80, 0x81, 0x82},
		nil,
	}
	for _, in := range dangerous {
		_ = decodeCmdOutput(in) // must not panic
	}
}
