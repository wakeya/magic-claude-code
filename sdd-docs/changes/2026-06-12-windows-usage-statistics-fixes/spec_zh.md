# 变更规格：Windows 使用统计可靠性修复

日期: 2026-06-12
状态: 已验证
关联功能规格: `sdd-docs/features/2026-06-12-windows-usage-statistics-fixes/spec_zh.md`

---

## 摘要

本变更修复 Windows 上使用统计页面为空的问题：一方面让 Windows 二进制自带 IANA 时区数据，另一方面在收到 SSE 终止事件后即可完成 usage 记录，不再只等待上游 EOF。

## 用户影响

修复前，Windows 用户可以看到代理请求成功日志，`data/proxy.db` 中也存在 usage 数据，但服务状态页和使用统计页仍显示 0。修复后，页面可以基于浏览器时区正确查询已有记录，流式请求也能在终止事件后完成统计写入。

## 修复内容

| 区域 | 修复 |
|------|------|
| Windows 时区支持 | 嵌入 Go `time/tzdata`，使 Windows 二进制可以加载 `Asia/Shanghai` 等浏览器 IANA 时区 |
| 流式 usage 持久化 | 将 `message_stop` 和 `[DONE]` 视为 SSE 终止事件，避免一直等待上游 EOF |
| 终止事件 usage 解析 | `message_stop` payload 中的 usage 会先合并，再标记流完成 |
| Windows 二进制包 | 重新构建 `bin/mcc-windows-amd64/mcc.exe`，包含后端修复与当前内嵌前端资源 |

## 验证证据

| 检查 | 结果 |
|------|------|
| `go test ./...` | 328 通过 |
| `npm --prefix internal/frontend test` | 45 通过 |
| `git diff --check` | 通过 |
| 二进制检查 | Windows exe 包含 `time/tzdata`、`Asia/Shanghai`、`IsComplete`、`index-Dxc_BCfC.js` |

## 发布说明

1. 用重新构建的 `mcc.exe` 替换 Windows 上的旧文件。
2. 重启服务。
3. 如果管理后台页面已打开，浏览器强制刷新。
4. `data/proxy.db` 中已有的数据无需清除，应可直接显示。

## 兼容性

- Linux 既有行为保持不变。
- 使用统计日期快捷筛选语义保持不变。
- SSE EOF fallback 保留。
- Windows 二进制体积会因内嵌时区数据增加。
