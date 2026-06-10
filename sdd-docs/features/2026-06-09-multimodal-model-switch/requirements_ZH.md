# 多模态模型切换

**版本：** 1.0
**日期：** 2026-06-09
**状态：** implemented
**生命周期：** validated

---

## 1. 目标

为供应商配置增加可选的“多模态切换”能力。当 Claude Code 请求中包含图片、PDF、音频或视频等非文本内容时，代理可自动将请求模型替换为该供应商配置的单一多模态模型 ID，避免文本模型上游拒绝多模态输入。

## 2. 背景

小米 Mimo 供应商在请求包含截图图片时返回：

```json
{"error":{"code":"404","message":"No endpoints found that support image input","param":"","type":""}}
```

排查确认该请求由 `mcp__chrome-devtools-mcp__take_screenshot` 的 `tool_result` 带入 `image/png` base64 内容。现有模型映射将 `claude-opus-4-6` 映射到 `mimo-v2.5-pro`，但该模型不支持图片输入。

同时，部分供应商或模型虽然名称上是文本模型，但上游做了兼容，可直接接受含图片的请求。因此不能全局强制切换，也不能把“是否支持多模态”视为供应商通用能力。

## 3. 需求

### 3.1 Provider 配置

每个 Provider 增加两个字段：

1. `multimodal_switch`：是否启用“多模态切换”。
2. `multimodal_model`：检测到非文本内容时使用的目标模型 ID。

默认值：

1. `multimodal_switch = false`
2. `multimodal_model = ""`

### 3.2 模型选择规则

请求转发前按以下顺序确定最终 `model`：

1. 读取原始请求 `model`。
2. 先按现有 `model_mappings` 得到文本请求的默认映射模型。
3. 若满足以下全部条件，则覆盖为 `multimodal_model`：
   - 请求体检测到非文本内容。
   - Provider 的 `multimodal_switch` 为 `true`。
   - Provider 的 `multimodal_model` 非空。
4. 否则继续使用原有模型映射结果。

`supports_thinking` 与多模态切换互相独立。模型切换后仍按 Provider 的 `supports_thinking` 规则保留或剥离顶层 `thinking` 字段。

### 3.3 非文本内容检测范围

检测应覆盖 Claude Messages 请求中的常见结构，尤其是工具结果嵌套内容：

1. `messages`
2. `system`
3. 嵌套的 `tool_result.content`
4. 任意数组或对象深度中的内容块

判定为非文本内容的条件：

1. `type` 为 `image`、`input_image` 或 `document`。
2. `source.media_type` 以 `image/`、`video/`、`audio/` 开头。
3. `source.media_type` 等于 `application/pdf`。

### 3.4 Admin API

Provider 的创建、查询、更新、复制均需保留多模态配置。

API 约束：

1. 创建 Provider 时，若 `multimodal_switch=true`，则 `multimodal_model` 必须非空。
2. 更新 Provider 时，若请求未包含多模态字段，不应清空已有配置。
3. 更新后若 Provider 处于 `multimodal_switch=true` 且 `multimodal_model` 为空，应返回 400。

### 3.5 前端 UI

Provider 弹窗增加：

1. 开关名称：`多模态切换`
2. 字段名称：`多模态模型 ID`
3. Tooltip 文案：`请求含图片、PDF 等非文本内容时，自动改用该模型。若当前模型已兼容多模态，可关闭。`

UI 行为：

1. 默认关闭。
2. 开启后显示 `多模态模型 ID` 输入框。
3. 开启后保存时必须填写模型 ID。
4. Provider 卡片显示多模态切换摘要，便于排查配置。

## 4. 非目标

1. 不做按原始模型分别配置多模态映射。
2. 不自动剥离图片、PDF、音频或视频内容。
3. 不根据供应商 URL 自动判断是否需要多模态切换。
4. 不修改 Anthropic Beta Header 或其他协议头。
5. 不新增多供应商故障转移。
6. 不改变未启用该开关的 Provider 行为。

## 5. 成功标准

1. Mimo 这类文本模型不支持图片输入的供应商，可通过配置 `multimodal_switch=true` 和 `multimodal_model` 自动切换模型。
2. GLM 这类已兼容图片输入的供应商，可保持开关关闭并继续使用原映射。
3. 截图工具返回的 `tool_result` 图片能被检测到。
4. 纯文本请求不触发多模态模型切换。
5. 旧 SQLite 数据库升级后不会因缺少新字段而启动失败。
6. 后端测试、前端测试和前端构建通过。
