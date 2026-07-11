# 自动更新器下载 URL 脱敏规格

状态：validated  
本地端点：管理更新 API（`POST /api/update/apply`）  
实现入口：`internal/updater/updater.go`（`DownloadAndApply`、`downloadFileWithLimit`）  
测试文件：`internal/updater/updater_test.go`、`internal/admin/update_handler_test.go`  
运行环境：Go 1.26 标准库  
最后更新：2026-07-10  
进度：4 / 4 个实现任务

## 1. 目标

阻止攻击者可控或配置错误的更新 URL、重定向目标以及网络错误消息通过以下位置暴露凭据或签名 URL 材料：

1. `Updater.DownloadAndApply` 或 `downloadFileWithLimit` 返回的任何错误字符串；
2. `POST /api/update/apply` 返回 JSON 中的 `error` 字段。

实现必须 fail-closed，不得尝试保留任意底层错误文本。安全诊断信息仅限：规范化 URL origin、稳定的操作/类别、HTTP 状态码以及大小上限。

## 2. 规范用语

文中的“必须”“不得”“应该”“可以”属于规范要求。代码示例定义预期契约；只有在所有测试和可观察行为保持等价时，才允许微调命名或格式。

## 3. 当前行为与已验证根因

### 3.1 现有泄露

`downloadFileWithLimit` 当前会直接返回以下位置的原始错误：

- `http.NewRequestWithContext`；
- `u.client.Do`；
- `io.ReadAll`。

`DownloadAndApply` 再用 `%w` 包装这些错误，`handleUpdateApply` 则将 `err.Error()` 写入管理 API 响应。当前没有 updater 错误的进程日志 sink；安全相关的真实 sink 是已认证的管理 API 响应，以及未来任何格式化返回错误的调用方。

现有测试使用真实 URL `https://user:pass@example.com?token=secret`。`example.com` 可响应时测试通过；代理或网络故障使 `client.Do` 返回错误时测试失败。当前代码已验证的稳定复现命令：

```bash
HTTPS_PROXY=http://127.0.0.1:9 \
  go test ./internal/updater/ \
  -run TestDownloadAndApplyRedactsInvalidDownloadURL \
  -count=1
```

### 3.2 Go 1.26 行为

`http.Client.Do` 通常返回顶层 `*url.Error`。Go 1.26 会把外层 `URL` 字段中的 password 掩码为 `***`，但 username、query、fragment、path 以及重定向相关文本仍可能暴露。因此测试不得只依赖 password 来证明泄露。

### 3.3 为什么只修改字段不够

只修改 `*url.Error.URL` 无法清理 `*url.Error.Err`。例如畸形重定向头：

```text
Location: https://redirect.example/%zz?token=redirect-secret
```

会使 Go 将原始 `Location` 放入嵌套错误。返回或包装该嵌套错误仍会泄露 `redirect-secret`。

### 3.4 为什么现有 URL formatter 不够

`redactURLForError` 会清除层级 URL 的 `User`、`RawQuery` 和 `Fragment`，但 `url.Parse` 也接受 opaque URL。例如：

```text
user:password-secret@example.com/path?token=query-secret
```

会被解析为 scheme `user` 和 opaque payload。清除 `RawQuery` 并不会清除 `password-secret`。`http.NewRequestWithContext` 接受该 scheme，后续“不支持协议”错误可能回显 opaque payload。

## 4. 威胁模型与安全边界

### 4.1 不可信输入

以下内容均视为不可信并可能携带机密：

- 来自 `ReleaseSource.AssetURL` 或已发布 release asset 的 `UpdateInfo.DownloadURL`；
- URL 的每个组成部分：userinfo、path、raw path、query、forced query、fragment、opaque payload；
- 重定向 `Location` 头；
- `RoundTripper`、重定向处理、响应 body、context 和网络层返回的 `error` 值。

### 4.2 允许公开的诊断数据

错误最多只能暴露：

- 规范化为小写的 scheme（`http` 或 `https`）；
- URL host 和可选端口；
- 本规格定义的固定操作/类别字符串；
- 数字 HTTP 状态码；
- 配置的最大字节数。

安全显示目标仅为 URL origin：

