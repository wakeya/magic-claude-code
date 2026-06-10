# 多模态模型切换 — 实现计划

**目标：** 在 Provider 中增加可选多模态模型切换配置，并在代理请求转换阶段按请求内容选择最终模型。

**架构：** 复用现有 Provider 配置、SQLite 存储、Admin API 和 `transformRequest` 请求转换路径。不新增外部依赖。

---

## 文件规划

修改：

1. `internal/config/provider.go`
2. `internal/config/sqlite_store.go`
3. `internal/admin/provider_handler.go`
4. `internal/proxy/handler.go`
5. `internal/frontend/src/composables/useApi.ts`
6. `internal/frontend/src/composables/useI18n.ts`
7. `internal/frontend/src/components/ProviderModal.vue`
8. `internal/frontend/src/components/ProviderCard.vue`
9. `internal/frontend/dist/*`

新增：

1. `internal/admin/provider_handler_test.go`

测试扩展：

1. `internal/proxy/server_test.go`
2. `internal/config/sqlite_store_test.go`

## 任务 1：代理模型选择测试

- [x] 编写失败测试：检测到 `tool_result.content` 中的 `type: image` 时使用 `multimodal_model`。
- [x] 增加媒体检测测试，覆盖 document 块、PDF media type、音频 media type 和视频 media type。
- [x] 编写失败测试：纯文本请求即使启用多模态切换，也继续使用 `model_mappings`。
- [x] 编写失败测试：请求含图片但开关关闭时，仍使用 `model_mappings`。
- [x] 验证 RED：测试因 Provider 缺少新字段失败。

## 任务 2：配置结构和请求转换

- [x] 在 Provider 中增加 `MultimodalSwitch` 和 `MultimodalModel`。
- [x] 在 `transformRequest` 中复用已解析 JSON，递归检测 `messages/system` 中的非文本内容。
- [x] 检测到非文本内容且配置有效时，将请求 `model` 改为 `MultimodalModel`。
- [x] 更新请求日志和 usage 记录里的 mapped model，使其反映最终转发模型。

## 任务 3：SQLite 存储和迁移

- [x] 在 `providers` 表中增加 `multimodal_switch` 和 `multimodal_model`。
- [x] 为已有 SQLite 数据库补列，保持升级兼容。
- [x] 保存和加载 Provider 时持久化新字段。
- [x] 增加旧表结构迁移测试。

## 任务 4：Admin API

- [x] Provider 列表和详情接口返回多模态字段。
- [x] 创建 Provider 时接收并保存多模态字段。
- [x] 更新 Provider 时支持部分更新，字段省略时保留原配置。
- [x] 复制 Provider 时保留多模态配置。
- [x] 校验开启多模态切换时必须填写多模态模型 ID。
- [x] 用 Admin API 测试覆盖列表、详情、更新、复制行为。

## 任务 5：前端配置 UI

- [x] API 类型增加 `multimodal_switch` 和 `multimodal_model`。
- [x] Provider 弹窗增加 `多模态切换` 开关。
- [x] 开关旁增加问号提示。
- [x] 开启后显示 `多模态模型 ID` 输入框。
- [x] 保存时传递多模态字段。
- [x] Provider 卡片显示多模态切换摘要。
- [x] 更新中英文 i18n 文案。

## 任务 6：验证

- [x] `go test ./internal/proxy ./internal/config ./internal/admin`
- [x] `go test ./...`
- [x] `npm --prefix internal/frontend test`
- [x] `npm --prefix internal/frontend run build`
- [x] 浏览器检查 Provider 弹窗渲染。

## 风险与缓解

| 风险 | 缓解 |
|------|------|
| 递归扫描请求体影响性能 | 复用 `transformRequest` 的 JSON 解析结果，避免额外解析；扫描只在请求转发前进行一次。 |
| 误判普通文本为多模态内容 | 只匹配明确的内容块类型或明确的 `media_type`。 |
| API 部分更新清空已有配置 | 使用指针字段判断请求是否包含该字段。 |
| 旧数据库缺少列 | 启动时通过 `PRAGMA table_info` 检查并补列。 |
| 开启开关但未填模型 ID | 前端和 Admin API 双重校验。 |
