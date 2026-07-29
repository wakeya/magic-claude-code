# 安全修复规格（review HIGH/MEDIUM/LOW）

本地页面：AI 生成 + LLM 客户端 / 后端：`internal/providerquota/` + `internal/admin/` / 最后更新：2026-07-28 / 状态：draft / 进度：0 / 5 planned

## 整体分析

codex review 发现 5 项需合并前修复（HIGH 2 + MEDIUM 2 + LOW 1）。本规格逐一修复，MEDIUM 3（goja 资源）风险低留 follow-up。

## 任务详情

### 任务 1（HIGH）：LLM 凭据脱敏——request_info 发 LLM 前脱敏

#### 问题
前端指引让用户贴完整抓包（含 Cookie/Authorization/sec_token），`buildUserMessage` + `buildFixMessage` 原样发 LLM。违反 spec §185（LLM 不携带上游凭据）。

#### 修复
**文件：`internal/providerquota/script_generator.go`**

1. 新增 `sanitizeRequestInfo(info string) string`：对 request_info 脱敏，**保留字段名/位置**（LLM 仍知道"这里有 Cookie"），只替换值为 `[REDACTED]`。规则（大小写不敏感，正则）：
   - `(?i)(cookie\s*:\s*)([^\r\n]+)` → `${1}[REDACTED]`
   - `(?i)(authorization\s*:\s*)([^\r\n]+)` → `${1}[REDACTED]`
   - `(?i)(bearer\s+)([A-Za-z0-9._\-/+=]+)` → `${1}[REDACTED]`
   - `(?i)(sec_token\s*=\s*)([^\r\n&]+)` → `${1}[REDACTED]`
   - `(?i)(api[_-]?key\s*=\s*)([^\r\n&]+)` → `${1}[REDACTED]`
   - `(?i)(access[_-]?token\s*=\s*)([^\r\n&]+)` → `${1}[REDACTED]`
2. `buildUserMessage(req)` 内 `info` 用 `sanitizeRequestInfo(strings.TrimSpace(req.RequestInfo))`。
3. `buildFixMessage(script, warnings, req)` 内 request_info 同样脱敏。
4. **注意**：脱敏只作用于发给 LLM 的文本；用户原始 `req.RequestInfo` 不改（自检 `auditScript` 仍用原值判断"含 cookie"——它只看字段名存在，不看值，所以脱敏前后都触发；这里保持原值给 audit，脱敏值给 LLM）。

**测试：`script_generator_test.go`**
5. `TestSanitizeRequestInfo`：含 `Cookie: sid=secret` / `Authorization: Bearer abc.def` / `sec_token=xyz` / `api_key=k` 的输入 → 输出含 `[REDACTED]` 且**不含** `secret`/`abc.def`/`xyz`/`k`；保留 `cookie:`/`authorization:`/`sec_token=`/`api_key=` 字段名。
6. `TestBuildUserMessageRedactsCredentials`：buildUserMessage 输出不含原 Cookie 值。
7. `TestBuildFixMessageRedactsCredentials`：buildFixMessage 同样脱敏。

---

### 任务 2（HIGH）：LLMClient SSRF 防护——禁重定向 + 内网拦截

#### 问题
`LLMClient` 用普通 `http.Client.Do`，无重定向校验 + 无内网地址拦截。恶意 endpoint 可 SSRF（localhost/内网/metadata）。

#### 修复
**文件：`internal/providerquota/llm_client.go`**

1. `NewLLMClient` 的 `http.Client.CheckRedirect` 设为**禁用自动重定向**（返回 `http.ErrUseLastResponse`）——LLM 调用不应跟随重定向（合法 LLM endpoint 不重定向；重定向即可疑）。
2. 新增 `isInternalHost(host string) bool`（或复用 `internal/proxy` / `token_plan.go` 已有的内网判断；查现有代码复用）：解析 host，若为 loopback / private（10/172.16/192.168）/ link-local（169.254，含云 metadata 169.254.169.254）/ unspecified（0.0.0.0）→ true。
3. `Call` 在构造请求后、`client.Do` 前：解析 `endpoint` 的 host，`isInternalHost` → 返回 `LLMCallResult{ErrorCode: "invalid_config", ErrorMessage: "LLM endpoint resolves to an internal address"}`
4. 错误信息不含完整 URL（只稳定文案）。