```text
https://user:pass@example.com:8443/releases/private/asset.tar.gz?token=secret#fragment
  -> https://example.com:8443
```

特意不保留 path，因为签名 CDN 可能在 path segment 中携带凭据。

### 4.3 禁止公开的诊断数据

返回的 `Error()` 字符串和管理 API JSON 不得包含：

- username 或 password；
- path 或 raw path；
- query key 或 value，包括单独的尾随 `?`；
- fragment 或 raw fragment；
- opaque URL payload；
- 重定向 `Location` 文本；
- 请求构造、transport、重定向、body 读取、context 或网络层的任何底层 `err.Error()` 文本。

### 4.4 URL 接受策略

下载 URL 只有同时满足以下条件才有效：

1. `url.Parse` 成功；
2. `Opaque == ""`；
3. scheme 不区分大小写地等于 `http` 或 `https`，随后规范化为小写；
4. `Host != ""`；
5. 重建后的 URL 能被 `http.NewRequestWithContext` 接受。

实际请求 URL 可以含 userinfo、query 和 fragment，因为部分 source 使用认证或签名 URL；但它们绝不能出现在诊断输出中。相对 URL、opaque URL、缺 host URL、不支持的 scheme 以及畸形 escape 必须在调用 transport 前拒绝。

## 5. 必须采用的设计

### 5.1 只解析一次并派生安全 origin

在 `internal/updater/updater.go` 现有脱敏 helper 附近增加两个职责单一的 helper：

```go
func parseDownloadURL(raw string) (*url.URL, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Opaque != "" || parsed.Host == "" {
		return nil, false
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return nil, false
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	return parsed, true
}

func safeURLOrigin(parsed *url.URL) string {
	return (&url.URL{Scheme: parsed.Scheme, Host: parsed.Host}).String()
}

func redactURLForError(raw string) string {
	parsed, ok := parseDownloadURL(raw)
	if !ok {
		return "<invalid-url>"
	}
	return safeURLOrigin(parsed)
}
```

强制属性：

- `parseDownloadURL` 必须丢弃 parser error；调用方不得格式化它。
- `safeURLOrigin` 只能接收已验证成功的 URL。
- `redactURLForError` 对所有被拒绝的 URL 必须精确返回 `<invalid-url>`。
- 不要新增 `redactURLError` helper；字段修改无法满足嵌套错误要求。

### 5.2 稳定的请求失败类别

增加一个只映射固定公开类别、不返回源错误文本的 helper：

```go
func requestFailureCategory(ctx context.Context, err error) string {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(ctx.Err(), context.Canceled):
		return "was canceled"
	case errors.Is(err, context.DeadlineExceeded), errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "timed out"
	default:
		return "failed"
	}
}
```

该 helper 可以用 `errors.Is` 检查错误，但不得把错误写入返回字符串。为 `updater.go` 增加 `errors` import。

### 5.3 安全的 `downloadFileWithLimit` 流程

按以下严格顺序实现：

1. 解析并验证原始 URL；
2. 派生 `target := safeURLOrigin(parsed)`；
3. 使用 `parsed.String()` 构造请求；
4. 执行请求；
5. 用固定消息处理状态码、body 读取和大小限制失败。

必须遵守的可观察消息：

| 失败 | 必须使用的格式 |
| --- | --- |
| URL 被拒绝 | `invalid download URL: <invalid-url>` |
| 请求构造 | `create download request for <origin> failed` |
| `client.Do` 被取消 | `download request to <origin> was canceled` |
| `client.Do` 超时 | `download request to <origin> timed out` |
| 其他 `client.Do` 失败 | `download request to <origin> failed` |
| 非 200 状态 | `unexpected status <code> from <origin>` |
| 响应 body 读取 | `read download response from <origin> failed` |
| 大小限制 | `download from <origin> exceeds maximum size of <n> bytes` |

参考结构：

```go
func (u *Updater) downloadFileWithLimit(ctx context.Context, raw string, maxSize int) ([]byte, error) {
	parsed, ok := parseDownloadURL(raw)
	if !ok {
		return nil, errors.New("invalid download URL: <invalid-url>")
	}
	target := safeURLOrigin(parsed)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("create download request for %s failed", target)
	}
	resp, err := u.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download request to %s %s", target, requestFailureCategory(ctx, err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from %s", resp.StatusCode, target)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, int64(maxSize)+1))
	if err != nil {
		return nil, fmt.Errorf("read download response from %s failed", target)
	}
	if len(data) > maxSize {
		return nil, fmt.Errorf("download from %s exceeds maximum size of %d bytes", target, maxSize)
	}
	return data, nil
}
```

