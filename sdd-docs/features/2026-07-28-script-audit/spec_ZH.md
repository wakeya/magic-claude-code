# Custom 脚本自检规格

本地页面：AI 生成对话框（`ScriptGeneratorModal.vue`）/ 后端：`internal/providerquota/script_generator.go` + `internal/admin/provider_quota_handler.go` / 技术栈：Go + Vue 3 + TS / 最后更新：2026-07-28 / 状态：draft / 进度：0 / 2 planned

## 整体分析

### 目标

AI 生成脚本后，后端自动扫描用户「请求信息」文本 + 生成脚本，检测 AI 常见错误（漏 Cookie / `response.body` 误用 / POST 空 body / 无凭据占位符 / 硬编码 URL），返回警告清单。前端在脚本回填后显示警告，让用户**立即知道脚本哪里可能有问题**，不用测失败再人工诊断。

### 背景

AI 生成对复杂接口反复失败，根因都是"AI 从长篇 free-text 抓包提取关键信息不可靠"：
- 漏 Cookie（Cookie 在 request headers 里，AI 注意力漏了）
- `response.body` + `JSON.parse(response)`（fetch 语义污染）
- POST + 空 body `{}`
- 无凭据占位符（`{{apiKey}}`/`{{apiKey2}}` 都没用）
- 硬编码 URL（没用 `{{baseUrl}}`）

自检把这些常见错误模式检测出来，警告用户。

### 设计

1. **后端 `auditScript(requestInfo, script) []string`**：扫描请求信息 + 脚本，返回警告字符串数组（空 = 干净）。
2. **`GenerateScriptResult` 加 `Warnings []string`**：`GenerateScript` 在 parseRequest 预验证后调用 `auditScript`，填入 `result.Warnings`。
3. **handler 响应加 `warnings`**：`generate-script` 响应 JSON 加 `warnings: string[]`。
4. **前端**：`GenerateScriptResponse` 加 `warnings?: string[]`；脚本回填后，若 warnings 非空，在对话框显示黄色警告区（列出每条）；**不阻塞**（脚本仍回填，用户可继续/修改/重生成）。

### 自检规则（auditScript 全文）

```go
// auditScript scans the user's request info and the generated script for
// common AI mistakes and returns human-readable warnings (empty if clean).
// Warnings are advisory; the script is still returned for the user to use or fix.
func auditScript(requestInfo, script string) []string {
    var warnings []string
    ri := strings.ToLower(requestInfo)
    sc := script // case-sensitive for placeholders

    // 1. Credential in request info but missing from script
    if (strings.Contains(ri, "cookie:") || strings.Contains(ri, "cookie =")) &&
        !strings.Contains(strings.ToLower(sc), "cookie") {
        warnings = append(warnings, "request info contains a Cookie header but the script does not set Cookie — authentication will likely fail")
    }
    if (strings.Contains(ri, "authorization:") || strings.Contains(ri, "bearer ")) &&
        !strings.Contains(strings.ToLower(sc), "authorization") {
        warnings = append(warnings, "request info contains Authorization/Bearer but the script does not set the Authorization header")
    }
    if strings.Contains(ri, "sec_token") && !strings.Contains(sc, "sec_token") {
        warnings = append(warnings, "request info contains sec_token but the script does not include a sec_token field in body/url")
    }

    // 2. response fetch-API misuse (response is already a parsed JSON object)
    if strings.Contains(sc, "response.body") || strings.Contains(sc, "JSON.parse(response") {
        warnings = append(warnings, "script uses response.body or JSON.parse(response) — the extractor receives an already-parsed JSON object; use response.xxx directly")
    }

    // 3. POST with empty body
    if strings.Contains(strings.ToLower(sc), "\"post\"") || strings.Contains(strings.ToLower(sc), "'post'") {
        if strings.Contains(sc, "body: {}") || strings.Contains(sc, "body:{}") {
            warnings = append(warnings, "POST request with empty body {} — required fields are likely missing")
        }
    }

    // 4. No credential placeholder (configured secrets won't be injected)
    if !strings.Contains(sc, "{{apiKey}}") && !strings.Contains(sc, "{{apiKey2}}") &&
        !strings.Contains(sc, "{{accessToken}}") && !strings.Contains(sc, "{{userId}}") {
        warnings = append(warnings, "script uses no credential placeholder ({{apiKey}}/{{apiKey2}}/{{accessToken}}); configured secrets will not be injected")
    }

    // 5. Hardcoded URL (no {{baseUrl}}) — same-origin check may reject
    if (strings.Contains(sc, "url:") || strings.Contains(sc, "url :")) && !strings.Contains(sc, "{{baseUrl}}") {
        warnings = append(warnings, "script URL does not use the {{baseUrl}} placeholder; the same-origin check may reject it")
    }

    return warnings
}
```

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | Planned | 后端 auditScript + Warnings + handler 响应 | `script_generator.go` + `provider_quota_handler.go` | `auditScript` 测试覆盖 5 类规则 |
| 2 | Planned | 前端 warnings 显示 | `ScriptGeneratorModal.vue` + `useApi.ts` + `useI18n.ts` | 组件测试 warnings 渲染 |

