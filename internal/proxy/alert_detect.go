package proxy

import (
	"fmt"
	"net"
	"sync"
)

// alertDetectingConn 包装 net.Conn，在 Read 时增量解析 TLS record，
// 检测客户端发来的明文 Alert record（ContentType=21, length=2）。
//
// 用途：TLS 握手失败时，区分"客户端主动拒绝"（如 unknown_ca、handshake_failure）
// 和真正的密钥协商/记录错误。客户端在证书校验失败后会发明文 fatal alert，
// 代理用 handshake key 解这条明文 alert 必然 AEAD 失败、日志表现为
// "bad record MAC"——此包装器让真实原因能被记录下来。
//
// 设计约束（生产实现，无缓冲）：
//   - 不保存任何原始字节；只保留最后检测到的 2 字节 alert（level + description）
//   - 结构化解析 record（5 字节 header + payload），不搜索 magic bytes，
//     避免 handshake/AppData payload 中恰好出现的 15 03 03 00 02 序列被误判
//   - 只识别 ContentType=Alert(21) 且 length=2 的 record：
//     · TLS 1.3 加密 Alert 外层是 AppData(23)，不匹配
//     · TLS 1.2 加密 Alert 的 length > 2（含 MAC/padding），不匹配
//   - feed 无分配，仅操作固定大小的结构体字段
type alertDetectingConn struct {
	net.Conn
	mu sync.Mutex

	// record header 读取状态
	hdr       [5]byte
	hdrFilled int // 0..5
	// 当前 record 的 payload 读取状态（payloadRemain>0 表示正在读 payload）
	payloadType   byte
	payloadRemain int
	// 当前 record 是否是待读取的明文 alert（header 读满时计算）
	readingAlert bool
	// 明文 alert 的 2 字节缓冲
	alertBuf    [2]byte
	alertFilled int
	// 检测结果（最后一次明文 alert）
	level    byte
	desc     byte
	detected bool
}

// Read 透传底层读取，同时把读到的字节喂给增量 parser。
func (a *alertDetectingConn) Read(b []byte) (int, error) {
	n, err := a.Conn.Read(b)
	if n > 0 {
		a.mu.Lock()
		a.feed(b[:n])
		a.mu.Unlock()
	}
	return n, err
}

// feed 增量解析 TLS 字节流。状态机：payloadRemain>0 时读 payload，否则读 header。
// 无分配。
func (a *alertDetectingConn) feed(data []byte) {
	for len(data) > 0 {
		if a.payloadRemain > 0 {
			// 阶段 2：读 payload
			if a.readingAlert {
				n := copy(a.alertBuf[a.alertFilled:2], data)
				a.alertFilled += n
				a.payloadRemain -= n
				data = data[n:]
				if a.alertFilled == 2 {
					a.level = a.alertBuf[0]
					a.desc = a.alertBuf[1]
					a.detected = true
					a.alertFilled = 0
					a.readingAlert = false
				}
				if a.payloadRemain > 0 {
					return // alert 未读满，等待更多数据
				}
			} else {
				taking := min(len(data), a.payloadRemain)
				data = data[taking:]
				a.payloadRemain -= taking
			}
		} else {
			// 阶段 1：读 5 字节 record header
			n := copy(a.hdr[a.hdrFilled:5], data)
			a.hdrFilled += n
			data = data[n:]
			if a.hdrFilled < 5 {
				return // header 未读满，等待更多数据
			}
			a.payloadType = a.hdr[0]
			a.payloadRemain = int(a.hdr[3])<<8 | int(a.hdr[4])
			a.hdrFilled = 0
			a.readingAlert = a.payloadType == 21 && a.payloadRemain == 2
			// payloadRemain==0 时，下一轮循环直接读下一个 header
		}
	}
}

// hint 返回握手失败的附加诊断信息。未检测到明文 alert 时返回空串。
// 格式示例：" (client sent plaintext fatal alert: unknown_ca [48])"
func (a *alertDetectingConn) hint() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.detected {
		return ""
	}
	return fmt.Sprintf(" (client sent plaintext %s alert: %s [%d])",
		alertLevelName(a.level), alertName(a.desc), a.desc)
}

// alertLevelName 映射 alert level 到名称。未知值输出数值，无字符串注入风险。
func alertLevelName(level byte) string {
	switch level {
	case 1:
		return "warning"
	case 2:
		return "fatal"
	}
	return fmt.Sprintf("level_%d", level)
}

// alertName 映射 TLS alert description 到名称（RFC 5246 §A.3 / RFC 8446 §6）。
// 未知值输出数值，无字符串注入风险（description 是单字节，0-255）。
func alertName(desc byte) string {
	switch desc {
	case 0:
		return "close_notify"
	case 10:
		return "unexpected_message"
	case 20:
		return "bad_record_mac"
	case 21:
		return "decryption_failed"
	case 22:
		return "record_overflow"
	case 30:
		return "decompression_failure"
	case 40:
		return "handshake_failure"
	case 41:
		return "no_certificate"
	case 42:
		return "bad_certificate"
	case 43:
		return "unsupported_certificate"
	case 44:
		return "certificate_revoked"
	case 45:
		return "certificate_expired"
	case 46:
		return "certificate_unknown"
	case 47:
		return "illegal_parameter"
	case 48:
		return "unknown_ca"
	case 49:
		return "access_denied"
	case 50:
		return "decode_error"
	case 51:
		return "decrypt_error"
	case 60:
		return "export_restriction"
	case 70:
		return "protocol_version"
	case 71:
		return "insufficient_security"
	case 80:
		return "internal_error"
	case 86:
		return "inappropriate_fallback"
	case 90:
		return "user_canceled"
	case 100:
		return "no_renegotiation"
	case 109:
		return "missing_extension"
	case 110:
		return "unsupported_extension"
	case 111:
		return "certificate_unobtainable"
	case 112:
		return "unrecognized_name"
	case 113:
		return "bad_certificate_status_response"
	case 114:
		return "bad_certificate_hash_value"
	case 115:
		return "unknown_psk_identity"
	case 116:
		return "certificate_required"
	case 120:
		return "no_application_protocol"
	case 121:
		return "encrypted_client_hello_required"
	}
	return fmt.Sprintf("alert_%d", desc)
}