安全约束：

- 所有新错误都不得使用 `%w`，也不得包含局部变量 `err`。
- `DownloadAndApply` 可以保留现有上下文 `%w` 包装，因为被包装的 updater 错误已经安全，并且没有不安全的 unwrap 链。
- 不得返回带 `Unwrap` 的自定义错误类型；保留原始 cause 会为未来调用方重新引入不安全错误链。
- 成功路径以及 200 响应的 body 字节保持不变。

### 5.4 安全构造 checksum URL

替换原始的 `strings.LastIndex(info.DownloadURL, "/")` 逻辑。原始字符串切片可能选择 query 内的斜杠，也与新的 URL 策略不一致。

新增：

```go
func checksumURLForAsset(raw string) (string, bool) {
	base, ok := parseDownloadURL(raw)
	if !ok {
		return "", false
	}
	base.RawQuery = ""
	base.ForceQuery = false
	base.Fragment = ""
	base.RawFragment = ""
	return base.ResolveReference(&url.URL{Path: "SHA256SUMS.txt"}).String(), true
}
```

在 `DownloadAndApply` 中：

```go
sumsURL, ok := checksumURLForAsset(info.DownloadURL)
if !ok {
	return nil, errors.New("invalid download URL: <invalid-url>")
}
```

随后继续使用 `sumsURL`。helper 有意保留合法 scheme、host、port、userinfo 和目录 path，删除 asset query/fragment，并仅替换最后一个 path segment。不得在生产错误中输出返回的 URL。

## 6. 完整错误路径清单

`downloadFileWithLimit` 的所有路径均在范围内：

| 顺序 | 路径 | 原始数据风险 | 必须处理方式 |
| --- | --- | --- | --- |
| 1 | URL 解析/策略拒绝 | 原始 URL 和 parser error | 固定 `<invalid-url>` 消息 |
| 2 | `NewRequestWithContext` | 原始 URL/parser 文本 | origin + 固定类别，丢弃 `err` |
| 3 | `client.Do` | 请求 URL、重定向 Location、transport error | origin + 固定类别，丢弃 `err` 文本/链 |
| 4 | 非 200 响应 | 请求/最终响应 URL | 仅状态码 + 原始 origin |
| 5 | `io.ReadAll` | body/transport error 文本 | origin + 固定类别，丢弃 `err` |
| 6 | 大小限制 | 原始 URL | 仅限制值 + origin |
| 7 | 成功 | 无 | 原样返回字节 |

`DownloadAndApply` 的两次下载——归档文件和 `SHA256SUMS.txt`——必须共用该安全路径。

## 7. 必需测试 helper

若没有等价 helper，在 `internal/updater/updater_test.go` 增加：

```go
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

type errorReadCloser struct{ err error }

func (r errorReadCloser) Read([]byte) (int, error) { return 0, r.err }
func (errorReadCloser) Close() error                { return nil }
```

测试必须使用不同标记，避免 Go 自动掩码 password 后产生误通过：

```text
username-secret
password-secret
path-secret
query-key
query-secret
fragment-secret
redirect-secret
transport-secret
body-secret
```

可以共用以下断言 helper：

```go
func assertNoSensitiveMarkers(t *testing.T, got string, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if strings.Contains(got, marker) {
			t.Fatalf("output leaked %q: %s", marker, got)
		}
	}
}
```

## 8. 强制测试矩阵

### 8.1 URL 策略与安全 origin

为 `parseDownloadURL` 和 `redactURLForError` 增加表驱动测试：

