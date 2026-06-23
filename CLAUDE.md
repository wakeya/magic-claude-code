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
| internal/bootstrap/ | 启动引导：hosts/CA/环境自动配置与模式降级 |
| internal/i18n/ | 启动日志和引导消息本地化 |

## 编码规范

1. 使用 Go 标准错误处理模式
2. 所有公开函数需要注释
3. 单元测试覆盖核心逻辑
4. 使用 context.Context 进行取消和超时控制
5. **新增或修改跨函数共享的状态字段**（`bootstrap.Result/Caps`、`config.Config`、`admin.AdminConfig` 等）时，必须 `grep` 所有读该字段的早返回、hash 输入、成功谓词，确认是否需要同步更新。常见盲区：透明模式成功判断、`stateHash`、API 响应字段。
6. **占位/未完成的实现**必须主动暴露（`return fmt.Errorf("unimplemented: ...")` 或 `panic("unimplemented")`），禁止写"形似已完成"的检测/分支但不执行实际操作。

## 测试

```bash
# 运行所有测试（含 race detector 和覆盖率，CI 入口）
make test

# 等价的直接调用
go test -v -race -coverprofile=coverage.out ./...

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

5. 二进制不存入 git 仓库。构建时使用临时目录，通过 Release API 上传为各平台的 Release 附件。

6. GitHub 通过 `v*` tag 触发 CI 构建并上传 Release 附件。GitLab/Gitee/GitCode 使用 `scripts/release.sh vX.Y.Z` 手动构建，将二进制作为 Release 附件上传到 Gitee/GitCode，GitLab Release 附 GitHub 下载链接。

7. 发版前在 `sdd-docs/changes/release-notes/vX.Y.Z.md` 编写发布说明并提交。CI 和 `scripts/release.sh` 都会优先使用此文件作为 Release 描述。

8. 自动更新器通过 Release 下载 URL（`{platform}/{owner}/{repo}/releases/download/{tag}/{file}`）获取二进制，免认证匿名下载。

## 常见问题

### Q: 证书过期怎么办？

A: 证书有效期 10 年，启动时会自动检查并续期。也可在配置页面手动重新生成。

### Q: 忘记密码怎么办？

A: 重启容器时通过环境变量 ADMIN_PASSWORD 重新设置。