## 任务详情

### 任务 1：后端 auditScript + Warnings + handler

#### 需求

**Objective（目标）** — `script_generator.go` 加 `auditScript` 函数 + `GenerateScriptResult.Warnings` 字段；`GenerateScript` 在 parseRequest 预验证后调用 `auditScript` 填充 warnings；`provider_quota_handler.go` 的 `generate-script` 响应加 `warnings`。

**Outcomes（成果）** — `internal/providerquota/script_generator.go` + `script_generator_test.go` + `internal/admin/provider_quota_handler.go`。

**Evidence（证据）** — `auditScript` 测试覆盖 5 类规则（cookie/auth/sec_token、response.body、空 body、无占位符、硬编码 URL）；干净脚本返回空 warnings；handler 响应含 warnings 字段。

**Constraints（约束）** — warnings 是建议性的，不阻塞脚本返回（即使有 warnings 也返回 script + warnings）；warnings 文案英文（首版不 i18n，前端原样显示）；`auditScript` 不影响现有错误码路径（LLM 失败/解析失败仍返回 ErrorCode，不调 audit）。

**Edge Cases（边界）** — requestInfo 为空（只检查脚本本身规则 2-5）；script 为空（audit 不调用，因为前面已返回错误）；干净脚本（warnings=[]）。

**Verification（验证）** — `go test -v -race ./internal/providerquota/ -run 'AuditScript|ScriptGenerator'` + `go test -v -race ./internal/admin/ -run GenerateScript`。

#### 计划

**文件：`internal/providerquota/script_generator.go`**

1. 加 `auditScript(requestInfo, script string) []string` 函数（全文见 §自检规则）。
2. `GenerateScriptResult` struct 加字段 `Warnings []string`（json tag 在 handler DTO 映射，本身 struct 不需 json tag）。
3. `GenerateScript` 在 parseRequest 预验证通过后、`return GenerateScriptResult{Script: script}` 之前，加：
   ```go
   warnings := auditScript(req.RequestInfo, script)
   return GenerateScriptResult{Script: script, Warnings: warnings}
   ```

**文件：`internal/admin/provider_quota_handler.go`**

4. `handleGenerateUsageScript` 的成功响应 DTO 加 `Warnings []string \`json:"warnings"\``；把 `result.Warnings` 填入响应。

**测试文件：`internal/providerquota/script_generator_test.go`**

5. `TestAuditScript`（table-driven）覆盖：
   - 干净脚本（用千问样例：Cookie={{apiKey}} + sec_token={{apiKey2}} + {{baseUrl}} + 无 response.body）→ `len(warnings)==0`
   - requestInfo 含 `cookie:` 但脚本无 Cookie → 1 条 cookie 警告
   - requestInfo 含 `authorization: bearer` 但脚本无 Authorization → 1 条
   - requestInfo 含 `sec_token` 但脚本无 → 1 条
   - 脚本含 `response.body` → 1 条 response 警告
   - 脚本含 `JSON.parse(response` → 1 条
   - POST + `body: {}` → 1 条空 body 警告
   - 无任何 `{{apiKey}}/{{apiKey2}}/{{accessToken}}/{{userId}}` → 1 条占位符警告
   - URL 含 `url:` 但无 `{{baseUrl}}` → 1 条
   - 多问题组合 → 多条警告
