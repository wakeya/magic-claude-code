# 版本显示修复规格

本地页面：管理面板头部  
代理入口：无（管理服务 :8442）  
参考源站：`/api/status`（version 字段）、`/api/update/check`（current_version 字段）  
技术栈：Vue 3 + TypeScript  
最后更新：2026-06-15  
进度：1 / 1 已完成

## 整体分析（源站分析）

### Bug 描述

头部版本标签（`AppHeader.vue` 中的 `currentVersion`）绑定在 `updateInfo.value?.current_version` 上，该值仅在 `/api/update/check` 调用成功后才会赋值。然而更新检查通过 localStorage 按浏览器限制为每 24 小时一次。在节流窗口内的后续页面加载中，`shouldCheckForUpdate()` 返回 `false`，`checkUpdate()` 直接返回，`updateInfo` 保持 `null`，`currentVersion` 回退为 `'dev'`。

### 根因分析

设计上将两个不相关的关注点耦合到了同一个数据源：

1. **版本显示** — 静态信息，应始终可获取。
2. **更新可用性检查** — 远程操作，合理地被节流。

通过从 `updateInfo.current_version` 派生版本标签，更新检查的 24 小时节流意外地导致版本标签在第一次加载后的每次页面刷新时消失。

### 修复前数据流

```
页面加载
  → checkUpdate()
    → shouldCheckForUpdate() == false（被节流）
    → 直接返回
  → updateInfo 保持 null
  → currentVersion = updateInfo?.current_version || 'dev'
  → 头部显示 'dev'
```

### 修复后数据流

```
页面加载
  → fetchStatusVersion()         （每次都调用，不受节流影响）
    → GET /api/status
    → statusVersion = status.version
  → checkUpdate()                （节流，仅用于检查更新可用性）
    → shouldCheckForUpdate() == false
    → 直接返回（不影响版本显示）
  → currentVersion = statusVersion.value
  → 头部显示正确版本
```

### 后端验证

两个端点在成功响应中都从 `internal/version.Version` 派生版本号。`GET /api/status` 始终返回 `version` 字段。`GET /api/update/check` 仅在成功时返回 `current_version`；失败时可能只返回 `error` 字段而不含 `current_version`。

修复将版本显示与更新检查解耦，使用始终可用的 `/api/status` 端点。

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | 已完成 | 将版本显示与更新检查解耦 | `useApi.ts`、`AppHeader.vue`、`AppHeader.test.ts` | 测试通过；构建通过 |

## 需求

### 交付物

1. `useApi.ts` 的 `StatusInfo` 接口包含 `version?: string` 字段，与后端 `/api/status` 响应对齐。
2. `AppHeader.vue` 通过专用的 `fetchStatusVersion()` 函数每次页面加载都从 `/api/status` 获取版本，独立于更新检查节流。
3. `currentVersion` computed 属性从 `statusVersion` ref 读取，而非 `updateInfo.current_version`。
4. `onMounted` 同时调用 `fetchStatusVersion()`（每次都调用）和 `checkUpdate()`（被节流），并行执行。
5. `AppHeader.test.ts` 新增回归测试，锁定版本来源为 `/api/status`，防止再次绑定回 `updateInfo`。

### 约束

1. 修复不得改变 24 小时更新检查节流行为——节流仅适用于更新可用性检查，不影响版本显示。
2. `fetchStatusVersion()` 必须是尽力而为的，失败时静默——如果 `/api/status` 失败，版本回退为 `'dev'`，与之前一致。
3. 更新对话框仍使用 `updateInfo.current_version` 和 `updateInfo.latest_version`——这是正确的，因为对话框只在更新检查成功后打开。

### 边界情况

1. `/api/status` 不可达——版本回退为 `'dev'`；不向用户显示错误。
2. 更新检查被节流——版本仍从 `/api/status` 正确显示。
3. 更新检查成功——`updateInfo` 被赋值供对话框使用；版本显示不受影响（已从 `/api/status` 获取正确值）。
4. `/api/status` 和 `/api/update/check` 都失败——版本显示 `'dev'`；不显示更新通知。

### 关联规格

- [自动更新规格](../2026-06-13-auto-update/spec_ZH.md) — 任务 5 原始设计中 `currentVersion` 派生自 `updateInfo.current_version`。本次修复纠正了该设计。

## 任务详情

### 任务 1：将版本显示与更新检查解耦

#### 需求

**Objective（目标）** — 修复在 24 小时更新检查节流窗口内页面刷新时头部版本标签显示 `dev` 的回归问题。

**Outcomes（成果）** — `StatusInfo` 接口包含 `version?: string`；`AppHeader.vue` 从 `/api/status` 独立获取版本；`currentVersion` 从 `statusVersion` 读取；新增回归测试。

**Evidence（证据）** — `npm test` 50/50 通过，包含新增的回归测试；`npm run build` 构建成功。

**Constraints（约束）** — 不改变更新检查节流逻辑；不改变更新对话框数据来源；`fetchStatusVersion` 失败时必须静默。

**Edge Cases（边界）** — `/api/status` 失败（回退 `'dev'`）；更新检查被节流（版本仍正确）；两者都失败（版本 `'dev'`，无更新徽章）。

**Verification（验证）** — 前端测试通过；构建通过。

#### 计划

1. 在 `useApi.ts` 的 `StatusInfo` 接口添加 `version?: string`。
2. 在 `AppHeader.vue` 添加 `statusVersion` ref（默认 `'dev'`）和 `fetchStatusVersion()` 函数。
3. 将 `currentVersion` computed 改为读取 `statusVersion.value`。
4. 在 `onMounted` 中同时调用 `fetchStatusVersion()` 和 `checkUpdate()`。
5. 在 `AppHeader.test.ts` 添加回归测试。

#### 验证

- [x] `StatusInfo` 接口包含 `version?: string`。
- [x] `currentVersion` 从 `statusVersion.value` 读取，而非 `updateInfo.current_version`。
- [x] `fetchStatusVersion()` 调用 `api.getStatus()` 并设置 `statusVersion`。
- [x] `onMounted` 同时调用 `fetchStatusVersion()` 和 `checkUpdate()`。
- [x] 回归测试断言版本来源独立于更新检查。
- [x] `npm test` 通过（50/50）。
- [x] `npm run build` 构建成功。
