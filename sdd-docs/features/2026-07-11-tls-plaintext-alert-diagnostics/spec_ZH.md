# TLS 明文 Alert 诊断规格

本地页面：无<br>
代理入口：`internal/proxy/server.go`（`handleConn` 握手错误日志）<br>
参考源：Go 1.26 `crypto/tls`（`conn.go` 的 `halfConn.decrypt`、`handshake_server_tls13.go`）、RFC 8446 §6、RFC 6066<br>
技术栈：Go 1.26 标准库（`net`、`sync`）<br>
最后更新：2026-07-11<br>
进度：5 / 5（已验证）

## 整体分析（源站分析）

### 现象

透明代理（`mcc`）为 `api.anthropic.com` 终结 TLS。长对话期间，`handleConn` 间歇性打印连续 6 条 `local error: tls: bad record MAC`，呈 3s/6s 退避模式，总出现在 SSE streaming 进行中。

### 为什么 "bad record MAC" 是误导性表象

Go 的 `crypto/tls` 中，`alertBadRecordMAC` 由 `halfConn.decrypt`（`conn.go:380-383`）在 AEAD `Open` 失败时返回。`handleConn` 只记录这个 Go 层错误，混淆了两种本质不同的情况：

1. **真正的密钥/记录失败**——客户端密文无法用协商的 handshake key 解密。
2. **客户端侧信任拒绝**——客户端校验代理自签名证书失败后发了**明文** alert（如 `unknown_ca`）。代理此时已装好 handshake traffic key，尝试 AEAD 解密这条明文 alert，必然认证失败 → 表现为 `bad record MAC`。

### 根因链（字节级 dump 验证）

1. Claude Code（Bun 运行时 + BoringSSL）在长对话期间发起后台辅助请求（上下文压缩、摘要等），走的是**不读 `NODE_EXTRA_CA_CERTS`** 的 TLS 路径。
2. 客户端无法校验代理的自签名 CA → 发明文 `fatal / unknown_ca [48]` alert。
3. 代理 AEAD 解密明文 alert → 失败 → 记录 `bad record MAC`。
4. 客户端按 3s/6s 退避重试 → 典型的 6 条爆发。

失败连接的字节 dump 显示记录序列 `[Handshake/1538 Alert/2]`；尾部 alert 解析为 `level=2 (fatal)、description=0x30（48 = unknown_ca）`。

### 修复分工

- **客户端侧（真正的根因修复）：** 把代理 CA 装进**系统 CA 库**（`update-ca-certificates`）。A/B 测试（取消 `SSL_CERT_FILE` 并重启 Claude Code）证明仅靠系统库即足够——Bun 的所有 TLS 路径都读它。`SSL_CERT_FILE` 非必需；指向单个 CA 文件反而有害（某些 TLS 实现把它当替换而非追加，破坏公网 CA 信任）。二进制安装由 `bootstrap` 自动完成；Docker 需运行 `setup-host.sh`。
- **代理侧（本特性）：** 增量 TLS record parser（`alertDetectingConn`），严格解析明文 alert，把真实原因追加到握手失败日志，让未来出现时报告真相，而非误导性的裸 `bad record MAC`。

### 设计约束（来自 review）

1. **不缓冲原始字节**——first-N 字节缓冲无法普遍保证保留后续 alert（取决于 alert 相对 N 的位置）；旧诊断写入未严格遵守名义上限；且生产代码不应保存或记录原始握手字节。
2. **只做结构化 record 解析**——绝不搜索 magic bytes（`15 03 03 00 02`）；handshake/AppData payload 内可能合法出现该序列。
3. **只识别 `ContentType=21、length=2` 的记录**——TLS 1.3 加密 alert 外层是 AppData(23)；TLS 1.2 加密 alert length>2（含 MAC/padding）。两者都被正确排除。
4. **热路径零分配**——只保留 2 字节 alert。

## 开发检查清单

- [x] `alertDetectingConn` 增量 record parser（`internal/proxy/alert_detect.go`）
- [x] `alertName` / `alertLevelName` 按 RFC 8446 §6，与 Go 1.26 标准库对齐
- [x] `handleConn` 集成：包装 conn，失败日志追加 `hint()`
- [x] 单元测试：feed 分片、header/payload 拆分、截断 alert、payload 内伪特征、AppData/加密 alert 排除、未知 alert 编号
- [x] 集成测试：`TestTLSListenerAlertHintOnUntrustedCert` 端到端驱动 `handleConn`（TLS 1.2 明文 alert）
- [x] `CLAUDE.md` 常见问题条目
- [x] 移除临时诊断开关（`MCC_DISABLE_SESSION_TICKET`、`MCC_DEBUG_HANDSHAKE`）