6. `TestGenerateScript_PopulatesWarnings`：用 mock LLM 返回漏 Cookie 的脚本 + requestInfo 含 cookie → 断言 `result.Warnings` 非空且含 "Cookie"。

**测试文件：`internal/admin/provider_quota_handler_test.go`**

7. 扩展 `TestGenerateScriptSuccess` 或新增：响应含 `warnings` 字段（成功路径 warnings 可为空或非空，断言字段存在）。

#### 验证

- [ ] `go test -v -race ./internal/providerquota/ -run 'AuditScript|ScriptGenerator'` 全绿。
- [ ] `go test -v -race ./internal/admin/ -run GenerateScript` 全绿。

---

### 任务 2：前端 warnings 显示

#### 需求

**Objective（目标）** — `ScriptGeneratorModal.vue` 在脚本回填后显示后端返回的 warnings（黄色警告区）；`useApi.ts` 加 warnings 类型；`useI18n.ts` 加警告区标题文案。

**Outcomes（成果）** — `internal/frontend/src/components/ScriptGeneratorModal.vue` + `composables/useApi.ts` + `composables/useI18n.ts` + 测试。

**Evidence（证据）** — 组件测试：mock API 返回 warnings → 警告区渲染每条；warnings 为空 → 警告区不显示。

**Constraints（约束）** — warnings 非空时**仍 emit generated + 回填脚本**（不阻塞，用户可改/测/重生成）；警告区黄色背景；warnings 原样显示（英文，不翻译）；可折叠/收起可选（首版直接展开）。

**Edge Cases（边界）** — warnings 空（不显示区）；多条（全列）；含特殊字符（前端转义，Vue `{{ }}` 默认转义）。

**Verification（验证）** — `npm --prefix internal/frontend test` + `npm run build`。

#### 计划

**文件：`internal/frontend/src/composables/useApi.ts`**

1. `GenerateScriptResponse` 加 `warnings?: string[]`。

**文件：`internal/frontend/src/components/ScriptGeneratorModal.vue`**

2. 加状态 `const warnings = ref<string[]>([])`。
3. `generate()` 成功后：`warnings.value = response.warnings || []`，然后 emit generated + close（warnings 不阻塞）。**或**：不 close，停留在对话框显示 warnings + 脚本已回填提示。首版选：**warnings 非空时不 close**，显示"脚本已生成，但有警告："+ warnings + 「关闭」按钮（用户看完关）。
4. 模板加警告区（在 error 区附近）：
   ```vue
   <div v-if="warnings.length > 0" class="text-sm rounded-md p-3" style="background: rgba(234, 179, 8, 0.15); color: rgb(161, 98, 7);">
     <div class="font-medium mb-1">{{ t('quota.ai_generate_warnings') }}</div>
     <ul class="list-disc ml-5 space-y-1">
       <li v-for="(w, i) in warnings" :key="i" class="text-xs">{{ w }}</li>
     </ul>
   </div>
   ```

**文件：`internal/frontend/src/composables/useI18n.ts`**

5. 加 key（中英）：
   - `quota.ai_generate_warnings`：「脚本已生成，但检测到以下问题（可修改后测试）：」/「Script generated, but the following issues were detected (you can fix and test):」

**测试文件：`internal/frontend/src/components/ScriptGeneratorModal.test.ts`**

6. 加测试：
   - mock `generateUsageScript` 返回 `{ script: '...', warnings: ['request info contains Cookie...'] }` → 警告区渲染该文案。
   - mock 返回 `{ script: '...', warnings: [] }` → 警告区不显示。

#### 验证

- [ ] `npm --prefix internal/frontend test` 全绿。
- [ ] `npm --prefix internal/frontend run build` 成功。
- [ ] 手测：AI 生成漏 Cookie 的脚本 → 对话框显示 Cookie 警告。