**测试：`llm_client_test.go`**
5. `TestLLMClientRejectsLoopback`：APIURL=`http://127.0.0.1:...` → `invalid_config`。
6. `TestLLMClientRejectsMetadata`：`http://169.254.169.254` → `invalid_config`。
7. `TestLLMClientRejectsRedirect`：server 返回 302 到另一 host → 不跟随（返回 upstream_http_error 或原始 302 响应，不调重定向目标）。

---

### 任务 3（MEDIUM）：错误响应不回显 LLM 原文/上游 body

#### 问题
`GenerateScript` 的 `invalid_response`/`script_error` ErrorMessage 含 `summarizeLLMText(LLM 原文)`；`LLMClient` 4xx/5xx 含上游 body 摘要。若 LLM 回显 Cookie/sec_token，会进 HTTP 响应。

#### 修复
**文件：`internal/providerquota/script_generator.go`**
1. `extractScript` 失败 → `ErrorMessage: "LLM output does not contain a recognizable object literal"`（去掉 `first N bytes: <原文>`）。
2. `parseRequest` 失败 → `ErrorMessage: "generated script failed to parse request"`（去掉 `; llm_output=<原文>`）。

**文件：`internal/providerquota/llm_client.go`**
3. 4xx/5xx → `ErrorMessage: fmt.Sprintf("HTTP %d", resp.StatusCode)`（去掉 `: <body 摘要>`）。服务端日志可保留摘要（`log.Printf`），但 `LLMCallResult.ErrorMessage` 不含。

**测试**
4. `TestGenerateScriptParseErrorNoLeak`：LLM 返回回显了 "Cookie: secret" 的文本 + 解析失败 → `result.ErrorMessage` 不含 "secret"。
5. `TestLLMClient404NoBodyLeak`：上游 404 body 含 "sensitive" → `ErrorMessage` 不含 "sensitive"（只 "HTTP 404"）。

---

### 任务 4（MEDIUM）：generate-script 拒绝 disabled provider

#### 问题
`llm_provider_id` 可指定任意 provider，没检查 `Enabled`。disabled provider 仍可被用来调 LLM。

#### 修复
**文件：`internal/admin/provider_quota_handler.go`**
1. `isConfiguredLLMProvider(provider)`（或 handler 选 provider 后）加 `provider.Enabled` 检查：disabled → 400 `invalid_config`（"LLM provider is disabled"）。

**测试：`provider_quota_handler_test.go`**
2. `TestGenerateScriptDisabledProvider`：provider `Enabled: false` → 400 `invalid_config`。

---

### 任务 5（LOW）：generate-script 请求体大小限制

#### 问题
无 `MaxBytesReader` + 字段长度限制。超大 JSON DoS + 超大 prompt 转 LLM。

#### 修复
**文件：`internal/admin/provider_quota_handler.go`**
1. `handleGenerateUsageScript` 开头：`r.Body = http.MaxBytesReader(w, r.Body, 256*1024)`（总 256KB）。
2. 解析 DTO 后校验字段长度：`prompt` ≤ 8KB / `response_sample` ≤ 32KB / `request_info` ≤ 16KB / `model` ≤ 128B。超限 → 400 `invalid_config`（"field exceeds size limit"）。

**测试**
3. `TestGenerateScriptOversizedBody`：prompt > 8KB → 400。
4. `TestGenerateScriptTotalBodyLimit`：整体 > 256KB → 400（MaxBytesReader 触发）。

---

## 验证

- [ ] `go test -v -race ./internal/providerquota/ ./internal/admin/ -count=1` 全绿。
- [ ] `go vet ./...` 干净。
- [ ] 所有新增测试覆盖 5 项修复。
- [ ] commit: `fix(ai-generate): security fixes (credential redaction, SSRF, error leak, disabled provider, size limits)`。

## Follow-up（不在本规格）

- MEDIUM 3（goja parseRequest 资源风险）：静态解析替代执行，或受限 worker。风险低（goja 无 require/process/file + 200ms interrupt + admin 鉴权），留独立 follow-up。
