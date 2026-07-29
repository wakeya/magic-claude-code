# Custom 脚本样例库规格

本地页面：AI 生成对话框（`ScriptGeneratorModal.vue`）右侧 / 技术栈：Vue 3 + TypeScript + Tailwind / 最后更新：2026-07-28 / 状态：draft / 进度：0 / 2 planned

## 整体分析

### 目标

AI 生成对话框右侧加「参考脚本样例」折叠区，内置 3 个示例脚本，每个可展开查看 + 一键复制到剪贴板。帮助用户：
1. 参考 body 结构（尤其复杂接口的 `product`/`action`/`sec_token`/`params`）—— 在「请求信息」框里照填，或理解 AI 生成脚本的构成；
2. 直接复制样例到脚本编辑器，跳过 AI 生成（适合已知接口）。

### 设计决策

1. **数据来源**：前端 hardcoded `const scriptExamples`（不落后端 API；样例是静态教学内容，无需运行时获取）。
2. **UI 位置**：`ScriptGeneratorModal.vue` 主表单右侧（宽屏右栏，窄屏 `max-w-[760px]` 收窄时样例区移到主表单下方）。模态宽度从 `max-w-[760px]` 加宽到 `max-w-[1100px]` 以容纳双栏。
3. **每样例卡片**：标题 + 一行说明 + 「展开/收起」切换 + 展开时显示 `<pre>` 脚本（等宽、横向滚动）+ 「复制」按钮。
4. **复制**：`navigator.clipboard.writeText(script)`；复制后按钮文案短暂变「已复制 ✓」（2 秒后恢复），失败 fallback 用临时 textarea + `document.execCommand('copy')`。
5. **i18n**：样例标题、说明、复制/已复制按钮文案双语（`useI18n.ts`）。

### 样例内容（hardcoded，三份全文见下）

样例脚本直接嵌入组件 const，**不得**偏离下方全文（含占位符 `{{baseUrl}}`/`{{apiKey}}`/`{{apiKey2}}`、`bodyType:"form"`、注释）。

## 任务详情

### 任务 1：前端样例区 + 复制逻辑

#### 需求

**Objective（目标）** — `ScriptGeneratorModal.vue` 加右侧「参考脚本样例」区，渲染 3 个 hardcoded 样例，每个可展开 + 复制到剪贴板。

**Outcomes（成果）** — `internal/frontend/src/components/ScriptGeneratorModal.vue` + `composables/useI18n.ts`。

**Evidence（证据）** — 组件测试：3 个样例标题渲染；点击复制按钮调用 `navigator.clipboard.writeText` 并显示「已复制」反馈。

**Constraints（约束）** — 样例脚本字符串与 §"样例内容" 全文一致；不落后端 API；复制不用第三方库；模态加宽到 `max-w-[1100px]`；窄屏（<768px）样例区移到主表单下方。

**Edge Cases（边界）** — `navigator.clipboard` 不可用（HTTP 或旧浏览器）→ fallback `document.execCommand('copy')`；复制失败 → 按钮显示「复制失败」。

**Verification（验证）** — `npm --prefix internal/frontend test` 全绿；`npm run build` 成功。

#### 计划

**文件：`internal/frontend/src/components/ScriptGeneratorModal.vue`**

1. 在 `<script setup>` 顶部加 `scriptExamples` const（3 项，每项 `{ id, titleKey, descKey, script }`，`titleKey`/`descKey` 是 i18n key，`script` 是下方全文）。脚本字符串用 `String.raw` 或普通模板串（注意反引号转义）。
2. 加状态：`const expanded = ref<Record<string, boolean>>({})`（每样例独立展开）；`const copiedId = ref<string>('')`（当前已复制的样例 id，2s 清空）。
3. 加 `toggleExample(id)` 和 `copyExample(id)` 方法。`copyExample`：
   ```ts
   async function copyExample(id: string) {
     const ex = scriptExamples.find(e => e.id === id)
     if (!ex) return
     try {
       if (navigator.clipboard?.writeText) {
         await navigator.clipboard.writeText(ex.script)
       } else {
         // fallback
         const ta = document.createElement('textarea')
         ta.value = ex.script
         ta.style.position = 'fixed'
         ta.style.opacity = '0'
         document.body.appendChild(ta)
         ta.select()
         document.execCommand('copy')
         document.body.removeChild(ta)
       }
       copiedId.value = id
       setTimeout(() => { if (copiedId.value === id) copiedId.value = '' }, 2000)
     } catch {
       copiedId.value = ''
     }
   }
   ```
