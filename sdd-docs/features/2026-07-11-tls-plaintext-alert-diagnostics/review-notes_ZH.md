# TLS 明文 Alert 诊断审查记录

日期：2026-07-11<br>
审查者：Codex 与 Claude Code

## 审查范围

审查提交 `da83af1`、`5f00d56`、`efb48e7` 相对 `3308aca` 的变更，包括入站 TLS record 增量解析器、握手错误日志集成、测试，以及 `bad record MAC / unknown_ca` 运维指引。

## 主要发现与处理

1. **逻辑缺陷——TLS Alert 111 至 117 的映射区间存在错位或无效映射。**
   - `alertName` 从 113 才开始映射 RFC 6066/TLS 1.3 扩展 Alert，但正确编号应为：`certificate_unobtainable=111`、`unrecognized_name=112`、`bad_certificate_status_response=113`、`bad_certificate_hash_value=114`、`unknown_psk_identity=115`、`certificate_required=116`。当前实现遗漏 111–112，把 113–116 的名称整体错位，并错误地把 117 映射成 `unknown_psk_identity`。例如真实 Alert 115 会被记录成 `bad_certificate_status_response`，而不是 `unknown_psk_identity`。
   - 必须处理：修正 111–116 的 case；按目标协议版本删除或正确定义 case 117；把修正后的值加入表驱动测试；若目标是与本机 Go 1.26 标准库完整对齐，同时加入 Alert 121（`encrypted_client_hello_required`）。该缺陷不影响本次已验证的 `unknown_ca=48` 路径。

2. **安全审查——解析器中未发现可利用的内存、信息泄露或日志注入缺陷。**
   - 包装器只保留固定大小的解析状态和两个 Alert 字节，不保存或记录 ClientHello/session 内容；按 record 声明长度跳过 payload，不为 payload 分配内存；未知的对端可控字节只按数字格式化。CPU 工作量与 TLS 握手已经读取的字节线性相关，并继续受既有握手超时与并发上限约束。
   - 处理：可接受。

3. **逻辑审查——record 分片和 payload 内伪特征均处理正确。**
   - 解析器能跨多次 Read 保存五字节 header 和剩余 payload 长度。Handshake/AppData payload 内形似 record 的字节会作为 payload 跳过，不会重新解析；TLS 1.3 加密 Alert 的外层仍是 AppData，不会被当作明文 Alert。
   - 处理：作为诊断增强可接受；Go 原始握手错误仍保留且不被替换。

4. **测试缺口——测试分别覆盖了 `feed` 与 `hint`，但没有覆盖生产日志集成。**
   - 当前没有测试通过 `alertDetectingConn.Read` 驱动 `tlsListener.handleConn`，并断言完整明文 Alert 会追加到日志，而畸形/截断输入不会追加。
   - 建议处理：增加一条 listener/log 集成回归测试。这是维护层测试缺口，不是已证明的运行时或安全缺陷。

5. **运行验证——CA 侧修正在当前环境持续有效。**
   - 2026-07-11 02:45 之后的容器日志中没有 `bad record MAC`、`unknown_ca` 或明文 Alert 握手错误，同时后续请求继续成功。FAQ 已正确提醒：`SSL_CERT_FILE` 必须指向系统 CA bundle，不能指向单独的 MCC CA 文件。
   - 处理：运行现象与证书信任根因一致。日志增强用于诊断未来失败，本身不是客户端信任修复。

## 验证记录

- `go test ./internal/proxy -count=1`——通过。
- `go test -race ./internal/proxy -count=1`——通过。
- `go vet ./internal/proxy`——通过。
- `go test ./... -count=1`——全部包通过。
- `git diff --check 3308aca..HEAD`——干净。
- 扫描 2026-07-11 02:45 之后的运行容器日志——无匹配的 TLS 握手错误。

## 最终审查结论

`unknown_ca(48)` 诊断与固定内存解析器在功能和安全方面成立，客户端 CA 修正在当前运行环境中有效。未发现安全阻断项。但 Alert 111–117 映射区间仍有一项功能准确性缺陷，应在推送或发布前修正；缺少生产日志集成测试属于建议跟进项。

