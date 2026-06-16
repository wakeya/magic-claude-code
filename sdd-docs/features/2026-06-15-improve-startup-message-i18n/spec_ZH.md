# 启动日志国际化与配置提示改进规格

本地页面：`cmd/server/main.go` 启动输出 / `internal/admin/` 管理面板提示
代理入口：无（管理服务 :8442）
参考源站：系统 locale 检测（`LANG`/`LC_ALL`）、Claude Code 官方文档  
技术栈：Go 1.26 标准库
最后更新：2026-06-15
进度：0 / 4 已规划

## 整体分析（源站分析）

### 问题

`mcc` 启动时打印的配置提示和日志消息全部为中文硬编码，存在以下四个问题：

1. **日志内容仅中文**：`cmd/server/main.go` 中所有 `fmt.Println`、`fmt.Printf`、`log.Printf` 输出均为中文。英文环境用户（尤其 Claude Code 原生用户）无法阅读启动提示，降低了易用性。
2. **后端地址示例单一**：启动提示仅显示 `后端地址: %s`，示例值默认来自旧配置（bigmodel）。缺少说明"可配置其他兼容 Anthropic 或 OpenAI Chat 的接口地址"，用户可能误认为仅支持 bigmodel。
3. **配置示例缺少 Windows 平台**：`hosts` 修改和 `NODE_EXTRA_CA_CERTS` 的示例命令均为 Linux/macOS 风格（`sudo tee -a /etc/hosts`、`echo '...' >> ~/.bashrc`），Windows 用户无对应示例。
4. **配置页面地址不完整**：仅提示 `https://localhost:%d`，但用户在 `hosts` 中已将 `api.anthropic.com` 映射到 `127.0.0.1`，因此 `https://api.anthropic.com:%d` 同样可达（且更符合 Claude Code 配置语境）。未提示此地址，用户可能不知道可以通过域名访问配置页面。

### 当前日志分布

所有待国际化文本集中在 `cmd/server/main.go`：

| 行号范围 | 内容 |
|----------|------|
| 109–128 | 启动横幅、端口信息、配置命令、密码提示（纯 `fmt.Println` 输出） |
| 31 | `警告: 随机数生成失败，使用后备方案` |
| 58 | `警告: 未设置密码，使用随机生成的密码` |
| 100 | `CA certificate: %s`（英文，但其余混合） |
| 149 | `运行在 Docker 容器中...` |
| 156 | `Proxy server error: %v`（英文） |
| 162 | `Admin server error: %v`（英文） |
| 177–202 | 关闭/重启/更新日志（混合中英文） |

### 现有 i18n 基础设施

- 前端已有完整的 i18n 体系（`internal/frontend/src/composables/useI18n.ts`），但后端无任何国际化机制。
- Go 标准库提供 `golang.org/x/text/message`（catalog/pipeline），但本项目使用纯标准库。
- 最简单的方案：运行时检测系统 locale（`LANG`/`LC_ALL` 环境变量），根据前缀（`zh`、`en` 等）选择消息集。默认回退英文。

### 策略

**单文件消息表 + 运行时 locale 检测**：

- 在 `internal/i18n/` 创建轻量级消息表（纯 Go map，不引入外部依赖）。
- 启动时读取 `LANG`/`LC_ALL` 环境变量：以 `zh` 开头输出中文，否则输出英文。
- 保留所有英文日志原文，中文作为可选覆盖。
- 配置提示按平台（Windows / Unix）分别输出对应命令。
- 配置页面地址同时提示 `localhost` 和 `api.anthropic.com` 两种形式。

### 范围

- **纳入范围**：`cmd/server/main.go` 启动日志国际化、`internal/admin/` 中面向用户的中文提示。
- **排除范围**：前端 i18n（已完备）、错误栈追踪（保持英文）、HTTP 日志中间件（技术日志保持英文）。

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | 已规划 | 创建 `internal/i18n` 消息表与 locale 检测 | `internal/i18n/i18n.go` | 单元测试：各 locale 前缀返回正确语言 |
| 2 | 已规划 | 国际化 `cmd/server/main.go` 启动横幅与配置提示 | `cmd/server/main.go` | 中英文启动日志对比验证 |
| 3 | 已规划 | 补充 Windows 配置示例与双域名提示 | `cmd/server/main.go` | 手动检查 Windows/Unix 输出差异 |
| 4 | 已规划 | 国际化 `internal/admin` 中面向用户的消息 | `internal/admin/` 中相关 handler | 手动检查管理面板提示 |