## 需求

### 功能性

- TLS 握手失败日志**必须**区分"客户端发明文 alert"与真正的密钥/记录失败。
- Go 原始错误**必须**保留（hint 是追加，不是替换）。
- 只有严格解析出的明文 alert 记录（`ContentType=21、length=2`）才触发 hint。

### 非功能性

- `Read` 热路径零分配；parser 只操作固定大小的结构体字段。
- 不保存原始握手字节。
- 无日志注入：alert description 是单字节，未知值按数值格式化。

### 范围外

- 客户端侧 CA 配置（写入 FAQ，不在代理代码内）。
- 加密 alert 检测（TLS 1.3 ServerHello 之后；TLS 1.2 CCS 之后）——没有密钥时与应用数据结构上无法区分。

> **计划格式说明：** 本规格为事后归档。下面的计划回顾性地记录实际实现步骤，而非前瞻的 TDD 红绿序列。验证复选框反映实现后收集的证据。

## 任务详情

### 任务 1：增量 TLS Record Parser

#### 需求

**Objective（目标）** — 检测客户端在失败握手中发出的明文 alert，不缓冲原始字节。

**Outcomes（成果）** — `alertDetectingConn` 包装 `net.Conn`，在 `Read` 中增量解析 TLS record，只保留最后检测到的 2 字节明文 alert；`hint()` 返回格式化注解。

**Evidence（证据）** — `internal/proxy/alert_detect.go`；`feed` 无分配；14 个单元测试通过。

**Constraints（约束）** — 结构化 record 解析（5 字节 header + 声明的 payload 长度）；只捕获 `ContentType=21、length=2`；固定大小 parser 状态。

**Edge Cases（边界）** — record 跨多次 Read；header 拆分；alert payload 拆分；截断 alert（声明 length=2 但实际收到 <2 字节）；handshake payload 含形似 alert 的字节；AppData 记录；TLS 1.2 加密 alert（length>2）。

**Verification（验证）** — `go test ./internal/proxy -run 'TestFeed|TestAlert|TestHint' -v`。

#### 计划

1. 定义 `alertDetectingConn`，固定大小字段：5 字节 header 缓冲、`hdrFilled`、`payloadType`、`payloadRemain`、`readingAlert`、2 字节 `alertBuf`、检测到的 `level`/`desc`。
2. `feed` 状态机：`payloadRemain>0` 时消费 payload（`readingAlert` 时捕获，否则用 `min(len(data), payloadRemain)` 跳过）；否则读 header；header 读满时设 `readingAlert = payloadType==21 && payloadRemain==2`。
3. `Read` 在每次底层读取后加锁调用 `feed(b[:n])`。
4. `hint()`（加锁；返回 `""` 或 `(client sent plaintext <level> alert: <name> [<desc>])`）。
5. `alertName` / `alertLevelName` 按 RFC 8446 §6 / RFC 6066。

#### 验证

- [x] `TestFeedAlertSameRead`、`TestFeedAlertByteByByte`、`TestFeedHeaderSplit`、`TestFeedAlertPayloadSplit`、`TestFeedTruncatedAlert`
- [x] `TestFeedNoFalsePositiveInHandshakePayload`、`TestFeedNoFalsePositiveAppData`、`TestFeedNoFalsePositiveEncryptedAlert`
- [x] `TestFeedUnknownAlertNumber`、`TestAlertNameKnown`、`TestAlertLevelName`、`TestFeedMultipleAlertsKeepsLast`、`TestHintEmptyWhenNotDetected`、`TestHintFormat`

### 任务 2：handleConn 集成

#### 需求

**Objective（目标）** — 把检测到的 alert 呈现在握手失败日志里。

**Outcomes（成果）** — `handleConn` 把每个接入的 conn 用 `alertDetectingConn` 包装；失败分支追加 `ac.hint()`；检测到 alert 时日志为 `bad record MAC (client sent plaintext fatal alert: unknown_ca [48])`，否则不变。

**Evidence（证据）** — `internal/proxy/server.go` 的 `handleConn`；`TestTLSListenerAlertHintOnUntrustedCert` 通过真实 TLS listener 验证注解。CA 修复后生产环境已不再产生该失败，因此没有可供观察的修复后运行时注解。

**Constraints（约束）** — 默认启用（无环境变量开关）；Go 原始错误保留。

**Edge Cases（边界）** — 未检测到 alert → 空 hint，日志不变；正常握手不受影响（parser 状态随 conn 释放）。

**Verification（验证）** — `go test ./internal/proxy -run TestTLSListener`；现有 `TestTLSListenerLogsSNIOnUntrustedCert` 仍通过。

#### 计划

