# Custom 脚本 AI 多轮自检修正规格

本地页面：AI 生成对话框（`ScriptGeneratorModal.vue`）/ 后端：`internal/providerquota/script_generator.go` + `internal/admin/provider_quota_handler.go` / 技术栈：Go + Vue 3 + TS / 最后更新：2026-07-28 / 状态：draft / 进度：0 / 2 planned

## 整体分析

### 目标

**A. 警告加「→ 修复」建议**：自检警告从纯英文描述升级为结构化（code + i18n 中文 + 具体修复步骤 + 代码片段），用户看警告就知道怎么改。

**B. AI 多轮自检修正**：生成 → 自检 → 有警告则把「原脚本 + 警告 + 上下文」喂回 LLM 让它修 → 再自检，循环最多 10 轮，直到无警告或耗尽。让 AI 自己修掉漏 Cookie / `response.body` / 空 body 等常见错误，逼近"一次成功"。

### 设计

#### A. 警告结构化

`auditScript` 返回 `[]AuditWarning{Code string, Message string}`（Message 是英文简述，Code 供前端 i18n 映射）。前端按 Code 翻译成中英文案（含「→ 修复」建议 + 代码片段）。

7 个 warning code（前端 i18n key `warning.<code>`）：

| code | 触发条件 | 中文文案 + 修复建议（i18n） |
|---|---|---|
| `missing_cookie` | 请求信息含 `cookie:` 但脚本无 `Cookie` header | 请求信息含 Cookie，但脚本没设置 Cookie header，鉴权大概率失败。→ 修复：在 `request.headers` 加 `"Cookie": "{{apiKey}}"`，并在「Script API Key」字段填完整 Cookie 值 |
| `missing_authorization` | 请求信息含 `authorization:`/`bearer` 但脚本无 `Authorization` | 请求信息含 Authorization/Bearer，但脚本没设置 Authorization header。→ 修复：在 `request.headers` 加 `"Authorization": "Bearer {{apiKey}}"`，并在「Script API Key」字段填 token |
| `missing_sec_token` | 请求信息含 `sec_token` 但脚本 body/URL 无 `sec_token` | 请求信息含 sec_token，但脚本 body/URL 里没有。→ 修复：在 `request.body` 加 `sec_token: "{{apiKey2}}"`，并在「附加密钥（apiKey2）」字段填 sec_token 值 |
| `response_body_misuse` | 脚本含 `response.body` 或 `JSON.parse(response` | 脚本用了 `response.body` 或 `JSON.parse(response)`——extractor 收到的 response 已是解析好的 JSON 对象。→ 修复：直接用 `response.xxx`（如 `response.data.DataV2.data.data`），不要 `.body` 或 `JSON.parse` |
| `empty_post_body` | POST 但 `body: {}` | POST 请求的 body 为空 `{}`，必填字段大概率缺失。→ 修复：按请求信息的 Form Data 在 body 里补字段 |
| `no_credential_placeholder` | 脚本无 `{{apiKey}}`/`{{apiKey2}}`/`{{accessToken}}`/`{{userId}}` | 脚本没用任何凭据占位符，配置的密钥不会注入。→ 修复：在 headers（如 `"Cookie": "{{apiKey}}"`）或 body（如 `sec_token: "{{apiKey2}}"`）加占位符 |
| `hardcoded_url` | 脚本 `url:` 无 `{{baseUrl}}` | 脚本 URL 没用 `{{baseUrl}}` 占位符，同源校验可能拒绝。→ 修复：把 URL 改成 `{{baseUrl}}/path`（如 `{{baseUrl}}/data/api.json?...`） |

英文文案在 `useI18n.ts` 的 `en` 段同样维护（同 key，英文翻译，同样含修复建议）。

#### B. 多轮迭代修正

`GenerateScript` 改为多轮（最多 `maxIterations`，默认 10）：

