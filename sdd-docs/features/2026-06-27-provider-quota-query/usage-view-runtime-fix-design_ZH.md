# Provider 用量页面运行时错误修复设计

## 问题

`ProviderUsageView.vue` 导入了 `showZenMuxFields` 工具函数，同时又声明了同名的本地 computed 值。Vite 当前不会因该命名冲突而停止构建，但生成的模块会将 computed ref 赋给其 getter 调用的同一变量。页面渲染时会把该 ref 当作函数调用，从而抛出 `TypeError: Q is not a function`，导致 Provider 用量页面空白。

## 范围

仅进行最小的前端修复。不改变配额查询行为、API 契约、表单请求载荷或无关的既有 TypeScript 错误。

## 设计

- 将导入的工具函数别名设为 `shouldShowZenMuxFields`。
- 保留本地 computed 值的名称 `showZenMuxFields`，使模板无需修改。
- 让 computed getter 调用 `shouldShowZenMuxFields`，消除变量命名冲突和自引用。
- 添加聚焦于源码接线的回归测试：当导入函数与 computed 变量冲突时测试失败，只有工具函数使用独立别名并由 getter 正确调用时测试才通过。

## 验证

- 运行新增的定向回归测试，并确认修改生产代码前测试会失败。
- 修改导入别名和 getter 后，确认定向测试通过。
- 运行完整前端测试套件。
- 构建前端，并确认生成的 Provider 用量页面代码块不再包含 computed 自引用调用。
- 运行仓库 Go 测试套件，确认更新后的内嵌前端与后端保持兼容。

## 非目标

- 将 `vue-tsc` 设为强制构建步骤。仓库当前存在无关的既有类型错误，应另行清理。
- 重构 Provider 配额表单状态或字段可见性工具函数。
- 更改 Provider 用量页面的布局或行为。