## 需求

### 需求 1：启动日志支持英文

`mcc` 启动时输出的所有面向用户的提示消息（横幅、端口、配置命令、密码）必须根据系统 locale 自动选择中文或英文。技术错误日志（如 `Proxy server error`）保持英文不变。

**Locale 检测规则**：

1. 优先读取 `MCC_LANG` 环境变量（值为 `zh` 或 `en`）。
2. 若未设置，读取 `LANG` 环境变量：以 `zh` 开头则中文，否则英文。
3. 若未设置，读取 `LC_ALL` 环境变量，规则同上。
4. 默认回退英文。

**消息表结构**（`internal/i18n/i18n.go`）：

```go
type Messages struct {
    StartupBanner       string
    ProxyPort           string
    AdminPort           string
    BackendURL          string
    ConfigInstructions  string
    HostsCommand        string
    CACertCommand       string
    SourceCommand       string
    AdminPage           string
    AdminPageAlt        string
    RandomPassword      string
    PasswordSaveHint    string
    PasswordEnvHint     string
    DockerUpdateHint    string
    // ... 其他消息
}

func Load(locale string) Messages { ... }
```

### 需求 2：后端地址提示补充说明

启动提示中的后端地址行，在显示实际 URL 的同时，追加说明可配置其他兼容端点：

- **中文**：`后端地址: %s（可配置其他兼容 Anthropic 或 OpenAI Chat 的接口地址）`
- **英文**：`Backend URL: %s (configurable to other Anthropic or OpenAI Chat compatible endpoints)`

### 需求 3：按平台输出配置示例

`hosts` 修改和 CA 证书环境变量配置的示例命令，根据运行平台（Windows / Unix）分别输出：

**Unix（Linux/macOS）**：

```
1. echo '127.0.0.1 api.anthropic.com' | sudo tee -a /etc/hosts
2. echo 'NODE_EXTRA_CA_CERTS=%s' >> ~/.bashrc
3. source ~/.bashrc
```

**Windows**：

```
1. 以管理员 PowerShell 运行：
   Add-Content -Path "$env:WINDIR\System32\drivers\etc\hosts" -Value "`n127.0.0.1 api.anthropic.com"
2. 设置 Node.js CA 证书环境变量：
   [Environment]::SetEnvironmentVariable("NODE_EXTRA_CA_CERTS", "%s", "User")
3. 关闭并重新打开终端
```

平台检测使用 `runtime.GOOS == "windows"`。

### 需求 4：配置页面地址双域名提示

配置页面地址同时显示两种形式：

- **中文**：
  ```
  配置页面:
    https://localhost:%d
    https://api.anthropic.com:%d
  ```
- **英文**：
  ```
  Admin panel:
    https://localhost:%d
    https://api.anthropic.com:%d
  ```

## 任务详情

### 任务 1：创建 i18n 消息表

#### 需求

**Objective（目标）** — 创建 `internal/i18n` 包，提供基于 locale 的消息选择和平台感知输出。

**Outcomes（成果）** — 后端具备轻量级国际化能力，不引入外部依赖。

**Evidence（证据）** — 单元测试覆盖 `zh_CN`、`zh`、`en_US`、`en`、`ja`、`''` 等 locale 输入，断言返回正确语言集。

**Constraints（约束）** — 不使用 `golang.org/x/text` 等外部包；保持纯标准库。消息表用 Go struct + map，编译期类型安全。

**Edge Cases（边界）** — `MCC_LANG` 设置为未知值（如 `fr`）时回退英文；`LANG` 值为 `C` 或 `POSIX` 时回退英文；Windows 上 `LANG` 可能不存在。

**Verification（验证）** — `go test ./internal/i18n/... -v`

#### 计划

1. 创建 `internal/i18n/i18n.go`：
   - 定义 `Messages` struct，包含所有需要国际化的字段。
   - 定义 `zhMessages` 和 `enMessages` 两个常量实例。
   - 实现 `ResolveLocale()` 函数，按 `MCC_LANG` → `LANG` → `LC_ALL` → 默认英文的顺序检测。
   - 实现 `Load(locale string) Messages` 函数。
2. 创建 `internal/i18n/i18n_test.go`：
   - 测试各 locale 前缀解析。
   - 测试未知 locale 回退英文。
   - 测试 `MCC_LANG` 优先于 `LANG`。

#### 验证

运行 `go test ./internal/i18n/... -v`，所有用例通过。

### 任务 2：国际化启动日志

#### 需求

**Objective（目标）** — 将 `cmd/server/main.go` 中所有面向用户的 `fmt.Println`/`fmt.Printf` 输出替换为 i18n 消息。

**Outcomes（成果）** — 启动横幅、端口信息、配置命令、密码提示等全部支持中英文切换。

**Evidence（证据）** — 手动验证：设置 `LANG=zh_CN.UTF-8` 启动输出中文；设置 `LANG=en_US.UTF-8` 启动输出英文。

**Constraints（约束）** — 技术错误日志（`log.Fatalf`、`log.Printf` 中的错误信息）保持英文。Docker 检测提示和自动更新提示也需国际化。

**Edge Cases（边界）** — 随机密码生成失败的后备提示；Docker 容器内启动提示。

**Verification（验证）** — 构建并分别以 `LANG=zh_CN.UTF-8` 和 `LANG=en_US.UTF-8` 运行，截图对比输出。

#### 计划

1. 在 `cmd/server/main.go` 中引入 `magic-claude-code/internal/i18n`。
2. 将启动横幅区域（行 109–128）替换为 `msg := i18n.Load(i18n.ResolveLocale())`。
3. 所有 `fmt.Println`/`fmt.Printf` 调用改用消息表字段。
4. 将 Docker 检测提示（行 149）替换为国际化消息。
5. 将关闭/重启日志（行 177–202）中面向用户的部分国际化。

#### 验证

```bash
# 中文
LANG=zh_CN.UTF-8 ./bin/mcc -data ./data