```text
1. 首轮：LLM.Call(systemPromptForScript(), buildUserMessage(req)) → extractScript → parseRequest → auditScript → warnings
2. while len(warnings) > 0 && iter < maxIterations:
   a. fixMsg = buildFixMessage(script, warnings, req)  // 原脚本 + 警告 + 原上下文
   b. call = LLM.Call(systemPromptForFix(), fixMsg)
   c. LLM 错误 → 返回当前 script + warnings + iter（保留上一轮成果）
   d. newScript = extractScript(call.Text)  // 解析失败 → 返回当前 script + warnings + iter
   e. parseRequest(newScript) 失败 → 返回当前 script + warnings + iter
   f. script = newScript; warnings = auditScript(req.RequestInfo, newScript); iter++
3. 返回 GenerateScriptResult{Script: script, Warnings: warnings, Iterations: iter}
```

**`systemPromptForFix()`**（修复模式系统提示，基于 `systemPromptForScript()` 加一段）：

```text
<systemPromptForScript 全文>

You are now in FIX mode. The user previously generated a script, but an automated audit found the issues below. Return a COMPLETE corrected script (same `({request, extractor})` format) that fixes every listed issue. Preserve the working parts of the previous script. Do not regress fields that were correct.
```

**`buildFixMessage(script, warnings, req)`**（喂回 LLM 的用户消息）：

```text
Previous script:
<script 全文>

Audit warnings (fix ALL of them):
- [<code>] <message>
- ...

Original need: <req.Prompt>
Original response sample (authoritative for extractor field paths):
<req.ResponseSample>
Request info (where the credentials live):
<req.RequestInfo 或 "(not provided)">

Return the corrected full script.
```

**终止条件**：
- `warnings` 空 → 成功，返回（iter = 实际轮数）
- `iter == maxIterations`（10）→ 返回当前 script + 剩余 warnings（用户手动修）
- 中途 LLM 调用错 / extractScript 错 / parseRequest 错 → 返回**上一轮成功的** script + warnings + iter（不丢成果）

**`GenerateScriptResult` 加 `Iterations int`**（实际迭代次数，首轮 = 1）。

### API 响应

`generate-script` 响应加：
- `iterations: number`（实际轮数）
- `warnings` 从 `[]string` 改为 `[]{code: string, message: string}`（结构化）

### 前端

- `GenerateScriptResponse.warnings` 改为 `Array<{code: string; message: string}>`，加 `iterations?: number`
- 警告区：按 `warning.code` i18n 翻译显示（中英，含修复建议）；`code` 未识别时 fallback 显示 `message`
- 顶部显示「AI 迭代 N 轮」（`iterations > 1` 时显示）

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | Planned | 后端 auditScript 结构化 + GenerateScript 多轮 | `script_generator.go` + 测试 | TestAuditScript 返回 Code；TestGenerateScriptMultiRound 迭代到无警告 |
| 2 | Planned | handler 结构化响应 + 前端 i18n + iterations 显示 | `provider_quota_handler.go` + `ScriptGeneratorModal.vue` + `useApi.ts` + `useI18n.ts` + 测试 | 前端测试 warnings 翻译 + iterations |

## 任务详情

### 任务 1：后端 auditScript 结构化 + GenerateScript 多轮

#### 需求

**Objective（目标）** — `auditScript` 返回 `[]AuditWarning{Code, Message}`（7 个 code）；`GenerateScript` 加多轮迭代（默认 10 轮），每轮把脚本+警告喂回 LLM 修正，返回 `Iterations`。

**Outcomes（成果）** — `internal/providerquota/script_generator.go` + `script_generator_test.go`。

**Evidence（证据）** — `TestAuditScript` 断言每类返回正确 Code；`TestGenerateScriptMultiRound`：mock LLM 第一轮返回漏 Cookie 脚本、第二轮返回补 Cookie 脚本 → 断言 `result.Script` 含 Cookie、`result.Warnings` 空、`result.Iterations == 2`；`TestGenerateScriptMaxIterations`：mock LLM 始终返回有警告脚本 → 断言 `Iterations == 10` + 返回最后一轮 script；`TestGenerateScriptFixLLMError`：第二轮 LLM 失败 → 返回首轮 script + warnings + Iterations=1。

