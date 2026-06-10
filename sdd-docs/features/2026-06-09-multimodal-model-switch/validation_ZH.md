# 多模态模型切换 — 验证清单

**功能：** 多模态模型切换
**状态：** validated
**最后更新：** 2026-06-09

## 验收标准

1. **模型切换：** 请求含图片/PDF/音频/视频等非文本内容，且 Provider 开启 `multimodal_switch` 并配置 `multimodal_model` 时，最终 `model` 使用多模态模型 ID。
2. **纯文本保持映射：** 纯文本请求继续走现有 `model_mappings`。
3. **开关关闭保持映射：** 请求含非文本内容但开关关闭时，不覆盖模型映射。
4. **工具结果嵌套：** `tool_result.content` 内嵌图片能被检测。
5. **配置持久化：** SQLite 保存和加载 Provider 时保留多模态字段。
6. **旧库兼容：** 已存在的 `providers` 表缺少多模态列时，启动会自动补列。
7. **Admin API：** 创建、列表、更新、复制 Provider 均保留多模态字段。
8. **API 校验：** 开启多模态切换但缺少多模态模型 ID 时返回 400。
9. **UI 配置：** Provider 弹窗展示 `多模态切换`、问号提示和 `多模态模型 ID` 输入框。
10. **构建产物：** 前端 dist 资源随 UI 变更重新构建。

## 自动化验证

```bash
go test ./internal/proxy ./internal/config ./internal/admin
go test ./...
npm --prefix internal/frontend test
npm --prefix internal/frontend run build
```

## 验证证据

2026-06-09 / 2026-06-10 复核：

1. `go test ./internal/proxy ./internal/config ./internal/admin` 通过，覆盖代理模型选择、SQLite 持久化和 Admin API。
2. 代理测试覆盖截图工具图片、document 块、PDF media type、音频 media type、视频 media type、纯文本回退和关闭开关回退。
3. Admin API 测试覆盖创建/列表/详情往返、字段省略更新保留、开启开关必须填写模型 ID、复制保留配置。
4. `go test ./...` 通过，结果：`311 passed in 8 packages`。
5. `npm --prefix internal/frontend test` 通过，结果：`37 passed`。
6. `npm --prefix internal/frontend run build` 通过，Vite 构建成功。
7. 浏览器打开 `http://127.0.0.1:5178`，进入 Provider 管理并打开添加弹窗：
   - 能看到 `多模态切换`。
   - 能看到问号提示。
   - 勾选后显示 `多模态模型 ID` 输入框。
8. 用户已使用真实 Mimo 多模态模型 `mimo-v2.5` 验证新版本，代理可以正常访问已配置的 Mimo 多模态模型。

## 手动验证建议

### 场景 1：Mimo 图片请求

状态：用户已使用真实 Mimo 多模态模型 `mimo-v2.5` 验证通过。

1. Provider 模型映射：`claude-opus-4-6 -> mimo-v2.5-pro`。
2. 开启 `多模态切换`。
3. 设置 `多模态模型 ID` 为 `mimo-v2.5`。
4. 在 Claude Code 中触发截图工具返回图片。
5. 预期：代理日志中最终 `model` 记录为多模态模型，不再触发 `No endpoints found that support image input`。

### 场景 2：GLM 兼容图片

1. Provider 模型映射：`claude-opus-4-6 -> glm-5.1`。
2. 保持 `多模态切换` 关闭。
3. 发送含图片的请求。
4. 预期：代理仍使用 `glm-5.1`，不做额外切换。

### 场景 3：纯文本请求

1. Provider 开启 `多模态切换` 并配置模型 ID。
2. 发送普通纯文本请求。
3. 预期：代理仍使用 `model_mappings` 的文本模型，不使用多模态模型。