1. 在 `handleConn` 的 `tls.Server` 之前包装：`ac := &alertDetectingConn{Conn: conn}; conn = ac`。
2. 在 `Handshake()` 失败分支计算 `extra := ac.hint()`，把 `%s` 追加到两条日志格式串。

#### 验证

- [x] `TestTLSListenerLogsSNIOnUntrustedCert` 通过（无回归）。
- [x] `TestTLSListenerAlertHintOnUntrustedCert` 端到端验证注解（断言 `bad_certificate [42]` + 原始 `remote error: tls: bad certificate` 错误）。注：CA 修复后生产环境不再出现这类失败；修复前的 `unknown_ca` 由临时字节抓取确认，修复后的注解由集成测试验证，而非运行时观察。

### 任务 3：alertName 映射修正

#### 需求

**Objective（目标）** — 把 alert 名称映射修正到 RFC 6066 / RFC 8446，与 Go 1.26 标准库一致。

**Outcomes（成果）** — `alertName` 的 111–116 + 121 case 与标准库对齐；删除错误的 `case 117`。

**Evidence（证据）** — review 发现 111–117 错位；修正 `alert_detect.go`；扩展 `TestAlertNameKnown`。

**Constraints（约束）** — 必须与 Go 1.26 `crypto/tls` 的 alert 赋值一致。

**Edge Cases（边界）** — 未知编号格式化为 `alert_<N>`（单字节，无注入）。

**Verification（验证）** — `go test ./internal/proxy -run TestAlertNameKnown -v`。

#### 计划

1. 对照 RFC 6066 / RFC 8446 与 Go 标准库，审计 `alertName` 的 109–121 case。
2. 加 111（`certificate_unobtainable`）、112（`unrecognized_name`）；修正 113–116；删除错误的 117；加 116（`certificate_required`）、121（`encrypted_client_hello_required`）。
3. 用 111–116、120、121 扩展 `TestAlertNameKnown`。

#### 验证

- [x] `TestAlertNameKnown` 覆盖 0、40、42、45、48、51、70、111–116、120、121。

### 任务 4：handleConn 端到端集成测试

#### 需求

**Objective（目标）** — 验证 `Read → feed → detected → hint → log` 在真实 `tlsListener` 上的完整链路。

**Outcomes（成果）** — `server_test.go` 的 `TestTLSListenerAlertHintOnUntrustedCert`。

**Evidence（证据）** — `-race` 下测试通过；断言原始错误保留与 hint 追加。

**Constraints（约束）** — 客户端与服务端都固定 `MaxVersion: VersionTLS12`，使客户端证书校验失败的 alert 为明文（CCS 之前）。TLS 1.3 同阶段的 alert 是加密的（外层 AppData），属范围外。

**Edge Cases（边界）** — 握手意外成功 → `t.Fatal`；测试生成的自签名证书必须保持不受信任，使用 skip 会掩盖失效的验收测试。

**Verification（验证）** — `go test -race ./internal/proxy -run TestTLSListenerAlertHint -v`。

#### 计划

1. 复用 `TestTLSListenerLogsSNIOnUntrustedCert` 的脚手架（测试证书、带 `logBuf` 的 `newTLSListener`）。
2. 把 `tlsCfg.MaxVersion` 与 `tls.Dial` 配置固定到 `VersionTLS12`。
3. dial 意外成功时立即失败；发生预期失败后，等待 `client sent plaintext` 日志条目。
4. 断言日志包含精确的 `bad_certificate [42]` hint，并保留原始 `remote error: tls: bad certificate` 错误。

#### 验证

- [x] `TestTLSListenerAlertHintOnUntrustedCert` 在 `-race` 下通过。

### 任务 5：FAQ 文档

#### 需求

**Objective（目标）** — 为遇到该日志的运维人员记录根因与修复。

**Outcomes（成果）** — `CLAUDE.md` 常见问题条目。

**Evidence（证据）** — `CLAUDE.md` 的 "bad record MAC / unknown_ca" 问答。

**Constraints（约束）** — 双语输出规范不覆盖 CLAUDE.md（开发者文档，按现有条目风格保持中文）；系统 CA 库是主要修复手段；`SSL_CERT_FILE` 仅作后备，指向系统 bundle。

**Verification（验证）** — 人工审阅。

#### 计划

1. 在 `## 常见问题` 下加一条问答。
2. 说明根因（某条客户端 TLS 路径不读 `NODE_EXTRA_CA_CERTS`）与误导性表象。
3. 给出系统 CA 库修复；注明 `SSL_CERT_FILE` 仅作后备，指向系统 bundle（绝不指向单个 CA 文件）。

#### 验证

- [x] 条目已添加并审阅。