| 输入 | 有效 | 安全显示 |
| --- | --- | --- |
| `https://example.com/a` | 是 | `https://example.com` |
| `HTTP://example.com:8080/a?x=y` | 是 | `http://example.com:8080` |
| `https://username-secret:password-secret@example.com/path-secret?query-key=query-secret#fragment-secret` | 是 | `https://example.com` |
| `user:password-secret@example.com/path?query-key=query-secret` | 否 | `<invalid-url>` |
| `ftp://example.com/file` | 否 | `<invalid-url>` |
| `/relative/path?query-key=query-secret` | 否 | `<invalid-url>` |
| `https:///missing-host` | 否 | `<invalid-url>` |
| `https://example.com/%zz?query-key=query-secret` | 否 | `<invalid-url>` |

每个无效用例还要验证返回显示字符串不含任何原始敏感标记。

### 8.2 transport 前拒绝

注入一个会递增 atomic/计数器、且一旦被调用就使测试失败的 `roundTripFunc`。对上面的每个无效 URL 调用 `downloadFileWithLimit`，断言：

- 返回错误；
- 错误精确等于 `invalid download URL: <invalid-url>`；
- transport 调用次数保持为零；
- 不含任何敏感标记。

### 8.3 Transport 错误

注入返回 `errors.New("transport-secret: " + req.URL.String())` 的 transport。使用含所有 URL 标记的合法 URL 调用，断言：

- 错误为 `download request to https://example.com failed`；
- 不含任何 URL 标记、`transport-secret`、`query-key` 或 `token=`；
- helper 直接边界没有可 unwrap 的原始 cause（`errors.Unwrap(err) == nil`）。

### 8.4 取消与超时

分别让 transport 返回 `context.Canceled` 和 `context.DeadlineExceeded`。断言精确固定消息分别以 `was canceled` 和 `timed out` 结尾，且固定类别之外不含底层错误文本。

### 8.5 畸形重定向

使用 `httptest.NewServer` 返回 302 和：

```text
Location: https://redirect.example/%zz?query-key=redirect-secret
```

使用 path/query 同样含标记的原始 URL 调用 `downloadFile`。断言返回错误保留原始 server origin，但不含 `redirect.example`、`%zz`、`query-key` 或 `redirect-secret`。

### 8.6 响应 body 读取错误

注入一个返回 200 response 的 transport，设置 `Body: errorReadCloser{err: errors.New("body-secret")}` 和非 nil `Request`。断言精确固定的读取错误消息，并确认不含 `body-secret` 和所有 URL 标记。

### 8.7 状态码与大小路径

更新现有状态码和大小测试，使其期望 origin-only 输出。测试必须证明 path、query、fragment 和 userinfo 均不存在，同时保留现有最大大小行为断言。

### 8.8 Checksum URL 构造

新增表驱动 `TestChecksumURLForAsset`，覆盖：

```text
https://user:pass@example.com/releases/v1/asset.tar.gz?token=secret#fragment
  -> https://user:pass@example.com/releases/v1/SHA256SUMS.txt

https://example.com/asset.tar.gz
  -> https://example.com/SHA256SUMS.txt

opaque / relative / unsupported scheme
  -> ok == false, result == ""
```

测试可以检查内部 URL 字符串，因为该 helper 生成请求 URL，而不是诊断字符串；生产错误绝不能输出它。

### 8.9 Hermetic `DownloadAndApply` 回归

改写 `TestDownloadAndApplyRedactsInvalidDownloadURL`，注入确定性失败 transport。它必须：

- 不访问任何外网；
- 使用不同的 URL 和 transport 标记；
- 断言上下文前缀 `download asset:` 仍保留；
- 断言唯一保留的 URL 信息是 `https://example.com`；
- 断言所有敏感标记均不存在。

### 8.10 管理 API 端到端回归

在 `internal/admin/update_handler_test.go` 中增加包内 fake `updater.ReleaseSource`，返回有效的新版本，且其 `AssetURL` 指向本地 `httptest` server。让 server 返回 8.5 节的畸形重定向。用 `Server.SetUpdater` 注入 updater，调用 `handleUpdateApply`，解码 `updateApplyResponse` 并断言：

- HTTP 状态为 200，保持现有业务错误行为；
- `Success == false`；
- `Error` 非空并保留安全的高层上下文；
- JSON body 不含重定向、path、query、userinfo 或底层错误标记。

这是实际安全 sink 的验收测试。

## 9. 实现任务

### 任务 1——先写失败的 URL 策略测试

文件：`internal/updater/updater_test.go`

