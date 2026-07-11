# 自动更新器下载 URL 脱敏审查记录

日期：2026-07-10  
审查人：Codex

## 审查范围

结合当前 updater 与管理 API 实现审查了 `spec.md` 和 `spec_ZH.md`，并验证了现有代理相关复现、Go 1.26 `net/http` 行为、畸形重定向处理、opaque URL 处理及 updater 测试基线。当前分支尚无实现代码 diff。

## 主要发现与处理要求

1. **安全缺陷——opaque 或非 HTTP URL 可绕过计划中的脱敏。**
   - 复现：`redactURLForError("user:password-secret@example.com/path?token=query-secret")` 会删除 query，但 URL 的 opaque 部分仍保留 `password-secret`。`http.NewRequestWithContext` 接受该 scheme，随后 `client.Do` 返回的错误仍含该值。因此，对可由 source 配置影响的 URL 而言，规格中“现有脱敏规则已经正确”的判断不成立。
   - 必须处理：除非 URL 是带 host 的绝对 `http`/`https` URL，否则脱敏输出应 fail closed；或以其他方式确保 `Opaque` 及所有可能承载凭据的部分都被移除。同时修改“不校验 scheme”的非目标，并增加 opaque/不支持 scheme 的回归测试。

2. **安全缺陷——只修改 `*url.Error.URL` 无法清除畸形重定向错误。**
   - 复现：本地服务返回 `Location: https://redirect.example/%zz?token=redirect-secret` 时，Go 1.26 会把原始 `Location` 放进 `urlErr.Err`。只脱敏外层 `urlErr.URL` 后，`urlErr.Error()` 以及管理 API 响应中仍含 `redirect-secret`。
   - 必须处理：为 `client.Do` 失败定义安全的可观察错误表示，不能假定敏感文本只存在于外层 `URL` 字段。增加 hermetic 的畸形重定向测试。若必须保留错误 identity 或 unwrap 能力，需要明确约定并测试这些语义。

3. **测试缺口——计划中的 `NewRequest` 修复没有直接回归测试。**
   - 必须处理：增加一个确定性的畸形 URL 用例，使用彼此不同的凭据/query 标记，并断言错误 fail closed（例如 `<invalid-url>`），且不回显原始解析错误。

4. **范围和错误路径清单不完整。**
   - `downloadFileWithLimit` 有五个错误返回，而不是四个；表格漏掉了 `io.ReadAll` 失败路径。普通远端无法任意控制 Go 的 body 读取错误文本，因此目前未证明它可被利用，但规格必须将其纳入保证，或明确排除并说明理由。
   - 当前 admin handler 会把 updater 错误返回给 `POST /api/update/apply`，但不会记录该错误。应删除没有代码依据的“进程日志”说法，或指出真实日志链路并补测试。

5. **需修正 Go 1.26 行为描述和测试断言。**
   - `http.Client` 使用 `stripPassword`，因此它生成的 `*url.Error.URL` 中 password 会显示为 `***`；username、query 和 fragment 仍可能暴露。规格中显示原始 password 的例子不符合所声明的 Go 版本。
   - username、password、query、fragment 应使用不同标记，并断言所有标记以及 `token=` 等敏感 key 均不存在。集成测试不能只检查 Go 已自动掩码的 `user:pass`。聚焦 helper 测试应断言精确的脱敏 URL 与保留语义，而不只是检查少量子串。

6. **helper 对包装错误的契约不明确。**
   - `errors.As` 可能找到嵌套的 `*url.Error`；返回该内层错误会丢失外层上下文，而返回原始 wrapper 又可能继续暴露包装时缓存的未脱敏消息。鉴于 `http.Client.Do` 返回顶层 `*url.Error`，应把 helper 限定为该契约，或明确安全的包装错误行为并增加测试。

## 验证记录

- `go test ./internal/updater/ -run TestDownloadAndApplyRedactsInvalidDownloadURL -count=1`——普通网络路径通过。
- `HTTPS_PROXY=http://127.0.0.1:9 go test ./internal/updater/ -run TestDownloadAndApplyRedactsInvalidDownloadURL -count=1`——确定性失败并暴露 query secret。
- `go test ./internal/updater/ -count=1`——基线通过。
- 临时 hermetic 探针确认了畸形重定向的嵌套泄露和 opaque URL 泄露；探针文件随后已删除。

## 最终审查结论

代理相关泄露是真实问题，但当前计划尚不能达到其声明的全路径脱敏保证。规格中仍有两个可复现的安全缺口：opaque/非 HTTP URL，以及含敏感信息的畸形重定向 Location。应先补全 sanitizer/错误契约和回归矩阵，再进入实现。

## 处理进展

在审查并确认 fail-closed 方案后，规格已经修订：opaque/非 HTTP URL 会在 transport 前被拒绝；诊断只暴露 origin；所有不可信底层错误文本及 unwrap 链都会被丢弃；畸形重定向和 body 读取失败均有覆盖；并新增管理 API sink 级验收测试。上述设计层发现已在规格中解决，代码实现与验证仍待完成。

## 遗留说明

