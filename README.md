# Magic Claude Code

[English](README.en.md) | 简体中文

满血使用 Claude Code 客户端，MCC Proxy 让你拥有 Claude Code 客户端的完整功能，调用高性价比第三方模型，成本可降低 80%+。

## 💡 为什么用 MCC Proxy

### 避免客户端功能降级

当 ANTHROPIC_BASE_URL 不是 [https://api.anthropic.com](https://api.anthropic.com) 时，Claude Code 客户端会有很多功能降级，具体包含：记忆系统、子代理优化、工具与交互、MCP/WebFetch 调用网络不稳等。

### 降低 token 成本

Claude Code 官方订阅昂贵，而中国产开源大模型性价比极高。MCC Proxy 让你**拥有 Claude Code 客户端的完整功能体验，调用高性价比第三方模型**，成本可降低 80%+：


| 官方模型          | 可替换为                                     | 场景         |
| ------------- | ---------------------------------------- | ---------- |
| claude-opus   | **GLM-5.2** / **MiniMax-M3**             | 复杂编码、深度推理  |
| claude-sonnet | **kimi-k2.7-code** / **deepseek-v4-pro** | 日常编码、代码生成  |
| claude-haiku  | **mimo-v2.5-pro** / **agnes-2.0-flash**  | 快速响应、子代理任务 |


通过模型映射自动转换，Claude Code 无感切换，遥测/特性开关等硬编码请求也在网络层被拦截，避免因使用第三方 API 而被禁用优化功能。

## 🎬 功能演示

![MCC Proxy 功能演示](images/visual_zh.gif)

## ✨ 功能特性

- 透明代理所有 Claude Code API 请求
- **多供应商管理**：支持配置多个 API 供应商，灵活切换
- **模型映射**：自动将请求模型名转换为供应商支持的高性价比模型（GLM-5.2、MiniMax-M3、kimi-k2.7-code、deepseek-v4-pro、mimo-v2.5-pro、agnes-2.0-flash 等），大幅降低使用成本
- 自动生成 CA 证书（10年有效期）
- 前端配置页面管理供应商和模型映射
- 密码保护配置页面
- **优先推荐二进制运行**：首次启动可自动引导 hosts、CA 信任和客户端环境变量配置
- Docker 单容器部署（适合服务器或容器化运维场景）
- 热更新配置无需重启
- **自动引导**：启动时自动尝试 hosts 修改、CA 信任安装、环境持久化

## 🔗 连接模式

系统支持三种连接模式，按优先级自动降级：


| 优先级 | 模式       | 入口                         | 能否拦截硬编码 `api.anthropic.com` | 权限要求                |
| --- | -------- | -------------------------- | --------------------------- | ------------------- |
| 1   | **透明模式** | hosts + 443 TLS            | ✅ 可以                        | 需要主机修改权限和 CA 信任     |
| 2   | **隧道模式** | HTTPS_PROXY + CONNECT MITM | ⚠ 大体可以                      | 不修改 hosts；运行时需信任 CA |
| 3   | **网关模式** | ANTHROPIC_BASE_URL         | ❌ 不可以                       | 不需要 hosts/CA/443    |


### 自动引导

启动时按优先级自动尝试：

1. 确保 CA 证书存在
2. 尝试修改 hosts（`127.0.0.1 api.anthropic.com`）
3. 尝试安装/信任 CA 到系统证书库
4. 尝试持久化客户端所需环境变量（如 `NODE_EXTRA_CA_CERTS`、Linux `SSL_CERT_FILE`）
5. 尝试持久化可执行文件目录为 MCC 根目录

**首次运行建议使用二进制**。管理员权限可自动完成 hosts、系统 CA 信任和 443 端口相关配置；普通用户环境中的 `NODE_EXTRA_CA_CERTS`、Linux `SSL_CERT_FILE` 会按当前运行用户尽量持久化。如果权限不足，系统会：

- 记录明确的失败原因
- 打印缺失能力说明
- 输出对应的后备模式启动命令
- 不阻塞代理启动

### 日志语言规则

- 中文系统（`zh*` locale）→ 中文日志和指引
- 其他语言 → 英文日志和指引
- 可通过 `MCC_LANG` 环境变量手动指定

### 何时降级

- **透明模式失败**（hosts 修改或 CA 安装失败）→ 自动降级到隧道模式
- **隧道模式不可用**（无法设置代理环境变量）→ 降级到网关模式
- 网关模式仅覆盖尊重 `ANTHROPIC_BASE_URL` 的客户端，无法拦截硬编码请求

### ⚖️ 模式优缺点对比

**透明模式 / 隧道模式**（推荐）——优点：

- **全量拦截**：所有 `api.anthropic.com` 请求（含 BigQuery 指标、1P 事件日志、GrowthBook 特性开关）在网络层/代理层被拦截，无直连泄漏
- **零启动延迟**：GrowthBook、指标检查等由代理本地即时响应，不再等待 5~10 秒超时
- **功能完整**：GrowthBook 正常加载，记忆搜索（coral_fern）、子代理精简（slim_subagent_claudemd）、工具优化（birch_trellis 等）全部按配置生效
- **TLS 加密**：443 端口提供标准 HTTPS
- **硬编码覆盖**：拦截不走 `ANTHROPIC_BASE_URL` 的直连请求

**路由模式**（最低权限后备）——局限性：

- **遥测直连**：BigQuery 指标、1P 事件日志等绕过代理直连 Anthropic，无法拦截
- **启动卡顿**：GrowthBook 初始化直连 `api.anthropic.com`，国内网络超时 5~10 秒；失败请求持久化重试，形成反复超时循环
- **功能降级**：GrowthBook 加载失败时，记忆系统、子代理优化、工具交互（Bash 权限树、剪贴板图片、深度思考等）无法按服务端配置启用
- **MCP/WebFetch 受影响**：MCP 官方注册表、WebFetch 域名安全检查均直连 `api.anthropic.com`，超时影响使用
- **仅本地 HTTP 明文**，无 TLS 加密，不适合跨网络使用
- **每个客户端需单独配置** `ANTHROPIC_BASE_URL`，无法全局透明拦截

> 完整覆盖所有流量（含遥测、特性开关等）请使用**透明模式**或**隧道模式**。

> 顶部 Header 中的模式入口可查看三种模式的详细说明。

## 📦 发布说明

版本发布由 `v*` tag 触发 CI 自动构建。GitHub/GitLab 会生成并上传跨平台二进制资产；Gitee/GitCode 通过 Release API 上传附件。开发者提交与发布步骤见 [AGENTS.md](AGENTS.md)。

## 🚀 快速开始

### 1. 使用二进制运行（推荐）

二进制运行是推荐方式。首次启动时，MCC 会尽量自动完成透明模式所需配置：

- 修改 hosts：`127.0.0.1 api.anthropic.com`
- 安装/信任本地 CA 证书
- 持久化 `NODE_EXTRA_CA_CERTS`
- Linux 上验证系统 CA bundle 并持久化 `SSL_CERT_FILE`
- 持久化 `MCC_ROOT`，方便之后从任意目录启动

> Docker 容器不能直接修改宿主机 hosts、系统证书库和宿主 shell profile。因此如果目标是让本机 Claude Code/Orca 最少手工配置，优先使用二进制。
>
> Linux 上 `sudo ./mcc` 会处理系统级配置，但不会代替普通用户修改 shell profile。若启动日志提示用户环境变量未持久化，请按日志提示以普通用户补齐，或完成系统信任后用普通用户重新运行一次二进制。

#### Linux / macOS

下载 Release 中对应平台的压缩包，解压后在目录中启动：

```bash
# 首次运行可使用 sudo，让自动引导完成 hosts、CA 信任和 443 端口相关配置
sudo ./mcc -data ./data -password "your-admin-password"
```

如果你从源码构建：

```bash
git clone <repo-url>
cd magic-claude-code

npm --prefix internal/frontend ci
npm --prefix internal/frontend run build
make build

sudo ./bin/mcc -data ./data -password "your-admin-password"
```

启动成功后，完全退出并重新打开 Claude Code/Orca 所在终端或应用，使新进程继承自动写入的环境变量。Linux 上可以检查：

```bash
echo "$NODE_EXTRA_CA_CERTS"
echo "$SSL_CERT_FILE"
```

期望 `NODE_EXTRA_CA_CERTS` 指向 MCC 的 `data/ca.crt`，`SSL_CERT_FILE` 指向完整系统 CA bundle，例如 `/etc/ssl/certs/ca-certificates.crt`。如果为空，请按启动日志提示在普通用户 shell 中持久化，或完成系统配置后以普通用户重新运行一次二进制。

#### Windows

下载 Windows Release 包后，推荐放在固定目录：

```text
C:\mcc\
  mcc.exe
  data\
```

用管理员 PowerShell 启动：

```powershell
cd C:\mcc
.\mcc.exe -data .\data -password "your-admin-password"
```

启动成功后关闭并重新打开终端，再启动 Claude Code。

### 2. 使用 Docker 部署（备选）

Docker 适合服务器部署或已经有容器化运维流程的场景。注意：Docker 内的自动引导无法直接修改宿主机 hosts、宿主机 CA 信任库或宿主 shell profile；这些宿主配置需要按日志提示手动完成，或使用后文的宿主机 helper。

#### 方式一：使用 docker build

```bash
# 克隆项目
git clone <repo-url>
cd magic-claude-code

# 构建镜像
docker build -t magic-claude-code .

# 运行容器
docker run -d \
  --name mcc \
  -p 443:443 \
  -p 8442:8442 \
  -v ./data:/app/data \
  -v "${CLAUDE_PROJECTS_DIR:-$HOME/.claude/projects}:/claude-projects:ro" \
  -e ADMIN_PASSWORD=admin123 \
  -e CLAUDE_PROJECTS_DIR=/claude-projects \
  --cap-add NET_BIND_SERVICE \
  --restart=unless-stopped \
  magic-claude-code

# 查看日志获取配置提示
docker logs mcc
```

#### 方式二：使用 docker-compose

```bash
# 克隆项目
git clone <repo-url>
cd magic-claude-code

# 安装 docker-compose-plugin（如果未安装）
sudo apt-get install docker-compose-plugin

# 启动服务
docker compose up -d

# 查看日志获取配置提示
docker logs mcc
```

### 3. Docker 构建和验证

```bash
# 1. 测试构建镜像
docker compose build

# 2. 查看构建的镜像
docker images | grep magic-claude-code

# 3. 启动服务
docker compose up -d

# 4. 查看服务日志
docker compose logs -f

# 5. 验证服务是否正常运行
curl -k https://localhost:8442

# 6. 查看容器状态
docker compose ps
```

### 4. Docker 重新打包部署

代码更新后，重新构建镜像并重启容器：

```bash
# 一键重新构建并部署
docker compose up -d --build

# 或者用 Make（克隆仓库后可用，自动注入 git 版本号到 mcc --version）
make docker-run

# 查看启动日志
docker compose logs -f
```

### 5. 辅助脚本说明

仓库的 `scripts/` 目录包含宿主机配置和发布辅助脚本：

| 脚本 | 用途 | 典型用法 |
| --- | --- | --- |
| `scripts/setup-host.sh` | Linux/macOS 宿主机配置脚本，负责 hosts 映射和 CA 信任安装 | `sudo ./scripts/setup-host.sh`、`sudo ./scripts/setup-host.sh hosts`、`sudo ./scripts/setup-host.sh trust` |
| `scripts/setup-host.ps1` | Windows 宿主机配置脚本，负责 hosts 映射和 CA 信任安装 | 管理员 PowerShell: `.\scripts\setup-host.ps1`、`.\scripts\setup-host.ps1 -Action hosts`、`.\scripts\setup-host.ps1 -Action trust` |
| `scripts/docker-host-helper.sh` | Docker 场景的宿主机状态 helper。容器通过 `MCC_HOST_HELPER` 调用它判断宿主机 hosts/CA 是否已配置 | 先在宿主机运行 `setup-host.sh`，再把 helper 挂载进容器并设置 `MCC_HOST_HELPER` |
| `scripts/start-mcc.ps1` / `scripts/stop-mcc.ps1` | Windows 二进制后台启动/停止脚本，写入 `logs/` 和 `mcc.pid` | 在 `mcc.exe` 同目录运行 `.\start-mcc.ps1`、`.\stop-mcc.ps1` |
| `scripts/register-mcc-task.ps1` | Windows 注册当前用户登录时自动启动 mcc 的计划任务 | 管理员 PowerShell: `.\register-mcc-task.ps1 -Force` |
| `scripts/release.sh` | 维护者发布脚本，用于跨平台构建并上传 Gitee/GitCode/GitLab Release | 发布版本时运行，不是普通用户启动所需 |

Release 资产包根目录会带上 `README.md`、`README.en.md`、`SCRIPTS.md`、`SCRIPTS.en.md`。Linux/macOS 包会带上 `setup-host.sh` 和 `docker-host-helper.sh`；Windows 包会带上 `setup-host.ps1`、`start-mcc.ps1`、`stop-mcc.ps1`、`register-mcc-task.ps1`。正常情况下优先运行二进制，让 bootstrap 自动处理；这些脚本主要用于自动引导失败、Docker 宿主机预配置、Windows 后台启动/开机自启，或需要单独配置 hosts/CA 的场景。

### 6. 二进制运行详细说明

服务会固定监听：

- `443`：代理服务入口，用于接收 `https://api.anthropic.com` 请求
- `8442`：配置页面

> **💡 快速配置**：Release 资产包内已附带宿主机配置脚本，可一键完成 hosts 映射 + CA 信任安装（含国内镜像 fallback，apt 海外源失败时自动切阿里云/清华）。从源码构建时脚本位于 `scripts/` 目录。
>
> ```bash
> # Linux / macOS（需要 sudo）
> sudo ./setup-host.sh
>
> # Windows（管理员 PowerShell）
> .\setup-host.ps1
> ```
>
> 支持选择性配置：`setup-host.sh hosts`（只改 hosts）、`setup-host.sh trust`（只装 CA）。
>
> 正常情况下管理员权限启动 `./mcc` 会自动引导完成 hosts 和系统 CA 信任等系统级配置；脚本仅在自动配置失败或需要单独操作时使用。Linux 普通用户 shell 环境变量仍以启动日志提示为准。

#### macOS / Linux

从源码构建：

```bash
# 克隆项目
git clone <repo-url>
cd magic-claude-code

# 构建前端静态资源
npm --prefix internal/frontend ci
npm --prefix internal/frontend run build

# 构建二进制
make build
```

启动服务：

```bash
# Linux 可先授予绑定 443 端口的能力
sudo setcap 'cap_net_bind_service=+ep' ./bin/mcc

# 启动，建议显式设置管理密码
./bin/mcc -data ./data -password "your-admin-password"
```

如果没有 `setcap`，可以用管理员权限启动：

```bash
sudo ./bin/mcc -data ./data -password "your-admin-password"
```

添加 hosts 映射：

```bash
echo "127.0.0.1 api.anthropic.com" | sudo tee -a /etc/hosts
```

配置 Node.js 读取代理 CA：

```bash
echo 'export NODE_EXTRA_CA_CERTS=/absolute/path/to/magic-claude-code/data/ca.crt' >> ~/.bashrc
source ~/.bashrc
```

Linux 二进制 bootstrap 还会尝试把 MCC CA 安装并验证到完整系统 CA bundle，并为后续启动的客户端持久化 `SSL_CERT_FILE`。如果自动配置失败，可在确认系统 bundle 已包含 MCC CA 后手动设置：

```bash
export SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt
```

请完全退出并重新启动 Claude Code/Orca，使新进程继承该变量。`SSL_CERT_FILE` 必须指向完整系统 bundle，**不要**指向单个 `data/ca.crt`，否则部分 TLS 实现会失去对正常公网证书的信任。使用 `sudo` 启动时，bootstrap 不会代替普通用户修改 shell profile；请按启动提示以普通用户持久化变量或手动加入相应 profile。

#### Windows

Windows 可直接使用 `mcc.exe`。如果在 Windows 本机从源码构建：

```powershell
npm --prefix internal/frontend ci
npm --prefix internal/frontend run build
go build -o mcc.exe ./cmd/server
```

也可以在 macOS/Linux 上交叉编译 Windows 版本：

```bash
GOOS=windows GOARCH=amd64 go build -o bin/mcc-windows-amd64/mcc.exe ./cmd/server
```

推荐目录结构：

```text
C:\mcc\
  mcc.exe
  data\
```

用管理员 PowerShell 启动：

```powershell
cd C:\mcc
.\mcc.exe -data .\data -password "your-admin-password"
```

如果直接运行 `.\mcc.exe`，数据目录默认使用当前目录下的 `.\data`。未通过 `-password` 或 `ADMIN_PASSWORD` 指定密码时，程序会随机生成管理密码，并在启动输出中打印一次；后台运行时请从 stdout 日志中查看 `随机生成的管理密码`。

添加 hosts 映射：

```powershell
Add-Content -Path "$env:WINDIR\System32\drivers\etc\hosts" -Value "`n127.0.0.1 api.anthropic.com"
```

导入 CA 证书到当前用户信任根：

```powershell
Import-Certificate -FilePath "C:\mcc\data\ca.crt" -CertStoreLocation Cert:\CurrentUser\Root
```

如果需要对所有用户生效，用管理员 PowerShell 导入到本机信任根：

```powershell
Import-Certificate -FilePath "C:\mcc\data\ca.crt" -CertStoreLocation Cert:\LocalMachine\Root
```

配置 Node.js 读取代理 CA：

```powershell
setx NODE_EXTRA_CA_CERTS "C:\mcc\data\ca.crt"
```

设置后关闭并重新打开终端，再启动 Claude Code。

如果 443 或 8442 端口被占用，可以检查占用进程：

```powershell
netstat -ano | findstr ":443"
netstat -ano | findstr ":8442"
```

如果 hosts 修改后不生效，可以刷新 DNS：

```powershell
ipconfig /flushdns
```

#### 二进制运行注意事项

- **首次运行建议使用二进制**；系统级配置需要管理员权限，普通用户 shell 环境变量按当前运行用户持久化。
- 引导失败不会阻塞启动，会自动降级到隧道模式或网关模式（参见"连接模式"）。
- 第一次启动会在 `data` 目录生成 `ca.crt`、`ca.key`、`server.crt`、`server.key` 和 `proxy.db`。
- 首次成功运行会尝试将可执行文件目录持久化为 `MCC_ROOT`，之后从任意工作目录启动都能自动定位证书。
- 建议始终通过 `-password` 或 `ADMIN_PASSWORD` 设置管理密码；未设置时程序会随机生成密码，并在启动输出中打印一次。
- 使用统计默认读取当前用户的 Claude Code session 目录：`~/.claude/projects`。如需覆盖，可设置 `CLAUDE_PROJECTS_DIR`。

### 7. 安装 CA 证书

代理使用自签名 CA 证书，需要在客户端机器上安装信任。有以下三种方式：

#### 方式一：指定证书路径（最简单）

直接通过环境变量指定证书文件路径。

```bash
# 将 ca.crt 路径添加到环境变量
echo 'export NODE_EXTRA_CA_CERTS=/path/to/data/ca.crt' >> ~/.bashrc
source ~/.bashrc
```

#### 优点

- 配置简单，一行命令搞定
- 兼容所有 Node.js 版本

#### 缺点

- 仅 Node.js 应用使用此证书
- 项目迁移时需要更新路径
- 如果证书位置变化，需要修改环境变量

#### 方式二：系统证书库 + Node.js 系统证书支持（推荐）

将证书安装到系统证书库，并让 Node.js 读取系统证书。

**macOS：**

```bash
# 1. 安装证书到系统钥匙串
sudo security add-trusted-cert -d -r trustRoot \
  -k /Library/Keychains/System.keychain ./data/ca.crt

# 2. 设置 Node.js 使用系统证书库（需要 Node.js 16+）
echo 'export NODE_OPTIONS="--use-system-ca"' >> ~/.bashrc
source ~/.bashrc

# 3. 配置桌面环境变量（让 GUI 应用也能使用证书）
# macOS 使用 launchctl setenv 设置环境变量
launchctl setenv NODE_OPTIONS "--use-system-ca"

# 要使配置永久生效，在 ~/.zshrc 或 ~/.bash_profile 中添加（推荐）
echo 'export NODE_OPTIONS="--use-system-ca"' >> ~/.zshrc  # zsh 用户
```

**Linux (Ubuntu/Debian)：**

```bash
# 1. 复制证书到系统证书目录
sudo cp ./data/ca.crt /usr/local/share/ca-certificates/

# 2. 更新证书库
sudo update-ca-certificates

# 如果看到 "skipping duplicate certificate" 警告，说明证书已安装，无需担心

# 3. 设置 Node.js 使用系统证书库（需要 Node.js 16+）
echo 'export NODE_OPTIONS="--use-system-ca"' >> ~/.bashrc
source ~/.bashrc

# 4. 配置桌面环境变量（让 GUI 应用也能使用证书）
cat >> ~/.xprofile << 'EOF'
# Claude Code 代理证书
export NODE_OPTIONS="--use-system-ca"
EOF

# 注销并重新登录桌面环境，或重启系统使 .xprofile 生效
```

#### 方式二优点

- 系统级信任，所有应用都能识别证书（curl、wget、浏览器等）
- Node.js 也使用系统证书库
- 证书更新后无需修改环境变量路径
- 多项目环境下配置统一

#### 方式二注意

- 需要 Node.js 16 或更高版本
- 仍需配置 `NODE_OPTIONS` 环境变量
- **桌面应用需要额外配置**：从桌面环境启动的应用

  （如 VS Code）不会加载 `~/.bashrc`，需要配置 `~/.xprofile`

#### 为什么需要 .xprofile？


| 文件            | 加载时机       | 作用范围   | 适用场景                 |
| ------------- | ---------- | ------ | -------------------- |
| `~/.bashrc`   | 打开终端时      | 仅终端会话  | 命令行工具、脚本             |
| `~/.profile`  | 登录 Shell 时 | 登录会话   | SSH、TTY 登录           |
| `~/.xprofile` | 桌面登录时      | 整个桌面环境 | GUI 应用（VS Code、浏览器等） |


从桌面环境启动的 GUI 应用不会继承 `~/.bashrc` 的环境变量，因此需要通过 `~/.xprofile` 在桌面登录时设置环境变量，确保所有桌面应用都能正确使用证书。

#### 方式三：仅系统级信任（不推荐单独使用）

仅将证书安装到系统，不配置 Node.js 环境变量。

```bash
# Linux
sudo cp ./data/ca.crt /usr/local/share/ca-certificates/
sudo update-ca-certificates

# macOS
sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain ./data/ca.crt
```

#### 适用场景

- 非 Node.js 应用（curl、wget、浏览器等）
- 配合方式一使用（系统工具 + Node.js 分别配置）

#### 注意

- ⚠️ Node.js 默认不会读取系统证书库
- 需要配合方式一或方式二的 `NODE_OPTIONS` 使用

### 8. 浏览器证书导入

系统证书库安装的 CA 证书只对命令行工具（curl、wget）生效。Chrome 和 Firefox 使用独立的 NSS 证书数据库，不读取系统 CA，需要单独导入。

> **前置条件**：先完成上述系统证书库安装（方式二或方式三），再执行以下步骤。

#### 通用工具：安装 certutil

```bash
sudo apt install libnss3-tools -y
```

`certutil` 是管理 NSS 数据库的命令行工具，Chrome 和 Firefox（apt 版）均使用它导入证书。

#### Chrome（Linux）

Chrome 在所有安装方式下都使用 `~/.pki/nssdb` 存储证书：

```bash
# 导入 CA 到 Chrome NSS 数据库
certutil -d sql:$HOME/.pki/nssdb -A -t "C,," -n "Claude Proxy Local CA" \
  -i /path/to/data/ca.crt

# 验证
certutil -d sql:$HOME/.pki/nssdb -L | grep -i "Claude"
```

导入完成后**完全关闭 Chrome**（包括所有窗口），重新打开即可。

#### Firefox（Linux）

Firefox 的 NSS 数据库路径因安装方式而异：

```bash
# 判断当前 Firefox 是 snap 版还是 apt 版
which firefox | xargs ls -la 2>/dev/null | grep snap
```

##### apt 版 Firefox

apt 版 Firefox 使用 `~/.mozilla/firefox/` 下的标准 NSS 数据库：

```bash
# 查找配置文件夹
PROFILE=$(ls ~/.mozilla/firefox/*/cert9.db 2>/dev/null | cut -d/ -f5 | head -1)
echo "Profile: $PROFILE"

# 导入 CA
certutil -d sql:$HOME/.mozilla/firefox/${PROFILE} \
  -A -t "C,," -n "Claude Proxy Local CA" \
  -i /path/to/data/ca.crt

# 验证
certutil -d sql:$HOME/.mozilla/firefox/${PROFILE} -L | grep -i "Claude"
```

##### snap 版 Firefox

snap 版 Firefox 运行在沙盒隔离环境中，**命令行导入不一定生效**，推荐以下两种方式：

方式一：通过 Firefox 界面手动导入（推荐）

1. 地址栏输入 `about:preferences#privacy`，滚动到 **证书** 部分
2. 点击 **查看证书** → 切换到 **授权机构** 标签页
3. 点击 **导入** → 选择 `data/ca.crt`
4. 勾选 **"信任此证书用于识别网站"** → 确定

方式二：改用 apt 版 Firefox（彻底解决）

snap 沙盒是根本限制，推荐换成 apt 版：

```bash
sudo snap disable firefox
sudo apt install firefox -y
```

之后按上述 **apt 版 Firefox** 步骤导入证书即可。

#### snap vs apt 浏览器对比


| 安装方式         | 特点               | 系统 CA 读取 | certutil 导入 |
| ------------ | ---------------- | -------- | ----------- |
| apt 版浏览器     | 标准安装，运行在主机环境     | 支持       | 直接生效        |
| snap 版浏览器    | 沙盒隔离，限制访问主机资源    | 不支持      | 可能不生效       |
| Chrome（任意方式） | 均使用 ~/.pki/nssdb | 不支持      | 直接生效        |


snap 版 Firefox 因为沙盒限制，即便 certutil 写入成功，Firefox 进程也可能读不到。因此 snap 版推荐 **通过 Firefox 界面导入** 或 **改用 apt 版**。

### 9. 配置系统

```bash
# 添加 hosts 映射（将 api.anthropic.com 指向代理服务器）
echo "127.0.0.1 api.anthropic.com" | sudo tee -a /etc/hosts
```

### 10. 访问配置页面

打开浏览器访问: `https://localhost:8442`

Docker 默认密码: `admin123`。二进制运行请使用启动时通过 `-password` 或 `ADMIN_PASSWORD` 设置的密码；未设置时会随机生成并在启动输出中打印一次。

## ⚙️ 配置说明

### 端口权限

代理服务需要绑定 443 端口（HTTPS 默认端口），这是一个特权端口（&lt; 1024），需要特殊权限。

**Docker 部署：**

`docker-compose.yml` 已配置 `cap_add: NET_BIND_SERVICE`，允许容器内进程绑定特权端口：

```yaml
cap_add:
  - NET_BIND_SERVICE
```

**直接运行（非 Docker）：**

需要授予二进制文件绑定特权端口的能力：

```bash
# 方式一：使用 setcap（推荐）
sudo setcap 'cap_net_bind_service=+ep' ./bin/mcc

# 方式二：使用 sudo 运行
sudo ./bin/mcc -data ./data
```

**注意：** 不推荐修改为非特权端口（如 8443），因为：

- 客户端请求的是 `https://api.anthropic.com`，默认使用 443 端口
- 通过 hosts 文件将域名解析到 127.0.0.1，但无法改变端口号
- 如果代理监听非 443 端口，客户端请求会失败

如果必须使用非特权端口，需要：

```bash
# 使用 iptables 将 443 端口转发到 8443
sudo iptables -t nat -A PREROUTING -p tcp --dport 443 -j REDIRECT --to-port 8443
sudo iptables -t nat -A OUTPUT -p tcp --dport 443 -j REDIRECT --to-port 8443
```

### 环境变量


| 环境变量                | 说明                                                                               | 默认值              |
| ------------------- | -------------------------------------------------------------------------------- | ---------------- |
| ADMIN_PASSWORD      | 管理密码                                                                             | admin123         |
| CLAUDE_PROJECTS_DIR | 容器内 Claude Code session 日志目录，用于使用统计自动补账                                          | /claude-projects |
| MCC_LANG            | 手动指定日志语言（`zh` 或 `en`）                                                            | 系统自动检测           |
| MCC_ROOT            | MCC 安装根目录，用于证书发现                                                                 | 可执行文件目录          |
| MCC_HOST_HELPER     | 宿主机 helper 可执行脚本路径（Docker 场景，绝对路径；容器会直接执行该脚本的 `hosts add` / `trust install` 子命令） | 未设置              |


### 使用统计自动补账目录

使用统计会自动扫描 Claude Code 本地 session JSONL 日志，并把其中已完成请求的 usage 导入统计库。服务启动时会同步一次，之后每分钟自动同步一次。

容器内统一读取：

```text
/claude-projects
```

宿主机路径需要通过 Docker volume 挂载到该目录。

**Linux / macOS：**

默认使用当前用户的 Claude Code session 目录：

```text
$HOME/.claude/projects
```

如果使用 docker-compose，默认配置已经包含该挂载：

```yaml
- ${CLAUDE_PROJECTS_DIR:-${HOME}/.claude/projects}:/claude-projects:ro
```

如果你的 Claude Code 日志不在默认目录，可以在启动前覆盖：

```bash
export CLAUDE_PROJECTS_DIR=/path/to/.claude/projects
docker compose up -d --build
```

**Windows：**

Windows 的用户目录路径不同，不能依赖 Linux/macOS 的 `$HOME/.claude/projects` 默认值。请显式设置 `CLAUDE_PROJECTS_DIR`，并优先使用 `/` 作为路径分隔符，避免 Docker volume 中的 `:` 和反斜杠转义问题。

PowerShell 示例：

```powershell
$env:CLAUDE_PROJECTS_DIR="C:/Users/你的用户名/.claude/projects"
docker compose up -d --build
```

`.env` 示例：

```env
CLAUDE_PROJECTS_DIR=C:/Users/你的用户名/.claude/projects
```

然后启动：

```powershell
docker compose up -d --build
```

注意事项：

- Docker Desktop 需要允许挂载该用户目录。
- 如果路径不存在，服务仍会启动，但不会导入 session 日志，使用统计里的补账数据会为空。
- 应用代码只读取容器内的 `/claude-projects`，宿主机路径差异由 Docker volume 处理。

### 供应商配置

在前端配置页面添加供应商：


| 字段        | 说明            | 示例                                                  |
| --------- | ------------- | --------------------------------------------------- |
| 名称        | 供应商显示名称       | 阿里云 DashScope                                       |
| API 地址    | 供应商的 API 端点   | `<https://dashscope.aliyuncs.com/api/v1/anthropic>` |
| API Token | 认证密钥          | sk-xxx                                              |
| 模型映射      | 客户端模型 → 供应商模型 | claude-sonnet-4 → qwen-max                          |


### 连接模式

顶部 header 里的“连接模式”区域会显示当前首选模式、实际生效模式和三种模式切换按钮。切换按钮会持久化到后端配置，下一次启动时生效。


| 模式   | 用途                 | `~/.claude/settings.json`                                                        |
| ---- | ------------------ | -------------------------------------------------------------------------------- |
| 透明模式 | 优先级最高，能拦截硬编码端点     | 可保持默认，或显式设置 `ANTHROPIC_BASE_URL=https://api.anthropic.com`                       |
| 隧道模式 | 不改 hosts，靠代理环境变量转发 | `HTTPS_PROXY=https://127.0.0.1:443` + `NODE_EXTRA_CA_CERTS=/path/to/data/ca.crt` |
| 网关模式 | 最低权限后备，只覆盖显式配置的客户端 | `ANTHROPIC_BASE_URL=http://127.0.0.1:17487`                                      |


#### 透明模式

透明模式优先级最高，适合希望尽量不改客户端的场景。第一次运行建议使用管理员权限，让程序自动尝试修改 hosts 和导入 CA 信任。

`~/.claude/settings.json` 可保持默认；如果你想显式声明官方端点，可以写成：

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "https://api.anthropic.com",
    "ANTHROPIC_API_KEY": "any-non-empty"
  }
}
```

> **⚠️ 让 `/model` 菜单出现跨 provider 模型**
>
> Claude Code 启动时向 `/api/claude_cli/bootstrap` 拉取额外模型选项，mcc 在此注入你在管理后台配置的 ExposedModels。要让它生效，客户端需满足两个前提：
>
> 1. **认证用 `ANTHROPIC_API_KEY`，不要用 `ANTHROPIC_AUTH_TOKEN`**：Claude Code 的 bootstrap 只认 OAuth 或 `x-api-key`。mcc 会用供应商 token 覆盖请求里的认证头，因此这个值填任意非空值即可（示例里用 `"any-non-empty"`）。
> 2. **不要设置 `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`**：否则 Claude Code 会把 bootstrap 当作非必要流量直接跳过，`/model` 菜单不会刷新出自定义模型。
>
> 隧道模式同理（它也不改 `ANTHROPIC_BASE_URL`，Claude Code 仍以 firstParty 自居发 bootstrap）。网关模式改了 `ANTHROPIC_BASE_URL`，Claude Code 进入 3P provider 模式、不发 bootstrap，因此 ExposedModels 不会出现在 `/model` 菜单。

#### 隧道模式

隧道模式不修改 hosts，依赖 `HTTPS_PROXY` 和 `NODE_EXTRA_CA_CERTS`。适合主机权限受限，或者你只想让支持代理环境变量的客户端工作。

`~/.claude/settings.json` 示例：

```json
{
  "env": {
    "HTTPS_PROXY": "https://127.0.0.1:443",
    "NODE_EXTRA_CA_CERTS": "/path/to/magic-claude-code/data/ca.crt",
    "ANTHROPIC_API_KEY": "any-non-empty"
  }
}
```

保存后重启 Claude Code。

> 同样地，要让 `/model` 菜单出现跨 provider 模型，请遵守上方透明模式列出的两个客户端前提（认证用 `ANTHROPIC_API_KEY`、不要设置 `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC`）。

#### 网关模式

网关模式只覆盖尊重 `ANTHROPIC_BASE_URL` 的客户端，不能拦截硬编码 `api.anthropic.com` 请求。它是最低权限后备，不依赖 hosts 和系统 CA 信任库。

`~/.claude/settings.json` 示例：

```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:17487"
  }
}
```

保存后重启 Claude Code。

代理会自动将 `claude-sonnet-4` 转换为供应商配置的模型（如 `glm-5`）。

## ✅ 首次运行检查清单


| 步骤          | 自动完成 | 说明                              |
| ----------- | ---- | ------------------------------- |
| 生成 CA 证书    | ✅    | 启动时自动生成到 `data/ca.crt`          |
| 修改 hosts    | 尝试   | 需要管理员权限；失败会降级                   |
| 安装 CA 信任    | 尝试   | 需要管理员权限；失败会降级                   |
| 持久化 MCC 根目录 | 尝试   | 写入 shell profile 或 Windows 环境变量 |
| 持久化客户端 CA 环境 | 尝试   | Linux 写 `NODE_EXTRA_CA_CERTS` 和系统 bundle `SSL_CERT_FILE` |
| 启动代理        | ✅    | 即使引导失败也会启动                      |


**日志中应该看到：**

- `CA certificate: /path/to/data/ca.crt`
- `[Bootstrap]` 开头的引导结果信息
- 如果引导成功：`Transparent mode configured`
- 如果引导失败：降级指引和手动命令

## 🔧 排错

### 权限不足

如果日志显示 hosts 修改或 CA 安装失败：

1. 用管理员权限重启程序（`sudo` 或 Windows 管理员 PowerShell）
2. 或按日志指引手动执行命令
3. 或降级到隧道模式/网关模式

### Docker 限制

Docker 容器**不能**直接修改宿主机的 hosts 文件或 CA 信任库。日志会明确区分：

- `helper 缺失`：容器内没有宿主机 helper（`MCC_HOST_HELPER` 未设置）
- `宿主机权限不足`：helper 存在但无法获得宿主机权限

Docker 场景下推荐：

- 在宿主机手动配置 hosts 和 CA 信任
- Linux 宿主机运行 `sudo ./setup-host.sh trust`，然后在启动 Claude Code/Orca 的宿主 shell 中设置 `export SSL_CERT_FILE=/etc/ssl/certs/ca-certificates.crt`
- 完全退出并重新启动 Claude Code/Orca；容器无法替宿主机写入或刷新 `SSL_CERT_FILE`
- 或使用隧道模式（设置 `HTTPS_PROXY`）

这里的 `SSL_CERT_FILE` 必须指向已包含 MCC CA 的完整系统 bundle，不能指向单个 `data/ca.crt`。

### Docker 宿主机 helper 的实际用法

如果你希望 Docker 自动尝试修改宿主机，请准备一个**宿主机侧可执行 helper**，然后把它挂载到容器内，再通过 `MCC_HOST_HELPER` 指向容器内的绝对路径。

helper 的职责很简单：

- 收到 `hosts add api.anthropic.com 127.0.0.1` 时，负责把宿主机的 `api.anthropic.com` 指向 `127.0.0.1`
- 收到 `trust install /app/data/ca.crt` 时，负责把该 CA 安装到宿主机信任库
- 成功返回 `0`，失败返回非 `0`

helper 不需要由 MCC 提供，MCC 只负责调用它。你可以用 shell 脚本、Go 小工具或其它可执行文件实现它，只要容器里能直接执行即可。

最小挂载示例：

```yaml
services:
  mcc:
    volumes:
      - ./data:/app/data
      - /opt/mcc/mcc-host-helper.sh:/host-helper/mcc-host-helper.sh:ro
    environment:
      - MCC_HOST_HELPER=/host-helper/mcc-host-helper.sh
```

宿主机上的 helper 文件需要满足：

- 路径必须是绝对路径
- 文件必须是可执行的普通文件
- 在你的宿主机权限模型下，helper 自己必须有能力完成 hosts / CA 的修改

Linux / macOS 的一个简单骨架可以是：

```bash
#!/usr/bin/env bash
set -euo pipefail

case "${1:-}" in
  hosts)
    # 例如：hosts add api.anthropic.com 127.0.0.1
    ;;
  trust)
    # 例如：trust install /app/data/ca.crt
    ;;
  *)
    echo "usage: $0 {hosts|trust} ..." >&2
    exit 2
    ;;
esac
```

如果 helper 失败，MCC 不会假装宿主机已经被修改，而是继续启动并打印透明/隧道/网关的后备指引。

### 手动导入 CA

如果自动安装失败，手动导入：

```bash
# macOS
sudo security add-trusted-cert -d -r trustRoot \
  -k /Library/Keychains/System.keychain /path/to/data/ca.crt

# Linux (Debian/Ubuntu)
sudo cp /path/to/data/ca.crt /usr/local/share/ca-certificates/mcc-proxy-ca.crt
sudo update-ca-certificates

# Windows (管理员 PowerShell)
Import-Certificate -FilePath "C:\mcc\data\ca.crt" -CertStoreLocation Cert:\LocalMachine\Root
```

## 🔌 端口说明


| 端口   | 用途     |
| ---- | ------ |
| 443  | 代理服务入口 |
| 8442 | 配置页面   |


## 📂 项目结构

```text
magic-claude-code/
├── cmd/server/          # 应用入口
├── internal/
│   ├── config/          # 配置管理
│   ├── cert/            # 证书管理
│   ├── proxy/           # 代理服务
│   ├── admin/           # 配置服务
│   ├── bootstrap/       # 启动引导（hosts/CA/环境自动配置）
│   ├── i18n/            # 本地化消息
│   └── frontend/        # 前端页面
├── data/                # 数据目录
├── Dockerfile
└── docker-compose.yml
```

## 🛠️ 开发

```bash
# 运行测试
make test

# 本地运行
make run

# 构建
make build
```

## 📄 许可证

MIT License