4. 模板：外层 `<div class="flex gap-6">` 包左主表单 + 右样例区。样例区 `<aside class="hidden md:block w-[320px] shrink-0">`（窄屏隐藏，下方再放一份 `<div class="md:hidden">` 简版，或用响应式 flex-wrap）。每样例卡片：
   ```vue
   <div v-for="ex in scriptExamples" :key="ex.id" class="border border-default rounded-md p-3 mb-3">
     <div class="flex items-center justify-between gap-2">
       <button type="button" class="text-sm font-medium hover:underline" @click="toggleExample(ex.id)">
         {{ expanded[ex.id] ? '▼' : '▶' }} {{ t(ex.titleKey) }}
       </button>
       <button type="button" class="text-xs text-primary hover:underline" @click="copyExample(ex.id)">
         {{ copiedId === ex.id ? t('quota.ai_generate_copied') : t('quota.ai_generate_copy') }}
       </button>
     </div>
     <div class="text-xs text-text-secondary mt-1">{{ t(ex.descKey) }}</div>
     <pre v-if="expanded[ex.id]" class="mt-2 text-xs font-mono whitespace-pre overflow-x-auto bg-black/5 dark:bg-white/5 rounded p-2 max-h-[300px] overflow-y-auto">{{ ex.script }}</pre>
   </div>
   ```
5. 模态根容器 `max-w-[760px]` 改 `max-w-[1100px]`；`<main class="p-6 space-y-4">` 外包 `<div class="flex flex-col md:flex-row gap-6">`：左 `<div class="flex-1 space-y-4">`（原 main 内容），右 `<aside class="w-full md:w-[320px] shrink-0">`（样例区）。

**文件：`internal/frontend/src/composables/useI18n.ts`**

6. 加 i18n key（中英）：
   - `quota.ai_generate_examples_title`：「参考脚本样例」/「Script Examples」
   - `quota.ai_generate_copy`：「复制」/「Copy」
   - `quota.ai_generate_copied`：「已复制 ✓」/「Copied ✓」
   - `quota.ai_generate_ex_qwen_title`：「千问 Token Plan（复杂 POST + 双密钥）」/「Qianwen Token Plan (complex POST + dual secret)」
   - `quota.ai_generate_ex_qwen_desc`：「POST form body + Cookie + sec_token + 嵌套 params」/「POST form body + Cookie + sec_token + nested params」
   - `quota.ai_generate_ex_balance_title`：「DeepSeek 余额（简单 GET + Bearer）」/「DeepSeek balance (simple GET + Bearer)」
   - `quota.ai_generate_ex_balance_desc`：「GET + Bearer token，AI 生成最擅长」/「GET + Bearer token, AI's best case」
   - `quota.ai_generate_ex_template_title`：「通用模板（form body + 占位符）」/「Generic template (form body + placeholders)」
   - `quota.ai_generate_ex_template_desc`：「占位符 + bodyType 注释，自行修改」/「Placeholders + bodyType comments, adapt as needed」

**测试文件：`internal/frontend/src/components/ScriptGeneratorModal.test.ts`**

7. 加测试（挂载组件，mock `navigator.clipboard`）：
   - 3 个样例标题（`t(ex.titleKey)` 文案）渲染。
   - 点击第一个样例的复制按钮 → `navigator.clipboard.writeText` 被以该样例 script 调用 + 按钮文案变「已复制 ✓」。

### 样例内容全文（hardcode 进 const）

#### 样例 1：千问 Token Plan usage（id: `qwen`）

