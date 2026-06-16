# CLAUDE.md - 项目指南

## 项目概述

Claude Code 透明代理，用于解决使用第三方 API 时 Claude Code 禁用优化功能的问题。

## 技术栈

- Go 1.26
- 原生 HTTP 标准库
- bcrypt 密码哈希
- Docker (Alpine)

## 架构

```
┌─────────────────────────────────────────┐
│            Go Application               │
│                                         │
│  :443 (代理服务)    :8442 (配置服务)    │
│       ↓                   ↓             │
│  [透明代理]         [配置API + 前端]    │
│       ↓                   ↓             │
│  [证书管理器] ←──────────────┘          │
│       ↓                                 │
│  [配置管理器]                           │
└─────────────────────────────────────────┘
```

## 关键文件

| 文件 | 职责 |
|------|------|
| cmd/server/main.go | 应用入口，启动服务 |
| internal/proxy/ | 透明代理逻辑 |
| internal/admin/ | 配置服务和认证 |
| internal/cert/ | CA 和服务器证书管理 |
| internal/config/ | 配置存储和管理 |

## 编码规范

1. 使用 Go 标准错误处理模式
2. 所有公开函数需要注释
3. 单元测试覆盖核心逻辑
4. 使用 context.Context 进行取消和超时控制

## 测试

```bash
# 运行所有测试
go test ./...

# 运行特定包测试
go test ./internal/cert/... -v

# 查看覆盖率
go test -cover ./...
```

## 提交与发布约束

1. 提交前必须检查工作区，只提交本次任务相关文件：

```bash
git status --short
git diff --stat
```

2. 后端或代理逻辑变更至少运行：

```bash
go test ./...
```

3. 前端源码变更必须运行：

```bash
npm --prefix internal/frontend test
npm --prefix internal/frontend run build
```

4. `internal/frontend/dist` 是 Go 二进制内嵌前端资源，前端源码变更后可以随构建结果一起提交。

5. `dist/release` 是发布资产目录，普通功能提交不得手动修改或覆盖。GitCode/Gitee 回退源的二进制资产以远端发布仓库为准，本地过期或不完整的 `dist/release` 不得推送覆盖远端。

6. 发布版本通过 `v*` tag 触发 CI 构建和同步资产。不要手工打包后直接替换远端发布资产，除非这是一次单独的发布修复任务。

## 常见问题

### Q: 证书过期怎么办？

A: 证书有效期 10 年，启动时会自动检查并续期。也可在配置页面手动重新生成。

### Q: 忘记密码怎么办？

A: 重启容器时通过环境变量 ADMIN_PASSWORD 重新设置。
