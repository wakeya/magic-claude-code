# 使用统计快捷日期范围 — 实现计划

**目标：** 在使用统计页面新增紧凑快捷日期范围条，并复用现有 `from`、`to`、`tz` 查询链路刷新所有统计视图。

**架构：** 仅修改前端。快捷范围在 `DashboardView.vue` 中转换为现有 `usageFilters.from` 和 `usageFilters.to`，后端 API 和存储逻辑保持不变。

---

## 文件规划

修改：

1. `internal/frontend/src/views/DashboardView.vue`
2. `internal/frontend/src/composables/useI18n.ts`
3. `internal/frontend/src/views/DashboardUsageRequests.test.ts`

如需要更新构建产物：

1. `internal/frontend/dist/*`

## 任务 1：日期预设状态和计算

- [ ] 定义快捷范围 key：`today`、`last_7_days`、`last_30_days`。
- [ ] 增加基于本地日期的格式化函数，避免使用 UTC `toISOString()` 导致本地日期偏移。
- [ ] 实现 `presetDateRange(key)`：
  - `today`：今天到今天。
  - `last_7_days`：昨天往前 7 个完整自然日。
  - `last_30_days`：昨天往前 30 个完整自然日。
- [ ] 将默认 `usageFilters.from/to` 改为 `last_7_days`。
- [ ] 增加计算属性，根据当前 `usageFilters.from/to` 判断当前匹配的快捷范围。

## 任务 2：快捷日期 UI

- [ ] 在使用统计二级页签切换块和筛选条件块之间插入紧凑工具条。
- [ ] 工具条展示 `时间范围`、`今日`、`近7天`、`近30天`。
- [ ] 选中状态复用现有按钮视觉语言。
- [ ] 未匹配任何预设时，三个按钮都不高亮。
- [ ] 保持移动端可换行，不让文本溢出。

## 任务 3：点击同步和刷新

- [ ] 点击快捷按钮时更新 `usageFilters.from` 和 `usageFilters.to`。
- [ ] 保持其他筛选条件不变。
- [ ] 复用现有 `watch(usageFilters)` 重置请求日志分页并触发刷新。
- [ ] 确认手动修改日期不会被快捷范围逻辑覆盖。

## 任务 4：i18n 和测试

- [ ] 增加中英文 i18n key：
  - `usage.date_range`
  - `usage.range_today`
  - `usage.range_last_7_days`
  - `usage.range_last_30_days`
- [ ] 扩展前端源文件测试，检查快捷条、i18n key、默认日期语义和点击函数存在。
- [ ] 如日期计算逻辑可独立抽出，优先增加确定性单元测试。

## 任务 5：验证

- [ ] `npm --prefix internal/frontend test`
- [ ] `npm --prefix internal/frontend run build`
- [ ] 浏览器手动检查使用统计页面：
  - 默认选中 `近7天`。
  - 点击三个快捷项能同步开始/结束日期。
  - 手动输入自定义日期后快捷项不高亮。
  - 日期变化后统计数据刷新。

## 风险与缓解

| 风险 | 缓解 |
|------|------|
| 使用 `toISOString()` 导致本地日期跨时区偏移 | 使用本地 `getFullYear()`、`getMonth()`、`getDate()` 生成 `YYYY-MM-DD`。 |
| 快捷按钮和手动日期互相覆盖 | 只在点击快捷按钮时写入日期；选中状态用计算属性派生。 |
| 筛选变更触发重复请求 | 复用现有 250ms debounce watcher。 |
| 移动端工具条过宽 | 使用 flex wrap 和紧凑按钮宽度。 |