**Constraints（约束）** — 多轮不丢失成果（中途错返回上一轮）；首轮 LLM 错仍返回 ErrorCode（不进迭代）；maxIterations=10；`systemPromptForFix` 复用 `systemPromptForScript` + 修复段；每轮 LLM 调用受 ctx/timeout 控制（用整体 timeout，不是每轮单独——首版整体 timeout 30s 覆盖所有轮，超时则返回当前成果）。

**Edge Cases（边界）** — 首轮就无警告（Iterations=1，不迭代）；首轮 LLM 错（返回 ErrorCode，不迭代）；中途某轮 LLM/解析错（返回上一轮成果）；达到 maxIterations（返回最后一轮 + 剩余 warnings）。

**Verification（验证）** — `go test -v -race ./internal/providerquota/ -run 'AuditScript|GenerateScript'`。

#### 计划

**文件：`internal/providerquota/script_generator.go`**

1. 新增类型：
   ```go
   type AuditWarning struct {
       Code    string
       Message string
   }
   ```
2. `auditScript` 返回类型从 `[]string` 改为 `[]AuditWarning`（每个 warning 带 Code，Code 取值：`missing_cookie`/`missing_authorization`/`missing_sec_token`/`response_body_misuse`/`empty_post_body`/`no_credential_placeholder`/`hardcoded_url`；Message 保留现有英文描述）。
3. `GenerateScriptResult.Warnings` 类型从 `[]string` 改为 `[]AuditWarning`；加 `Iterations int` 字段。
4. `GenerateScript` 重构为多轮：
   ```go
   func GenerateScript(ctx context.Context, llm *LLMClient, provider LLMProvider, req GenerateScriptRequest, timeout time.Duration) GenerateScriptResult {
       // 校验 + timeout 设置（同现有）
       maxIter := 10
       // 首轮
       call := llm.Call(callCtx, provider, model, systemPromptForScript(), buildUserMessage(req))
       if call.ErrorCode != "" { return ...ErrorCode... }
       script, err := extractScript(call.Text)
       if err != nil { return ...invalid_response... }
       if _, err := (&ScriptExecutor{}).parseRequest(script); err != nil { return ...script_error... }
       warnings := auditScript(req.RequestInfo, script)
       iter := 1
       // 迭代修正
       for len(warnings) > 0 && iter < maxIter {
           fixMsg := buildFixMessage(script, warnings, req)
           fixCall := llm.Call(callCtx, provider, model, systemPromptForFix(), fixMsg)
           if fixCall.ErrorCode != "" { break }  // 保留上一轮
           newScript, err := extractScript(fixCall.Text)
           if err != nil { break }
           if _, err := (&ScriptExecutor{}).parseRequest(newScript); err != nil { break }
           script = newScript
           warnings = auditScript(req.RequestInfo, newScript)
           iter++
       }
       return GenerateScriptResult{Script: script, Warnings: warnings, Iterations: iter}
   }
   ```
5. 新增 `systemPromptForFix()` = `systemPromptForScript() + 修复段（见 §设计）`。
6. 新增 `buildFixMessage(script string, warnings []AuditWarning, req GenerateScriptRequest) string`（见 §设计模板）。

**测试文件：`internal/providerquota/script_generator_test.go`**

7. `TestAuditScript` 断言每个 case 的 warnings[0].Code 正确（替换原断言 wantSubstrs 为 wantCodes）。
8. `TestGenerateScriptMultiRound`：httptest server 首次响应漏 Cookie 脚本（warnings 含 missing_cookie），第二次响应补 Cookie 脚本（warnings 空）→ 断言最终 script 含 Cookie、Warnings 空、Iterations==2。
9. `TestGenerateScriptMaxIterations`：server 始终返回有警告脚本 → 断言 Iterations==10 + 返回最后一轮 script。
10. `TestGenerateScriptFixLLMError`：server 首次正常，第二次返回 401 → 断言返回首轮 script + warnings + Iterations==1。
11. `TestGenerateScriptNoWarningsFirstRound`：server 首次返回干净脚本 → 断言 Iterations==1（不迭代）。

#### 验证

