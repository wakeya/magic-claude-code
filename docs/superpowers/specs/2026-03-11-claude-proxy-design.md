# Claude Code 透明代理设计文档

**版本**: 1.0
**日期**: 2026-03-11
**状态**: 待审核

---

## 1. 概述

### 1.1 背景

当使用 `ANTHROPIC_BASE_URL` 环境变量指向第三方 API 端点时，Claude Code 会检测到这不是官方端点，从而禁用某些优化功能（如 ToolSearch:optimistic）。此外，Claude Code 二进制中约有 50 处硬编码的 `api.anthropic.com` 引用，部分端点完全忽略 `ANTHROPIC_BASE_URL`，永远打到官方服务器。

### 1.2 目标

构建透明代理系统，让 Claude Code 误以为在与官方 API 通信，从而：

- 启用所有优化功能
- 统一处理所有 API 请求
- 提供可配置的后端地址管理

### 1.3 范围

| 包含 | 不包含 |
|------|--------|
| 透明代理服务 | 多租户支持 |
| 前端配置页面 | 高可用集群 |
| CA 证书自动管理 | API 调用统计/计费 |
| Docker 单容器部署 | 复杂用户权限系统 |

---

## 2. 系统架构

### 2.1 整体架构

```
┌──────────────────────────────────────────────────────────────────┐
│                        Docker Container                          │
│                                                                  │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │                    Go Application                          │ │
│  │                                                            │ │
│  │   :443 (HTTPS)           :8442 (HTTPS)                    │ │
│  │   代理服务                配置服务                         │ │
│  │   ┌──────────┐           ┌──────────────────────┐        │ │
│  │   │ TLS 终结 │           │   静态文件服务       │        │ │
│  │   │ 请求转发 │           │   配置 API          │        │ │
│  │   │ 认证透传 │           │   密码认证          │        │ │
│  │   └──────────┘           └──────────────────────┘        │ │
│  │        ↓                          ↓                       │ │
│  │   ┌──────────────────────────────────────────┐          │ │
│  │   │            配置管理器                     │          │ │
│  │   └──────────────────────────────────────────┘          │ │
│  │                              ↓                            │ │
│  │   ┌──────────────────────────────────────────┐          │ │
│  │   │            证书管理器                     │          │ │
│  │   └──────────────────────────────────────────┘          │ │
│  └────────────────────────────────────────────────────────────┘ │
│                                                                  │
│  数据目录: /app/data                                             │
│  ├── config.json          # 配置文件                            │
│  ├── ca.crt               # CA 证书                             │
│  ├── ca.key               # CA 私钥                             │
│  └── server.crt           # api.anthropic.com 证书              │
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

### 2.2 技术选型

| 组件 | 技术选型 | 理由 |
|------|----------|------|
| 后端语言 | Go 1.26 | 单进程、高性能、跨平台 |
| Web 框架 | Gin | 轻量、高性能、生态丰富 |
| 前端 | 原生 HTML/CSS/JS | 无构建依赖、简洁 |
| 容器 | Docker (Alpine) | 镜像小（约 20MB） |
| 存储 | JSON 文件 | 简单可靠、无需数据库 |

### 2.3 端口规划

| 端口 | 用途 | 协议 |
|------|------|------|
| 443 | 代理服务入口 | HTTPS |
| 8442 | 配置页面 | HTTPS |

---

## 3. 数据流

### 3.1 Claude Code 请求流程

```
Claude Code                          透明代理                        后端 API
┌──────────┐                       ┌──────────┐                   ┌──────────┐
│          │  HTTPS :443           │          │   HTTPS          │          │
│  客户端  │ ────────────────────→ │  代理层  │ ───────────────→ │  后端    │
│          │  api.anthropic.com    │          │   配置的后端     │          │
└──────────┘                       └──────────┘                   └──────────┘
     │                                  │                              │
     │  1. DNS: api.anthropic.com       │                              │
     │     → 127.0.0.1 (hosts)          │                              │
     │                                  │                              │
     │  2. TLS 握手                     │                              │
     │     证书: api.anthropic.com      │                              │
     │     (本地CA签发)                 │                              │
     │                                  │                              │
     │  3. HTTP 请求                    │                              │
     │     Headers 完整透传             │   转发请求                   │
     │                                  │                              │
     │  4. 响应                         │   ←─────────────────────────│
     │     直接返回                     │   返回响应                   │
     └──────────────────────────────────┴──────────────────────────────┘
```

### 3.2 配置页面流程

```
浏览器                              配置服务
┌──────────┐                       ┌──────────┐
│          │  HTTPS :8442          │          │
│  用户    │ ────────────────────→ │  配置层  │
│          │                       │          │
└──────────┘                       └──────────┘
     │                                  │
     │  1. 访问配置页面                  │
     │     GET /                        │
     │                                  │
     │  2. 密码认证                      │
     │     POST /api/login              │
     │     返回 Session Cookie          │
     │                                  │
     │  3. 获取/更新配置                 │
     │     GET/PUT /api/config          │
     │     热更新生效                    │
     └──────────────────────────────────┴──────────┘
