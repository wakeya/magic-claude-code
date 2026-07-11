package proxy

import (
	"net"
	"strings"
	"testing"
)

// tlsRecord 构造一条 TLS record：type + version(0303) + length + payload
func tlsRecord(typ byte, payload []byte) []byte {
	r := make([]byte, 5+len(payload))
	r[0] = typ
	r[1] = 0x03
	r[2] = 0x03
	r[3] = byte(len(payload) >> 8)
	r[4] = byte(len(payload))
	copy(r[5:], payload)
	return r
}

func alertRecord(level, desc byte) []byte {
	return tlsRecord(21, []byte{level, desc})
}

// feedChunked 把 data 按每块 chunk 字节喂给 ac，模拟 record 跨多次 Read
func feedChunked(ac *alertDetectingConn, data []byte, chunk int) {
	for len(data) > 0 {
		n := min(chunk, len(data))
		ac.feed(data[:n])
		data = data[n:]
	}
}

// --- 用例 1：ClientHello 与 Alert 在同一次 feed ---
func TestFeedAlertSameRead(t *testing.T) {
	ch := tlsRecord(22, make([]byte, 100)) // 模拟 ClientHello
	al := alertRecord(2, 48)               // fatal/unknown_ca
	var ac alertDetectingConn
	ac.feed(append(ch, al...))
	if !ac.detected {
		t.Fatal("expected alert detected")
	}
	if ac.level != 2 || ac.desc != 48 {
		t.Fatalf("got level=%d desc=%d, want 2/48", ac.level, ac.desc)
	}
}

// --- 用例 2：record 跨多次 Read（每字节一块）---
func TestFeedAlertByteByByte(t *testing.T) {
	ch := tlsRecord(22, make([]byte, 50))
	al := alertRecord(2, 48)
	var ac alertDetectingConn
	feedChunked(&ac, append(ch, al...), 1)
	if !ac.detected || ac.desc != 48 {
		t.Fatalf("byte-by-byte: detected=%v desc=%d", ac.detected, ac.desc)
	}
}

// --- 用例 3a：record header 跨 feed 边界 ---
func TestFeedHeaderSplit(t *testing.T) {
	ch := tlsRecord(22, make([]byte, 10))
	al := alertRecord(2, 48)
	all := append(ch, al...)
	var ac alertDetectingConn
	ac.feed(all[:7]) // 切在 ClientHello header 之后 / 中间
	ac.feed(all[7:])
	if !ac.detected || ac.desc != 48 {
		t.Fatalf("header split: detected=%v desc=%d", ac.detected, ac.desc)
	}
}

// --- 用例 3b：alert payload 跨 feed 边界（level 与 desc 分开）---
func TestFeedAlertPayloadSplit(t *testing.T) {
	ch := tlsRecord(22, make([]byte, 10))
	al := alertRecord(2, 48)
	all := append(ch, al...)
	alertPayloadStart := len(ch) + 5 // 跳过 ClientHello record + Alert record header
	var ac alertDetectingConn
	ac.feed(all[:alertPayloadStart+1]) // 含 alert level，不含 desc
	ac.feed(all[alertPayloadStart+1:]) // 含 desc
	if !ac.detected || ac.level != 2 || ac.desc != 48 {
		t.Fatalf("payload split: detected=%v level=%d desc=%d", ac.detected, ac.level, ac.desc)
	}
}

// --- 用例 3c：alert 声明 length=2 但 payload 截断（feed 后无更多数据）---
func TestFeedTruncatedAlert(t *testing.T) {
	ch := tlsRecord(22, make([]byte, 10))
	// 构造 Alert record header 声明 length=2，但只给 1 字节 payload
	trunc := []byte{21, 0x03, 0x03, 0x00, 0x02, 0x02}
	var ac alertDetectingConn
	ac.feed(append(ch, trunc...))
	if ac.detected {
		t.Fatal("truncated alert must not be detected")
	}
}

// --- 用例 4：handshake payload 内含 Alert record 的字节序列，不误判 ---
func TestFeedNoFalsePositiveInHandshakePayload(t *testing.T) {
	// 在 handshake record 的 payload 里塞入完整的 Alert record 字节
	// parser 必须按结构解析，不会把 payload 内容当作 record
	inner := alertRecord(2, 48)            // 7 字节，形似一条 Alert record
	payload := append(inner, make([]byte, 50)...)
	ch := tlsRecord(22, payload)
	var ac alertDetectingConn
	ac.feed(ch)
	if ac.detected {
		t.Fatal("must not detect alert inside handshake payload")
	}
}

