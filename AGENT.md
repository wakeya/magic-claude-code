# AGENT.md - 开发指南

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

1. 更新版本号
2. 运行完整测试套件
3. 构建发布镜像
4. 推送到镜像仓库

```bash
# 构建发布镜像
docker build -t magic-claude-code:latest .

# 运行发布镜像
docker run -d \
  --name mcc \
  -p 443:443 \
  -p 8442:8442 \
  -v ./data:/app/data \
  -e ADMIN_PASSWORD=your-password \
  magic-claude-code:latest

```