1. 增加第 7 节测试 helper。
2. 用 8.1 节表格替换 `TestRedactURLForError`。
3. 增加 transport 前拒绝和 checksum URL 测试。
4. 运行：

   ```bash
   go test ./internal/updater/ -run 'Test(ParseDownloadURL|RedactURLForError|DownloadFileRejectsUnsafeURL|ChecksumURLForAsset)' -count=1
   ```

5. 实现前预期：因 helper 缺失而编译失败，或因旧行为不符合断言而失败。

### 任务 2——实现 fail-closed URL 解析与 checksum 派生

文件：`internal/updater/updater.go`

1. 增加 `errors` import。
2. 增加 `parseDownloadURL`、`safeURLOrigin` 和修订后的 `redactURLForError`。
3. 增加 `checksumURLForAsset`。
4. 替换 `DownloadAndApply` 中基于 `strings.LastIndex` 的 checksum 派生。
5. 运行任务 1 命令并要求 PASS。

### 任务 3——先写再满足所有下载错误路径测试

文件：`internal/updater/updater.go`、`internal/updater/updater_test.go`

1. 增加 8.3 至 8.9 节的失败测试。
2. 运行并确认 transport、redirect、body 测试至少会因旧的原始错误行为失败。
3. 增加 `requestFailureCategory`，按 5.3 节改写 `downloadFileWithLimit`。
4. 运行：

   ```bash
   go test ./internal/updater/ -run 'TestDownload|TestRequestFailureCategory' -count=1
   ```

5. 要求 PASS，且不依赖外网。

### 任务 4——增加 sink 级覆盖并执行完整验证

文件：`internal/admin/update_handler_test.go`

1. 增加 8.10 节管理 API 畸形重定向回归。
2. 运行聚焦 admin 测试并要求 PASS。
3. 运行第 11 节所有命令。
4. 只有在全部命令通过后，才更新中英文规格进度和 feature review notes。

## 10. 非目标

- 修改 release source 认证或 provider API。
- 移除实际下载请求对合法 userinfo 或签名 query 参数的支持。
- 增加重试、重定向限制、证书变更，或超出明确的 HTTP(S) 绝对 URL 要求之外的 SSRF 策略。
- 修改 checksum 验证、归档解压、二进制替换、重启行为或前端 UI。
- 记录原始错误。任何 debug 模式都不得绕过本脱敏契约。

## 11. 验证与验收

从仓库根目录运行：

```bash
gofmt -w internal/updater/updater.go internal/updater/updater_test.go internal/admin/update_handler_test.go
go test ./internal/updater/ -count=1
go test ./internal/admin/ -run 'TestHandleUpdateApply|TestWriteUpdateApply' -count=1
HTTPS_PROXY=http://127.0.0.1:9 go test ./internal/updater/ -count=1
go test -race ./internal/updater/ ./internal/admin/
go test ./...
```

验收必须同时满足：

1. 所有命令退出码为 0。
2. 聚焦测试不访问公网。
3. 无效 URL 绝不进入 transport。
4. 返回的 updater 错误和管理 API JSON 最多暴露规范化 origin 与固定诊断字段。
5. opaque URL、畸形重定向、transport error、body 读取错误、状态码、大小、取消和超时均有覆盖。
6. 生产错误路径不格式化、也不 unwrap 不可信底层错误。
7. 现有成功下载和 updater 行为继续通过。

## 12. 实现完成检查表

- [x] URL 策略测试在实现前失败、实现后通过。
- [x] `parseDownloadURL` 只接受带 host 的绝对层级 HTTP(S) URL。
- [x] `redactURLForError` 只返回 origin 或 `<invalid-url>`。
- [x] opaque URL 在 transport 前被拒绝。
- [x] checksum URL 使用解析后的 URL resolution，而不是原始字符串切片。
- [x] 请求构造错误丢弃原始错误文本。
- [x] transport 和 redirect 错误丢弃原始错误文本与 unwrap 链。
- [x] body 读取错误丢弃原始错误文本。
- [x] 状态码和大小错误只使用 origin 诊断。
- [x] Hermetic `DownloadAndApply` 测试覆盖所有标记。
- [x] 管理 API sink 级测试覆盖畸形重定向泄露。
- [x] 包测试、race 测试和全仓库测试通过。