## 遗留说明

- 本次会话不可用 `codex-security` diff-scan 技能；归档前已手工执行等价的 source-to-log、资源耗尽、信息泄露、注入和并发检查。
- 本地分支为 `main`，比 `origin/main` 领先两个提交；添加本审查记录前工作区干净。

## 修复复审（2026-07-11）

提交 `efb48e7` 已解决映射缺陷：

- Alert 111–116 现已与 Go 1.26 标准库编号一致。
- 已删除错误的 `unknown_psk_identity=117`；`unknown_psk_identity` 正确对应 115。
- Alert 121 记录为 `encrypted_client_hello_required`。
- 表驱动测试现已覆盖 111–116、120 和 121。

`TestReadDrivesFeed` 通过 `net.Pipe` 验证真实的 `net.Conn.Read → feed → detected` 链路。`TestTLSListenerAlertHintOnUntrustedCert` 进一步端到端驱动 `tlsListener.handleConn`：强制 TLS 1.2 使客户端证书验证失败的 alert 为明文（ChangeCipherSpec 之前），断言失败日志保留原始 Go 握手错误并追加 `client sent plaintext ... alert` 提示。上一轮 listener/log 集成测试建议现已闭环。

新一轮验证全部通过：

- `go test ./internal/proxy -count=1`
- `go test -race ./internal/proxy -count=1`
- `go vet ./internal/proxy`
- `go test ./... -count=1`
- `git diff --check 3308aca..HEAD`

### 复审结论

未发现剩余逻辑或安全阻断项。Alert 映射修正准确，固定内存和不保存原始字节的安全属性保持不变，listener/log 集成由 `TestTLSListenerAlertHintOnUntrustedCert` 覆盖，聚焦/race/全量测试全部通过。本地三个提交可以推送。

## 独立文档复审（2026-07-11）

实现与运行测试仍然成立，但新归档的规格和审查证据引入了文档阻断项：

1. **记录的验证结果不真实。**
   - 两份 review notes 均称 `git diff --check 3308aca..HEAD` 干净。对当前四提交分支重新执行后退出码为 2，并报告两份 spec 与两份 review notes 均存在行尾空格。Markdown 硬换行可能是有意的，但除非修正文件或调整验证规则，否则不能把该命令记录成 clean。

2. **原始前缀缓冲的论证存在事实错误。**
   - 两份 spec 称约 1538 字节的 ClientHello 会撑爆此前的前缀缓冲；被审查的临时实现上限是 4096 字节，而该抓包连同 Alert 总共约 1550 字节，实际可以容纳。合理的反对理由应是：first-N 缓冲无法普遍保证保留后续 Alert；旧写入没有严格遵守名义上限；生产环境不应保存/记录原始握手字节。

3. **运行时注解证据被夸大。**
   - 任务 2 声称已经在长对话运行时观察到新注解。CA 修复后，被审查的运行日志已不再出现这类失败；注解能力由 TLS 1.2 集成测试证明，而修复前的字节证据来自临时原始抓取。应改为实际具备的证据。

4. **spec 没有严格遵守仓库的单文件计划规则。**
   - `sdd-docs/features/README.md` 要求每个 Task Plan 使用可追踪的 TDD 复选步骤，覆盖失败测试、确认失败、最小实现、确认通过、回归验证、精确文件路径及命令预期结果。当前五个 Plan 是编号叙述，没有记录这套必需的红绿顺序。应补齐，或明确说明为何这份实现后归档属于例外。

5. **集成测试还可以进一步 fail closed。**
   - `TestTLSListenerAlertHintOnUntrustedCert` 已正确驱动 `handleConn → logger`，但 `dialErr == nil` 时调用 `t.Skip`，断言也只检查通用的 `TLS handshake error` / `client sent plaintext` 子串。建议意外成功时 `t.Fatal`，并断言预期 Alert 名称/编号以及真实的 Go 错误子串。现有单测已单独保护 hint 格式，因此这是非安全性的测试加固项，不是已证明的实现缺陷。