- 合并上述发现时保持 `spec.md` 与 `spec_ZH.md` 语义一致。
- 实现完成后，在关闭审查前运行聚焦测试、`go test ./internal/updater/`、`go test ./...` 和 `go test -race ./internal/updater/`。

## 实现验证结果（2026-07-10）

按规格 fail-closed 设计完成实现，并按 TDD 顺序验证。

### 改动文件

- `internal/updater/updater.go`：新增 `parseDownloadURL`、`safeURLOrigin`、修订后的 `redactURLForError`、`checksumURLForAsset`、`requestFailureCategory`；改写 `downloadFileWithLimit` 为 origin-only 固定消息；`DownloadAndApply` 用 `checksumURLForAsset` 替换 `strings.LastIndex` 切片；新增 `errors` import。
- `internal/updater/updater_test.go`：新增第 7 节 helper（`roundTripFunc`、`errorReadCloser`、`assertNoSensitiveMarkers`、marker 常量、`validURLWithMarkers`、`allSensitiveMarkers`）及 8.1–8.9 全部测试；更新状态/大小测试为 origin-only。
- `internal/admin/update_handler_test.go`：新增包内 fake `updater.ReleaseSource` 与 8.10 sink 级畸形重定向回归。

### 红→绿证据

先加测试、再实现。实现前 `go test ./internal/updater/` 结果为 56 passed / 13 failed，失败精确暴露了旧实现的真实泄露面：

- `TestDownloadFileRejectsUnsafeURL`：opaque / 非法 scheme / 相对 URL / 缺 host / 畸形 escape 都进入了 transport（`transport must not be called for invalid URL`）。
- `TestDownloadFileTransportErrorDiscardsRawCause` / `TestDownloadFileCanceledRequest` / `TestDownloadFileDeadlineExceeded`：旧 `client.Do` 返回的 `*url.Error` 泄露 `Get "https://username-secret:***@example.com/path-secret?query-key=query-secret#fragment-secret"`（password 被 Go 掩为 `***`，但 username/path/query/fragment 仍泄露）。
- `TestDownloadFileMalformedRedirectDiscardsLocation`：畸形重定向 Location 进入嵌套错误并泄露 `redirect.example` / `%zz`。
- `TestDownloadFileBodyReadErrorDiscardsRawCause`：旧代码直接返回 `body-secret` 原文。
- `TestDownloadAndApplyRedactsInvalidDownloadURL`：`%w` 链把原始 `*url.Error` 穿透到 `download asset:` 上层。

改写后 `go test ./internal/updater/` 为 69 passed（绿）。

### 验证命令与结果（均退出码 0）

| 命令 | 结果 |
| --- | --- |
| `gofmt -w internal/updater/updater.go internal/updater/updater_test.go internal/admin/update_handler_test.go` | 通过，`gofmt -l` 为空 |
| `go test ./internal/updater/ -count=1` | 69 passed |
| `go test ./internal/admin/ -run 'TestHandleUpdateApply\|TestWriteUpdateApply' -count=1` | 5 passed |
| `HTTPS_PROXY=http://127.0.0.1:9 go test ./internal/updater/ -count=1` | 69 passed |
| `go test -race ./internal/updater/ ./internal/admin/` | 188 passed（2 packages） |
| `go test ./...` | 1395 passed（16 packages） |
| `go vet ./internal/updater/ ./internal/admin/` | No issues |
| `make test`（CI 入口） | 退出码 0 |

### 安全行为核对

- 下载 URL 仅当 `url.Parse` 成功、`Opaque==""`、scheme 为 `http`/`https`（不区分大小写并规范化小写）且 `Host!=""` 时才接受，否则在 transport 前返回固定 `invalid download URL: <invalid-url>`。
- 所有下载错误仅暴露规范化 origin（scheme+host[:port]）与固定类别/状态码/大小上限；原始 `err` 文本一律丢弃，不使用 `%w` 包装不可信错误，自定义错误不带 `Unwrap`。
- `TestDownloadFileTransportErrorDiscardsRawCause` 断言 `errors.Unwrap(err)==nil`，证明 helper 边界无可达原始 cause。
- checksum URL 用解析后的 URL + `ResolveReference` 构造，保留 userinfo/目录 path 仅作为请求 URL，绝不进入诊断输出。
- 管理 API 端到端测试（`TestHandleUpdateApplyRedactsMalformedRedirect`）验证最终安全 sink：JSON body 不含 redirect/path/query/userinfo/底层错误等 12 个标记，仅保留 `download asset:` 安全上下文。
- 全部聚焦测试 hermetic，不访问公网。

### 改动范围

`git diff --stat` 仅含本功能 3 个文件 + 用户预存的 `sdd-docs/features/README.md` 与本特性规格目录；无无关代码或前端产物变更。无未解决问题。

## 独立复审（2026-07-10）

### 发现