```

---

## 4. 模块设计

### 4.1 目录结构

```
claude-proxy/
├── cmd/
│   └── server/
│       └── main.go              # 入口，启动服务
│
├── internal/
│   ├── proxy/
│   │   ├── server.go            # 代理服务器
│   │   ├── handler.go           # 请求处理
│   │   └── transport.go         # HTTP 传输层
│   │
│   ├── config/
│   │   ├── manager.go           # 配置管理
│   │   ├── models.go            # 配置数据结构
│   │   └── store.go             # 持久化存储
│   │
│   ├── cert/
│   │   ├── ca.go                # CA 证书生成
│   │   ├── signer.go            # 证书签发
│   │   └── manager.go           # 证书生命周期管理
│   │
│   ├── admin/
│   │   ├── server.go            # 配置服务
│   │   ├── auth.go              # 密码认证
│   │   └── api.go               # REST API
│   │
│   └── frontend/
│       ├── embed.go             # 静态文件嵌入
│       └── dist/                # 前端构建产物
│
├── pkg/
│   └── utils/
│       ├── crypto.go            # 加密工具
│       └── network.go           # 网络工具
│
├── data/                        # 数据目录（Docker 映射）
│
├── Dockerfile
├── docker-compose.yml
├── go.mod
├── go.sum
├── Makefile
├── CLAUDE.md
├── README.md
└── AGENT.md
```

### 4.2 模块职责

| 模块 | 职责 |
|------|------|
| `proxy` | 处理 Claude Code 的 API 请求，TLS 终结，请求转发 |
| `config` | 配置的增删改查，持久化，热更新通知 |
| `cert` | CA 证书生成，域名证书签发，自动续期检查 |
| `admin` | 配置页面服务，密码认证，REST API |
| `frontend` | 配置页面前端代码，嵌入到二进制中 |

---

## 5. API 设计

### 5.1 配置服务 API (:8442)

| 方法 | 路径 | 描述 | 认证 |
|------|------|------|------|
| GET | `/` | 配置页面 | 需登录 |
| POST | `/api/login` | 密码登录 | 无 |
| POST | `/api/logout` | 退出登录 | 需登录 |
| GET | `/api/config` | 获取当前配置 | 需登录 |
| PUT | `/api/config` | 更新配置 | 需登录 |
| GET | `/api/certificates` | 获取证书信息 | 需登录 |
| POST | `/api/certificates/renew` | 手动续期证书 | 需登录 |
| GET | `/api/status` | 服务状态 | 需登录 |
| POST | `/api/config/test` | 测试后端连接 | 需登录 |

### 5.2 数据结构

```go
// 配置
type Config struct {
    BackendURL     string `json:"backend_url"`      // 后端代理地址
    AdminPassword  string `json:"-"`                // 管理密码（不返回前端）
}

// 证书信息
type CertificateInfo struct {
    CACertPath      string    `json:"ca_cert_path"`       // CA证书路径
    ServerCertPath  string    `json:"server_cert_path"`   // 服务器证书路径
    CAExpiresAt     time.Time `json:"ca_expires_at"`      // CA过期时间
    ServerExpiresAt time.Time `json:"server_expires_at"`  // 服务器证书过期时间
}

// 服务状态
type ServiceStatus struct {
    Running        bool      `json:"running"`          // 服务运行状态
    BackendURL     string    `json:"backend_url"`      // 当前后端地址
    Uptime         string    `json:"uptime"`           // 运行时长
    RequestsTotal  int64     `json:"requests_total"`   // 总请求数
    LastRequest    time.Time `json:"last_request"`     // 最后请求时间
}

// 登录请求
type LoginRequest struct {
    Password string `json:"password" binding:"required"`
}

// 配置更新请求
type ConfigUpdateRequest struct {
    BackendURL string `json:"backend_url" binding:"required,url"`
}

// 密码修改请求
type PasswordChangeRequest struct {
    OldPassword string `json:"old_password" binding:"required"`
    NewPassword string `json:"new_password" binding:"required,min=6"`
}
```

### 5.3 代理服务 (:443)

所有请求透明转发到配置的后端地址：

- HTTP Headers 完整透传（包括 `x-api-key`, `anthropic-version`, `Authorization` 等）
- Request Body 流式传输
- Response Body 流式传输
- Status Code 保持原始

---

## 6. 前端设计

### 6.1 页面布局

```
┌──────────────────────────────────────────────────────────────────┐
│  Claude Code 透明代理配置                              [退出登录] │
├──────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │ 服务状态                                                     ││
│  │  运行中 | 运行时长 | 请求总数                                ││
│  └─────────────────────────────────────────────────────────────┘│
│                                                                  │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │ 后端配置                                                     ││
│  │  后端地址输入框                                              ││
│  │  [测试连接]  [保存配置]                                      ││
│  └─────────────────────────────────────────────────────────────┘│
│                                                                  │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │ 证书信息                                                     ││
│  │  CA路径、有效期 | 服务器证书路径、有效期                     ││
│  │  NODE_EXTRA_CA_CERTS 配置提示 [复制]                         ││
│  │  [重新生成证书]                                              ││
│  └─────────────────────────────────────────────────────────────┘│
│                                                                  │
│  ┌─────────────────────────────────────────────────────────────┐│
│  │ 系统设置                                                     ││
│  │  管理密码修改                                                ││
│  └─────────────────────────────────────────────────────────────┘│
│                                                                  │
└──────────────────────────────────────────────────────────────────┘
```

### 6.2 技术实现

- 原生 HTML + CSS + JavaScript
- 无第三方依赖
- Fetch API 调用后端
- 响应式布局

---

## 7. 部署设计

### 7.1 Docker 配置

#### docker-compose.yml

```yaml
version: '3.8'
services:
  claude-proxy:
    build: .
    container_name: claude-proxy
    ports:
      - "443:443"      # 代理端口
      - "8442:8442"    # 配置页面
    volumes:
      - ./data:/app/data
    environment:
      - ADMIN_PASSWORD=${ADMIN_PASSWORD:-admin123}
    restart: unless-stopped
