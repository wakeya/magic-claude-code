# AGENTS.md - 开发指南

## 环境要求

- Go 1.26+
- Docker (可选，用于部署)
- Make

## 开发流程

### 1. 克隆项目

```bash
git clone <repo-url>
cd magic-claude-code
```

### 2. 安装依赖

```bash
go mod download
```

### 3. 运行测试

```bash
make test
```

### 4. 本地开发

```bash
# 创建数据目录
mkdir -p data

# 运行服务 (需要 root 权限绑定 443 端口)
sudo make run

# 或者使用非特权端口测试
sudo go run ./cmd/server -data ./data
```

### 5. Docker 本地部署

```bash
# 一键构建并启动（通过 git describe 自动注入版本号到 mcc --version）
make docker-run

# 查看日志 / 停止
docker logs -f mcc
make docker-stop
```

`make docker-run` 等价于 `APP_VERSION=$(git describe --tags --always --dirty) docker compose up -d --build`，版本号反映当前代码状态（tag / commit / dirty）。直接 `docker compose up`（不走 Makefile）时版本号为 `dev`。

## 模块说明

### internal/config

配置管理模块，负责：

- 加载和保存配置到 JSON 文件
- 配置验证
- 默认配置管理

### internal/cert

证书管理模块，负责：

- CA 证书生成（10年有效期）
- 服务器证书签发（api.anthropic.com）
- 证书加载和保存

### internal/proxy

代理服务模块，负责：

- TLS 终结
- HTTP 请求转发
- Header 透传
- 统计收集

### internal/admin

配置服务模块，负责：

- REST API
- 密码认证
- Session 管理
- 静态文件服务

## API 端点

| 方法 | 路径 | 描述 |
|------|------|------|
| POST | /api/login | 登录 |
| POST | /api/logout | 登出 |
| GET | /api/config | 获取配置 |
| PUT | /api/config | 更新配置 |
| GET | /api/status | 服务状态 |
| GET | /api/certificates | 证书信息 |
| POST | /api/config/test | 测试后端连接 |

## 调试

### 查看日志

```bash
# Docker 部署
docker logs -f mcc

# 本地运行
# 日志直接输出到控制台
```

### 检查证书

```bash
# 查看 CA 证书信息
openssl x509 -in data/ca.crt -text -noout

# 查看服务器证书信息
openssl x509 -in data/server.crt -text -noout
```

## 发布流程

发布由 `v*` tag 触发。GitHub 通过 CI 自动构建并发布；Gitee/GitCode 由 `scripts/release.sh vX.Y.Z` 手动构建并将二进制作为 Release 附件上传；GitLab 创建 Release 并附 GitHub 下载链接。二进制不存入 git 仓库。

### 1. 提交代码

提交前确认只包含本次改动：

```bash
git status --short
git diff --stat
```

运行验证：

```bash
go test ./...
npm --prefix internal/frontend test
npm --prefix internal/frontend run build
```

说明：

- 修改前端源码时，`internal/frontend/dist` 会随构建产物变化，可以一起提交。
- 二进制不存入 git，无需手动管理发布资产目录。

提交并推送：

```bash
git add <本次相关文件>
git commit -m "feat: ..."
git push origin main
```

### 2. 编写发布说明

在 `sdd-docs/changes/release-notes/` 下创建 `vX.Y.Z.md` 文件，记录本次版本变更摘要：

```bash
# 参考格式
cat > sdd-docs/changes/release-notes/v0.4.0.md <<'EOF'
## v0.4.0 更新内容

### Added
- 新功能描述

### Changed
- 变更描述

### Fixed
- 修复描述
EOF
```

`scripts/release.sh` 和 CI 都会优先读取此文件作为 Release 描述。如果文件不存在，CI 从 git log 自动生成，`scripts/release.sh` 使用默认文案。

提交发布说明：

```bash
git add sdd-docs/changes/release-notes/vX.Y.Z.md
git commit -m "docs: add release notes for vX.Y.Z"
git push origin main
```

### 3. GitHub 发布：打 tag

确认 `main` 已推送后创建版本 tag，触发 GitHub CI：

```bash
git tag vX.Y.Z
git push origin vX.Y.Z
```

tag 名必须符合 `v0.1.0` 这类语义版本格式。GitHub CI 会执行前端构建、Go 测试、跨平台二进制构建、SHA256SUMS 生成和发布资产上传。

### 4. GitLab/Gitee/GitCode 发布：手动构建

GitLab/Gitee/GitCode 使用 `scripts/release.sh` 在本地完成构建和发布：

```bash
GITEE_TOKEN=xxx GITCODE_TOKEN=xxx ./scripts/release.sh vX.Y.Z
```

脚本会依次执行：同步 main → 构建前端 → 测试 → 交叉编译 6 个平台 → 推送代码和 tag → 创建 Gitee/GitCode Release 并上传二进制附件 → 创建 GitLab Release（附 GitHub 下载链接） → 清理本地构建目录。发布说明自动从 `sdd-docs/changes/release-notes/vX.Y.Z.md` 读取。

### 5. 发布后检查

检查各平台 Release 是否包含以下附件：

```text
Magic-Claude-Code-vX.Y.Z-Linux-x86_64.tar.gz
Magic-Claude-Code-vX.Y.Z-Linux-arm64.tar.gz
Magic-Claude-Code-vX.Y.Z-macOS-x86_64.tar.gz
Magic-Claude-Code-vX.Y.Z-macOS-arm64.tar.gz
Magic-Claude-Code-vX.Y.Z-Windows-x86_64.zip
Magic-Claude-Code-vX.Y.Z-Windows-arm64.zip
SHA256SUMS.txt
```

- GitHub/Gitee/GitCode: 检查 Release 附件列表
- GitLab: 检查 Release 下载链接（指向 GitHub）