# 英文
LANG=en_US.UTF-8 ./bin/mcc -data ./data
```

对比输出确认语言正确。

### 任务 3：补充平台示例与双域名提示

#### 需求

**Objective（目标）** — 在启动配置提示中增加 Windows 平台示例，并同时显示 `localhost` 和 `api.anthropic.com` 两种配置页面地址。

**Outcomes（成果）** — Windows 用户无需自行翻译命令；所有用户知晓可通过域名访问配置页面。

**Evidence（证据）** — 构建后在 Windows（或模拟 `runtime.GOOS = "windows"`）运行，确认输出 Windows 风格命令；确认同时显示两个 URL。

**Constraints（约束）** — 不引入平台检测外部包，使用 `runtime.GOOS`。Windows PowerShell 命令需管理员权限，在提示中注明。

**Edge Cases（边界）** — WSL 环境下 `runtime.GOOS` 返回 `linux`，按 Unix 示例输出（正确行为）。

**Verification（验证）** — 构建后在目标平台手动运行，确认输出格式。

#### 计划

1. 在 `Messages` struct 中增加平台相关字段：
   - `HostsCommandUnix`
   - `HostsCommandWindows`
   - `CACertCommandUnix`
   - `CACertCommandWindows`
   - `SourceCommandUnix`
   - `SourceCommandWindows`
2. 在启动输出逻辑中根据 `runtime.GOOS` 选择对应命令示例。
3. 在配置页面提示中同时打印两个 URL。

#### 验证

```bash
# Linux/macOS
./bin/mcc -data ./data
# 确认输出 sudo tee 命令

# Windows（或交叉编译后）
./bin/mcc.exe -data .\data
# 确认输出 PowerShell 命令
```

### 任务 4：国际化管理面板提示

#### 需求

**Objective（目标）** — 检查并国际化 `internal/admin/` 中面向终端用户的中文提示。

**Outcomes（成果）** — 管理面板相关的中文日志（如更新禁用提示）支持英文。

**Evidence（证据）** — `grep -rn 'fmt.Println\|log.Printf' internal/admin/` 结果中无纯中文硬编码。

**Constraints（约束）** — HTTP 错误 JSON 响应（如 `{"error": "provider not found"}`）保持英文，这些是 API 契约。

**Edge Cases（边界）** — Docker 更新禁用提示已在前一步覆盖。

**Verification（验证）** — `grep` 检查确认无遗漏中文日志。

#### 计划

1. 搜索 `internal/admin/` 中所有中文硬编码日志。
2. 将面向用户的提示替换为 i18n 消息。
3. API 错误 JSON 保持英文不变。

#### 验证

```bash
grep -rn 'fmt\.Print\|log\.Print' internal/admin/ | grep -v '_test.go'
# 确认无纯中文输出
```