1. **测试缺陷——管理 API sink 级脱敏断言读取的是已消费完的 buffer。**
   - `TestHandleUpdateApplyRedactsMalformedRedirect` 先直接从 `rr.Body` 解码 JSON，随后才执行 `body := rr.Body.String()`。`json.Decoder.Decode` 完成后，`bytes.Buffer.String()` 只返回未读部分，此时为空；因此即使序列化响应含敏感标记，后续 12 个 marker 的循环也会误通过。临时探针已稳定复现该行为，随后已删除。
   - 测试 URL 实际只包含 path/query 标记，没有包含注释和 marker 列表所声称覆盖的 userinfo/fragment 标记。
   - 必须处理：解码前保存响应 body（或从副本解码），对该快照或 `resp.Error` 执行 marker 断言；若声称覆盖 userinfo/fragment，应把对应标记加入本地 asset URL。修正后重新运行聚焦 admin 测试。

### 独立验证

- `go test ./internal/updater/ -count=1`——通过。
- `HTTPS_PROXY=http://127.0.0.1:9 go test ./internal/updater/ -count=1`——通过。
- `go test ./internal/admin/ -run 'TestHandleUpdateApply|TestWriteUpdateApply' -count=1`——通过，但上述发现使 sink 负向断言无效。
- `go test -race ./internal/updater/ ./internal/admin/`——通过。
- `go test ./...`——通过。
- `go vet ./internal/updater/ ./internal/admin/`、`gofmt -l` 与 `git diff --check`——通过/干净。

### 复审结论

未在 fail-closed updater 实现中发现代码级 URL 泄露，updater 层安全测试也确实命中了预期路径。但强制的管理 API 验收测试仍有一个可复现的测试缺陷，因此在修正并重新运行该测试前，不应将功能视为完全 validated。此前“无未解决问题”的结论由本次复审取代。

## 复审缺陷修正（2026-07-10）

复审 Finding 已处理。两个问题均确认属实并已修正：

### 根因确认

`bytes.Buffer.String()` 返回 `buf[off:]`（未读部分），`json.NewDecoder(rr.Body).Decode` 推进读指针 `off`，故解码后再 `rr.Body.String()` 得到空串，12 个 marker 的"不存在"断言退化为对空串的检查，恒为真。

### 修正内容（`internal/admin/update_handler_test.go`）

1. 先 `body := rr.Body.String()` 捕获完整响应快照，再 `json.Unmarshal([]byte(body), &resp)` 解码；并新增健全性断言 `body` 非空且含 `download asset:`，证明捕获的是真实响应而非空串。
2. 本地 asset URL 注入 userinfo 与 fragment 标记：`http://username-secret:password-secret@<host>/path-secret?query-key=query-secret#fragment-secret`，使 sink 测试真正覆盖 userinfo/fragment 向量，而非仅 path/query。
3. 12 个 marker 的负向断言改为对捕获的 `body` 快照执行。

### 有效性探针

为证明负向断言不再空转，临时将 `downloadFileWithLimit` 的 `client.Do` 错误分支还原为 `return nil, err`（原始泄露写法），仅运行 `TestHandleUpdateApplyRedactsMalformedRedirect`：

```
[FAIL] TestHandleUpdateApplyRedactsMalformedRedirect
   update_handler_test.go:199: response body leaked "username-secret": {"success":false,"restarting":...
```

测试精确捕获到 `username-secret` 经 `download asset: %w` 链穿透到 JSON sink 的泄露，证明：捕获的 body 非空、userinfo 向量被覆盖、断言有效。随后立即恢复脱敏实现（`git diff` 确认仅 `return nil, err` → 脱敏消息这一处差异，无 PROBE 残留）。

### 重新验证（均退出码 0）

| 命令 | 结果 |
| --- | --- |
| `gofmt -w ...` + `gofmt -l` | 通过，为空 |
| `go test ./internal/updater/ -count=1` | 69 passed |
| `go test ./internal/admin/ -run 'TestHandleUpdateApply\|TestWriteUpdateApply' -count=1` | 5 passed |
| `HTTPS_PROXY=http://127.0.0.1:9 go test ./internal/updater/ -count=1` | 69 passed |
| `go test -race ./internal/updater/ ./internal/admin/` | 188 passed（2 packages） |
| `go test ./...` | 1395 passed（16 packages） |
| `go vet ./internal/updater/ ./internal/admin/` | No issues |

### 结论

复审发现的 sink 级测试缺陷已修正并经探针证明断言有效；实现代码无新的 URL 泄露。功能可视为完全 validated。

## Codex 最终复核（2026-07-10）

Codex 已独立验证本次修正：测试在解码前捕获非空 JSON body，真实覆盖 userinfo/path/query/fragment 与畸形重定向目标，并针对捕获的 sink 输出检查全部 marker。作为负向对照，临时恢复原始 `return nil, err` 分支后，`TestHandleUpdateApplyRedactsMalformedRedirect` 会因 `username-secret` 失败并显示完整旧泄露链；随后已恢复 fail-closed 分支，且无探针残留。

新一轮验证全部通过：聚焦 admin 测试、updater 测试、强制代理 updater 测试、`go test ./...`、`go test -race ./internal/updater/ ./internal/admin/`、`go vet`、`gofmt -l` 和 `git diff --check`。本次变更未发现剩余逻辑、安全或测试缺陷。