```

#### Dockerfile

```dockerfile
FROM golang:1.26-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o claude-proxy ./cmd/server

FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /app/claude-proxy .
RUN mkdir -p /app/data
EXPOSE 443 8442
ENTRYPOINT ["./claude-proxy"]
```

### 7.2 启动流程

```bash
# 1. 启动容器
docker-compose up -d

# 2. 容器首次启动自动：
#    - 生成 CA 证书（10年有效期）
#    - 签发 api.anthropic.com 证书
#    - 输出配置提示

# 3. 配置 hosts
echo "127.0.0.1 api.anthropic.com" | sudo tee -a /etc/hosts

# 4. 配置 Node.js 信任 CA
echo 'NODE_EXTRA_CA_CERTS=/path/to/data/ca.crt' >> ~/.bashrc
source ~/.bashrc

# 5. 访问配置页面
#    https://localhost:8442
```

---

## 8. 错误处理

### 8.1 错误处理策略

| 场景 | 处理方式 |
|------|----------|
| 后端不可达 | 返回 502 Bad Gateway，记录日志，前端显示警告 |
| 后端超时 | 默认 60 秒超时，返回 504 Gateway Timeout |
| 后端返回错误 | 透传错误响应，保持原始状态码和内容 |
| 证书过期 | 启动时自动检查，提前 30 天续期 |
| 配置文件损坏 | 使用默认配置，记录警告日志 |
| 密码错误 | 延迟 1 秒响应，5 次失败锁定 5 分钟 |

### 8.2 边界情况

| 场景 | 处理 |
|------|------|
| Claude Code 更新 | 透明代理不受影响 |
| 后端地址变更 | 热更新，无需重启 |
| 容器重启 | 自动加载已有证书和配置 |
| 首次启动 | 自动生成证书，输出配置提示 |
| 大文件上传 | 流式传输，不缓存内存 |
| 并发请求 | Go 协程处理，无阻塞 |

---

## 9. 测试策略

### 9.1 测试层次

| 层次 | 覆盖范围 | 工具 |
|------|----------|------|
| 单元测试 | 证书生成、配置管理、认证逻辑 | go test |
| 集成测试 | API 接口、代理转发 | go test + httptest |
| E2E 测试 | 完整流程 | 手动验证 |

### 9.2 测试用例

#### 单元测试

- CA 证书生成（有效期 10 年）
- 域名证书签发（CN=api.anthropic.com）
- 配置保存和加载
- 密码验证和锁定机制

#### 集成测试

- 请求转发正确性
- Header 完整透传
- 后端错误透传
- 登录/登出流程
- 配置热更新

#### E2E 测试

- Claude Code 完整请求流程
- 配置页面功能验证

### 9.3 覆盖率目标

- 单元测试：80%+
- 关键路径：100%

---

## 10. 非功能需求

### 10.1 性能

| 指标 | 目标 |
|------|------|
| 代理延迟 | < 10ms（不含后端） |
| 并发连接 | 100+ |
| 内存占用 | < 50MB |
| 镜像大小 | < 30MB |

### 10.2 安全

| 项目 | 措施 |
|------|------|
| 密码存储 | bcrypt 哈希 |
| Session | HttpOnly, Secure Cookie |
| TLS | TLS 1.2+ |
| 登录保护 | 5 次失败锁定 5 分钟 |

### 10.3 可维护性

- 结构化日志（JSON 格式）
- 健康检查端点
- 优雅关闭

---

## 11. 里程碑

| 阶段 | 内容 | 交付物 |
|------|------|--------|
| Phase 1 | 核心代理功能 | 可运行的代理服务 |
| Phase 2 | 证书管理 | 自动证书生成和续期 |
| Phase 3 | 配置服务 | 前端页面和 API |
| Phase 4 | 测试和文档 | 测试用例、README、CLAUDE.md、AGENT.md |

---

## 12. 参考资料

- Claude Code 源码: `/home/www/workspace/github/claude-code`
- Claude Code GitHub: `<https://github.com/anthropics/claude-code>`
- ACME.sh: `<https://github.com/acmesh-official/acme.sh>`