// --- 用例 5a：AppData record (type=23) 的 payload 是 02 30，不误判 ---
func TestFeedNoFalsePositiveAppData(t *testing.T) {
	rec := tlsRecord(23, []byte{0x02, 0x30}) // AppData，2 字节
	var ac alertDetectingConn
	ac.feed(rec)
	if ac.detected {
		t.Fatal("must not detect alert from AppData record")
	}
}

// --- 用例 5b：TLS 1.2 加密 Alert（type=21 但 length>2），不误判 ---
func TestFeedNoFalsePositiveEncryptedAlert(t *testing.T) {
	rec := tlsRecord(21, make([]byte, 32)) // type=21 但 length=32（加密 alert + MAC）
	var ac alertDetectingConn
	ac.feed(rec)
	if ac.detected {
		t.Fatal("must not detect encrypted alert (length!=2)")
	}
}

// --- 用例 6：未知 alert 编号输出数值，无注入 ---
func TestFeedUnknownAlertNumber(t *testing.T) {
	var ac alertDetectingConn
	ac.feed(alertRecord(2, 200)) // 200 不在已知列表
	if !ac.detected || ac.desc != 200 {
		t.Fatal("expected detected with desc=200")
	}
	if got := alertName(200); got != "alert_200" {
		t.Fatalf("alertName(200)=%q, want alert_200", got)
	}
	h := ac.hint()
	if !strings.Contains(h, "alert_200") {
		t.Fatalf("hint should contain alert_200: %s", h)
	}
}

// --- alertName 已知值映射 ---
func TestAlertNameKnown(t *testing.T) {
	cases := map[byte]string{
		0:   "close_notify",
		40:  "handshake_failure",
		42:  "bad_certificate",
		45:  "certificate_expired",
		48:  "unknown_ca",
		51:  "decrypt_error",
		70:  "protocol_version",
		111: "certificate_unobtainable",
		112: "unrecognized_name",
		113: "bad_certificate_status_response",
		114: "bad_certificate_hash_value",
		115: "unknown_psk_identity",
		116: "certificate_required",
		120: "no_application_protocol",
		121: "encrypted_client_hello_required",
	}
	for desc, want := range cases {
		if got := alertName(desc); got != want {
			t.Errorf("alertName(%d)=%q, want %q", desc, got, want)
		}
	}
}

// --- alertLevelName 已知/未知 ---
func TestAlertLevelName(t *testing.T) {
	if got := alertLevelName(1); got != "warning" {
		t.Errorf("level 1 = %s", got)
	}
	if got := alertLevelName(2); got != "fatal" {
		t.Errorf("level 2 = %s", got)
	}
	if got := alertLevelName(9); got != "level_9" {
		t.Errorf("level 9 = %s", got)
	}
}

// --- 多条 alert record，记录最后一条 ---
func TestFeedMultipleAlertsKeepsLast(t *testing.T) {
	first := alertRecord(2, 40)            // handshake_failure
	second := alertRecord(2, 48)           // unknown_ca
	var ac alertDetectingConn
	ac.feed(append(first, second...))
	if !ac.detected || ac.desc != 48 {
		t.Fatalf("expected last alert unknown_ca(48), got detected=%v desc=%d", ac.detected, ac.desc)
	}
}

// --- hint 未检测时返回空串 ---
func TestHintEmptyWhenNotDetected(t *testing.T) {
	var ac alertDetectingConn
	if h := ac.hint(); h != "" {
		t.Fatalf("expected empty hint, got %q", h)
	}
}

// --- hint 格式正确 ---
func TestHintFormat(t *testing.T) {
	var ac alertDetectingConn
	ac.feed(alertRecord(2, 48))
	h := ac.hint()
	for _, want := range []string{"plaintext", "fatal", "unknown_ca", "[48]"} {
		if !strings.Contains(h, want) {
			t.Errorf("hint %q missing %q", h, want)
		}
	}
}

// --- Read 驱动 feed（端到端：Read → feed → detected）---
// 验证 alertDetectingConn.Read 真的把底层字节喂给 feed，而不只是 feed 单元测试
// 自身的闭环。模拟握手期间客户端发 ClientHello + 明文 Alert 的真实读取路径。
func TestReadDrivesFeed(t *testing.T) {
	client, server := net.Pipe()
	defer client.Close()
	defer server.Close()

	ac := &alertDetectingConn{Conn: server}

	ch := tlsRecord(22, make([]byte, 20)) // 模拟 ClientHello
	al := alertRecord(2, 48)              // fatal/unknown_ca
	go func() {
		_, _ = client.Write(append(ch, al...))
		_ = client.Close()
	}()

	buf := make([]byte, 4096)
	for {
		n, err := ac.Read(buf)
		if err != nil && n == 0 {
			break
		}
	}
	if !ac.detected || ac.desc != 48 {
		t.Fatalf("Read-driven: detected=%v desc=%d", ac.detected, ac.desc)
	}
}