```js
({
  request: {
    url: "{{baseUrl}}/data/api.json?product=sfm_bailian&action=BroadScopeAspnGateway&api=zeldaHttp.apikeyMgr.%2Ftokenplan%2Fpersonal%2Fapi%2Fv2%2Fusage",
    method: "POST",
    bodyType: "form",
    headers: {
      "Cookie": "{{apiKey}}",
      "Content-Type": "application/x-www-form-urlencoded",
      "Accept": "application/json, text/plain, */*",
      "Referer": "https://platform.qianwenai.com/home/billing/subscription/token-plan-individual"
    },
    body: {
      product: "sfm_bailian",
      action: "BroadScopeAspnGateway",
      sec_token: "{{apiKey2}}",
      region: "cn-beijing",
      params: {
        Api: "zeldaHttp.apikeyMgr./tokenplan/personal/api/v2/usage",
        Data: {
          cornerstoneParam: {
            domain: "platform.qianwenai.com",
            consoleSite: "QIANWENAI",
            console: "ONE_CONSOLE",
            xsp_lang: "zh-CN",
            protocol: "V2",
            productCode: "p_efm"
          }
        },
        V: "1.0"
      }
    }
  },
  extractor: function(response) {
    if (response.code !== "200" || response.successResponse !== true) {
      return { __error_code: "upstream_business_error", __error_message: (response.data && response.data.errorMsg) || "qianwen usage query failed" };
    }
    var inner = response.data && response.data.DataV2 && response.data.DataV2.data && response.data.DataV2.data.data;
    if (!inner) { return { __error_code: "invalid_response", __error_message: "missing data.DataV2.data.data" }; }
    var tiers = [];
    if (typeof inner.per5HourPercentage === "number") { tiers.push({ window: "five_hour", utilization: inner.per5HourPercentage * 100 }); }
    if (typeof inner.per1WeekPercentage === "number") { tiers.push({ window: "seven_day", utilization: inner.per1WeekPercentage * 100, resetsAt: inner.per1WeekResetTime }); }
    if (tiers.length === 0) { return { __error_code: "empty_result", __error_message: "no percentage fields" }; }
    return tiers;
  }
})
```

#### 样例 2：DeepSeek 余额（id: `balance`）

```js
({
  request: {
    url: "{{baseUrl}}/user/balance",
    method: "GET",
    headers: {
      "Authorization": "Bearer {{apiKey}}",
      "Accept": "application/json"
    }
  },
  extractor: function(response) {
    var infos = response.balance_infos || [];
    var balances = [];
    for (var i = 0; i < infos.length; i++) {
      balances.push({
        planName: infos[i].currency,
        remaining: infos[i].total_balance,
        unit: infos[i].currency
      });
    }
    return balances;
  }
})
```

#### 样例 3：通用模板（id: `template`）

```js
({
  request: {
    // {{baseUrl}} is replaced from the Base URL field; the URL must share host with it (same-origin).
    url: "{{baseUrl}}/your/endpoint",
    method: "POST",
    // bodyType:"form" encodes body as application/x-www-form-urlencoded;
    // nested object values are JSON-marshaled automatically (e.g. params).
    bodyType: "form",
    headers: {
      // {{apiKey}} / {{apiKey2}} are replaced from Script API Key / Additional secret; never appear in JS runtime.
      "Cookie": "{{apiKey}}",
      "Content-Type": "application/x-www-form-urlencoded"
    },
    body: {
      token: "{{apiKey2}}",
      param1: "value1",
      nested: { key: "value" }
    }
  },
  extractor: function(response) {
    // window: five_hour | seven_day | monthly; utilization is always 0-100 used percent.
    return [
      { window: "five_hour", utilization: response.used_pct }
    ];
  }
})
```

#### 验证

- [ ] `npm --prefix internal/frontend test` 全绿。
- [ ] `npm --prefix internal/frontend run build` 成功。
- [ ] 3 个样例标题中英文都显示。
- [ ] 复制按钮把脚本写入剪贴板 + 显示「已复制 ✓」2 秒。