新一轮独立验证：

- `TestTLSListenerAlertHintOnUntrustedCert` 普通模式 20/20 通过。
- 同一集成测试 race 模式 10/10 通过。
- `go test ./internal/proxy -count=1`、`go vet ./internal/proxy`、`go test ./... -count=1` 全部通过。
- `git diff --check origin/main..HEAD` 因上述行尾空格退出 2。

### 独立复审结论

未发现新的代理逻辑或安全缺陷，映射与日志集成修正有效。但提交 `7f9b2d4` 的规格和归档证据存在可复现的不准确内容，也不满足其声称严格遵循的计划模板规则，因此应在推送前 amend。集成断言加固建议可在同一次 amend 中处理。

## 最终独立复审（2026-07-11）

提交 `aea8f25` 已解决独立文档复审中的主要实现与证据问题：

- Markdown 硬换行已改用 `<br>`，`git diff --check origin/main..HEAD` 现已干净。
- 原始前缀缓冲论据已改为正确的 first-N 保留、名义上限及原始数据暴露问题。
- Task 2 的验证条目已正确把注解证据归因于 `TestTLSListenerAlertHintOnUntrustedCert`，并说明 CA 修复后没有可供观察的生产失败。
- spec 已明确声明为实现后的回顾性归档，不再声称记录前瞻 TDD 顺序。
- 集成测试现在会在握手意外成功时 fail closed，并精确断言 `bad_certificate [42]` 与 `remote error: tls: bad certificate`。

推送前仍有两个文档一致性问题：

1. 两份 spec 的 Task 2 **Evidence** 行仍写着“运行时日志显示注解”，与紧随其后的已修正验证条目矛盾。
2. Task 4 仍把意外成功写成 `t.Skip`，Plan 仍描述通用的 `client sent plaintext` / `TLS handshake error` 断言，但最终测试已经使用 `t.Fatal` 和精确 Alert/原始错误断言。

提交卫生说明：`aea8f25` 标题为 `docs:`，但同时修改了 `internal/proxy/server_test.go`。可以把测试加固移动到 `efb48e7` 后重新 amend 文档提交，或把提交标题改为包含 test 范围。这只是维护项。

新一轮独立验证：

- `TestTLSListenerAlertHintOnUntrustedCert` 普通模式 20/20 通过。
- 同一测试 race 模式 10/10 通过。
- `go test ./internal/proxy -count=1`、`go vet ./internal/proxy`、`go test ./... -count=1` 全部通过。
- `git diff --check origin/main..HEAD` 通过。
- 本次更新 review notes 前工作区干净。

### 最终独立结论

未发现剩余代理逻辑或安全缺陷。运行实现与 fail-closed 集成测试已就绪；推送前应修正 spec 中两处过时内容，并在上一轮独立复审快照后追加明确闭环。`docs:` 提交范围不匹配建议顺手整理，但不构成阻断。

## 文档闭环（2026-07-11）

工作区中已解决最后两处规格不一致：

- Task 2 Evidence 现已明确把 `TestTLSListenerAlertHintOnUntrustedCert` 作为注解证据，并说明 CA 修复在观察到修复后运行时注解之前就消除了生产失败。
- Task 4 现已记录 fail-closed 的 `t.Fatal` 行为，以及测试实际实现的精确 `bad_certificate [42]` / `remote error: tls: bad certificate` 断言。

双语 spec 更新后的新一轮验证全部通过：

- `TestTLSListenerAlertHintOnUntrustedCert` 普通模式 20/20 通过。
- 同一测试 race 模式 10/10 通过。
- `go test ./internal/proxy -count=1`。
- `go vet ./internal/proxy`。
- `go test ./... -count=1`。
- 最终工作区改动执行 `git diff --check` 通过。

### 闭环结论

所有已识别的功能、安全、测试与文档问题均已解决。本次闭环没有修改业务代码。按已选择的方案，现有四个提交保持不变；这四份文档已可作为独立后续变更提交。