- [ ] `go test -v -race ./internal/providerquota/ -run 'AuditScript|GenerateScript'` 全绿。

---

### 任务 2：handler 结构化响应 + 前端 i18n + iterations 显示

#### 需求

**Objective（目标）** — handler 响应 warnings 改为 `[]{code, message}` 结构 + 加 `iterations`；前端 `ScriptGeneratorModal.vue` 按 `warning.code` i18n 翻译显示（中英 + 修复建议），顶部显示迭代次数。

**Outcomes（成果）** — `internal/admin/provider_quota_handler.go` + `internal/frontend/src/components/ScriptGeneratorModal.vue` + `composables/useApi.ts` + `composables/useI18n.ts` + 测试。

**Evidence（证据）** — 前端测试：mock 返回 `warnings: [{code: 'missing_cookie', message: '...'}]` + `iterations: 3` → 警告区显示中文「请求信息含 Cookie...」（i18n 翻译）+ 顶部「AI 迭代 3 轮」；未知 code → fallback 显示 message。

**Constraints（约束）** — i18n 中英都加 7 个 `warning.<code>` 文案（含修复建议）；`iterations > 1` 才显示迭代次数；`iterations == 1` 显示「AI 生成完成」（或隐藏）。

**Edge Cases（边界）** — warnings 空（不显示警告区）；未知 code（fallback message）；iterations=1（首轮成功，不显示迭代次数或显示「首轮成功」）。

**Verification（验证）** — `go test -v -race ./internal/admin/ -run GenerateScript` + `npm --prefix internal/frontend test` + `npm run build`。

#### 计划

**文件：`internal/admin/provider_quota_handler.go`**

1. 响应 DTO：`Warnings []struct{Code string \`json:"code"\`; Message string \`json:"message"\`} \`json:"warnings"\``；加 `Iterations int \`json:"iterations"\``。
2. `handleGenerateUsageScript` 成功路径填入 `result.Warnings`（转 DTO）+ `result.Iterations`。

**文件：`internal/frontend/src/composables/useApi.ts`**

3. `GenerateScriptResponse`：`warnings?: Array<{code: string; message: string}>`；加 `iterations?: number`。

**文件：`internal/frontend/src/composables/useI18n.ts`**

4. 中英段各加 8 个 key：
   - `quota.ai_generate_iterations`：「AI 迭代 {n} 轮」/「AI iterated {n} round(s)」
   - `warning.missing_cookie` / `warning.missing_authorization` / `warning.missing_sec_token` / `warning.response_body_misuse` / `warning.empty_post_body` / `warning.no_credential_placeholder` / `warning.hardcoded_url`（中英全文见 §设计表格；含「→ 修复」建议 + 代码片段）

**文件：`internal/frontend/src/components/ScriptGeneratorModal.vue`**

5. `generate()` 成功后：存 `warnings`（结构化）+ `iterations`。
6. 模板：
   - 顶部（标题下）：`iterations > 1` 时显示「AI 迭代 N 轮」徽章。
   - 警告区：`v-for` warnings，每条显示 `t('warning.' + w.code)`（i18n 翻译）；key 未命中（`t` 返回 `warning.xxx` 原文）→ fallback 显示 `w.message`。

**测试文件：`internal/frontend/src/components/ScriptGeneratorModal.test.ts`**

7. 测试：
   - mock 返回 `{warnings:[{code:'missing_cookie',message:'...'}], iterations:3}` → 警告区显示中文 missing_cookie 文案 + 顶部「AI 迭代 3 轮」。
   - mock 返回未知 code `{code:'unknown_x', message:'some msg'}` → 显示 'some msg'（fallback）。
   - mock 返回 `{warnings:[], iterations:1}` → 警告区不显示 + 不显示迭代次数。

#### 验证

- [ ] `go test -v -race ./internal/admin/ -run GenerateScript` 全绿。
- [ ] `npm --prefix internal/frontend test` 全绿。
- [ ] `npm --prefix internal/frontend run build` 成功。
- [ ] 中英 i18n 都含 7 个 warning code + iterations key。
