# 自动更新功能规格

本地页面：管理面板头部 / 状态接口  
代理入口：无（管理服务 :8442）  
参考源站：GitHub Releases API、GitCode（gitcode.com）Releases API  
技术栈：Go 1.26 标准库（`net/http`、`archive/tar`、`compress/gzip`、`crypto/sha256`）+ Vue 3 + 内嵌前端  
最后更新：2026-06-13  
进度：0 / 7 已规划

## 整体分析（源站分析）

### 当前项目状态

本项目是一个 Go 单二进制透明代理（`mcc`），支持裸机部署和 Docker 部署。发布物由 GitHub Actions（`.github/workflows/release.yml`）、GitLab CI（`.gitlab-ci.yml`）和 GitCode CI（`.gitcode/workflows/release.yml`）构建，产出各平台压缩包：

```
Magic-Claude-Code-{tag}-{Platform}-{Arch}.tar.gz   (Linux, macOS)
Magic-Claude-Code-{tag}-{Platform}-{Arch}.zip      (Windows)
SHA256SUMS.txt                                     （所有压缩包的校验和）
```

二进制文件名为 `mcc`。压缩包内包含一个目录，其中有 `mcc` 和 `README.md`。

当前二进制没有内置版本信息——`go build` 使用 `-ldflags="-s -w"`，未注入版本号。用户无法得知当前运行版本，更新需要手动下载并替换二进制文件。

### GitHub Releases API

GitHub 提供 REST API 查询发布元数据：

```
GET https://api.github.com/repos/{owner}/{repo}/releases/latest
```

响应包含 `tag_name`、`html_url` 以及 `assets` 数组（每个资产有 `name` 和 `browser_download_url`）。API 文档完善、稳定、返回 JSON。未认证请求限速 60 次/小时，足以支撑前端按浏览器每 24 小时一次的检查 + 应用更新时的按需检查。

### GitCode Releases API 与二进制分发

GitCode（CSDN 运营）是国内开源代码平台。经 API 实际验证：

- **API 基础路径**：`https://api.gitcode.com/api/v5`（也兼容 `https://gitcode.com/api/v5`）
- **认证头**：`PRIVATE-TOKEN`（不是 `Authorization: token`）
- **Releases API**：`GET /repos/{owner}/{repo}/releases/latest` — 返回 `tag_name`、`assets`（仅自动生成的源码包）
- **自定义资产上传**：**不支持** — GitCode releases 只自动生成源码包（zip、tar.gz），无法上传编译好的二进制

由于 GitCode release 不支持上传自定义二进制资产，预编译二进制存储在仓库的 `dist/release/{tag}/` 目录下。下载 URL 通过 GitCode raw 文件 API 构造：

```
https://api.gitcode.com/api/v5/repos/{owner}/{repo}/raw/dist/release/{tag}/{asset_name}
```

例如：
```
https://api.gitcode.com/api/v5/repos/wakeya/magic-claude-code/raw/dist/release/v0.2.0/Magic-Claude-Code-v0.2.0-Linux-x86_64.tar.gz
```

SHA256SUMS.txt 与二进制包同目录：
```
https://api.gitcode.com/api/v5/repos/wakeya/magic-claude-code/raw/dist/release/v0.2.0/SHA256SUMS.txt
```

本地 `dist/release/` 目录只保留最新版本的二进制文件；旧版本仅保留空目录和 `.gitkeep` 占位。

### 连通性检测策略

不做单独的连通性探测，而是按顺序尝试源站：先 GitHub，失败后回退 GitCode。每个源站请求继承 HTTP 客户端的 30 秒超时。如果第一个源站超时或返回网络错误，自动尝试下一个。前端页面加载检查通过浏览器本地状态限制为每 24 小时一次。

### 二进制自更新约束

| 平台 | 能否覆盖运行中的二进制？ | 说明 |
| --- | --- | --- |
| Linux | 可以 | 临时文件 + 备份 + 重命名替换 |
| macOS | 可以 | 同 Linux |
| Windows | 可以 | 临时文件 + 备份 + 重命名替换；不自动重启 |
| Docker | 仅检查 | 容器文件系统是临时的；显示新版本通知，但引导用户通过镜像更新 |

### 风险总结

1. 版本注入需要修改 CI（`-ldflags -X`）；如果 CI 遗漏，二进制会报告 `dev` 版本，导致永远显示"需要更新"。
2. SHA256 校验是强制的——缺失或不匹配的校验和必须阻止更新。
3. 二进制替换必须原子化，并自动回滚，避免损坏安装。
4. Docker 检测（`/.dockerenv`）必须保留更新检查能力，但禁用应用内自更新，并引导用户通过容器镜像更新。
5. GitCode API 格式已验证：releases API 可用于 tag 检测，但自定义二进制资产通过仓库 raw URL（`dist/release/{tag}/`）分发，而非 release 附件。
6. 更新后的重启策略：Linux/macOS 通过 `syscall.Exec` 自动重启；Windows 需要手动重启（磁盘上的二进制已替换，但运行中的进程仍使用旧二进制，直到手动重启）。

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | 已规划 | 版本包与 CI ldflags 注入 | `internal/version/version.go`、CI 更新、状态接口 | 使用测试版本构建；验证状态接口返回版本 |
| 2 | 已规划 | 更新源层（GitHub + GitCode） | `internal/updater/source.go` | 使用 mock HTTP 服务器的源站单元测试 |
| 3 | 已规划 | 核心逻辑（检查、下载、校验、应用） | `internal/updater/updater.go` | 核心单元测试（SHA256、解包、资产映射） |
| 4 | 已规划 | 管理端更新 API | `internal/admin/update_handler.go`、服务路由 | Handler 测试（无 updater 时 503、检查、应用） |
| 5 | 已规划 | 前端版本标签 + 更新通知 UI | `AppHeader.vue`、`useApi.ts`、`useI18n.ts` | 前端构建；手动检查标签 + 对话框 |
| 6 | 已规划 | Updater 接线与 Docker 检测 | `cmd/server/main.go` 接线 | 构建；验证 Docker 引导日志 |
| 7 | 已规划 | 端到端手动验证 | 验证记录 | Mock 发布服务器全链路测试 |

## 需求

### 交付物

1. 二进制通过 `internal/version.Version` 报告版本，构建时通过 `-ldflags "-X magic-claude-code/internal/version.Version={tag}"` 注入。
2. `/api/status` 接口响应包含 `version` 字段。
3. 新增 `internal/updater` 包提供：
   - `ReleaseSource` 接口，含 `GitHubSource` 和 `GitCodeSource` 实现。
   - `GitHubSource` 查询 GitHub Releases API，从 release 附件返回资产下载 URL。
   - `GitCodeSource` 查询 GitCode Releases API 进行 tag 检测，但二进制下载 URL 从仓库 raw 文件路径构造（`dist/release/{tag}/`）。
   - `CheckForUpdate` 按顺序查询源站，返回 `UpdateInfo`（当前版本 vs. 最新版本、下载 URL、发布页 URL，以及源站特定的下载请求头）。
   - `DownloadAndApply` 下载对应平台的压缩包，通过 `SHA256SUMS.txt` 校验 SHA256，解压 `mcc` 二进制，通过原子备份 + 回滚替换运行中的二进制。
4. 管理 API 端点：
   - `GET /api/update/check` — 返回当前版本、最新版本、是否有更新、使用的源站。
   - `POST /api/update/apply` — 下载并应用更新；返回成功、错误，以及显式的 `restarting` 布尔值表示服务是否正在自动重启。
5. 前端在标题旁始终显示当前版本标签。有新版本时，标签变为高亮可点击元素，展示"vX.Y.Z → vA.B.C"及箭头图标；点击打开对话框，展示版本详情和"立即更新"按钮。
6. 启动时服务端只为管理 API 接线 updater，不自行发起自动更新检查。
7. Docker 环境（存在 `/.dockerenv`）保留 `/api/update/check`，禁用 `/api/update/apply`，并输出引导消息。
8. 单元测试覆盖源站解析、SHA256 校验、二进制解包、资产名映射和版本比较逻辑。
9. 详细执行计划维护在 `sdd-docs/superpowers/plans/2026-06-13-auto-update.md`。

### 目录结构

```text
internal/
  version/
    version.go
  updater/
    source.go
    source_test.go
    updater.go
    updater_test.go
  admin/
    update_handler.go
    update_handler_test.go
    server.go           （修改：添加 updater 字段、路由、setter）
    handler.go          （修改：状态响应添加 version 字段）
cmd/
  server/
    main.go             （修改：接线 updater、Docker 检测）
dist/
  release/              （GitCode 二进制分发）
    v0.1.0/.gitkeep     （空占位）
    v0.2.0/             （最新：实际二进制 + SHA256SUMS.txt）
internal/frontend/src/
  composables/
    useApi.ts           （修改：添加更新 API 方法）
    useI18n.ts          （修改：添加更新相关 i18n 字符串）
  components/
    AppHeader.vue        （修改：添加版本标签 + 更新对话框）
```

### 数据模型

无持久化数据模型变更。Updater 仅操作内存状态：

```go
// internal/version/version.go
package version

var Version = "dev" // 由 ldflags 覆盖

// internal/updater/updater.go
type UpdateInfo struct {
    CurrentVersion string
    LatestVersion  string
    SourceName     string
    ReleaseURL     string
    AssetName      string
    DownloadURL    string
    DownloadHeaders map[string]string
}

type ApplyResult struct {
    NewVersion string
    Message    string
    Restarting bool
}
```

### 平台资产命名

| `runtime.GOOS` | `runtime.GOARCH` | 资产后缀 |
| --- | --- | --- |
| `linux` | `amd64` | `Linux-x86_64.tar.gz` |
| `linux` | `arm64` | `Linux-arm64.tar.gz` |
| `darwin` | `amd64` | `macOS-x86_64.tar.gz` |
| `darwin` | `arm64` | `macOS-arm64.tar.gz` |
| `windows` | `amd64` | `Windows-x86_64.zip` |
| `windows` | `arm64` | `Windows-arm64.zip` |

完整资产名格式：`Magic-Claude-Code-{tag}-{Platform}-{Arch}.{ext}`

### 约束

1. SHA256 校验是强制的——如果 `SHA256SUMS.txt` 缺失或校验和不匹配，更新必须失败并返回明确错误。
2. 二进制替换必须使用备份 + 重命名策略；写入失败时自动恢复备份。
3. Docker 环境（存在 `/.dockerenv`）必须实例化 updater 用于更新检查，但 `POST /api/update/apply` 必须返回明确业务错误，提示用户通过更新容器镜像升级。
4. 支持 Windows 自更新：通过临时文件 + 备份 + 重命名替换二进制；Windows 不支持自动重启，用户必须手动重启。
5. Updater 不得以后台轮询循环运行，也不得在服务端启动时自动检查。检查由认证后的管理 API 触发，前端页面加载检查按浏览器每 24 小时一次节流。
6. 用于发布检查的 HTTP 客户端必须有超时（30 秒），避免阻塞管理服务。
7. 版本比较使用语义版本解析；`dev` 版本始终视为比任何有效发布 tag 旧。
8. 服务端启动不得发起外部更新检查请求。
9. 管理 API `apply` 端点必须使用更长的超时（5 分钟），以适应慢速网络下的大文件下载。
10. 压缩包下载限制为 200 MB；`SHA256SUMS.txt` 下载限制为 1 MB；超出限制时必须以明确的大小限制错误失败。
11. 前端更新通知必须是尽力而为——检查失败不得显示错误徽章或阻塞正常使用。

### 边界情况

1. 二进制构建时未注入 ldflags（版本为 `dev`）——始终报告有可用更新。
2. GitHub API 限速——自动回退到 GitCode。
3. GitCode 未发布 Release 产物——源站返回错误；如果两个源站都失败，检查端点返回错误消息。
4. 运行中的二进制路径包含符号链接——`filepath.EvalSymlinks` 在替换前解析真实路径。
5. 压缩包中不包含预期的 `mcc` 二进制——`extractBinary` 返回明确错误。
6. SHA256SUMS.txt 格式有多余空格或空行——`parseSHA256Sums` 使用 `strings.Fields` 健壮解析。
7. 更新被触发但版本已经是最新的——返回明确的"已是最新版本"消息。
8. 网络完全不可达——两个源站都失败；检查端点返回明确错误；前端记录本次检查尝试，24 小时内页面加载不再重试。
9. 管理服务未配置 updater——更新端点返回 HTTP 503。Docker 环境仍配置 updater 用于检查，仅禁用 apply。
10. 下载中断或损坏——`io.ReadAll` 返回部分数据；SHA256 校验失败；更新被拒绝。

### 非目标

1. ~~不在二进制替换后实现自动进程重启~~ —— **2026-06-13 更新**：按用户要求，Linux/macOS 已通过 `syscall.Exec` 实现自动重启；Windows 仍需手动重启。
2. 不实现后台轮询循环检查更新。
3. ~~不在本期实现 Windows 自更新~~ —— **2026-06-13 更新**：按用户要求，Windows 已支持二进制替换自更新，但不自动重启。
4. 不实现回滚到历史版本（仅在写入步骤失败时自动回滚）。
5. 不在前端渲染发布说明。
6. ~~不在 CI 中实现 GitCode 发布自动化~~ — **2026-06-13 更新**：已通过 GitCode CI 工作流（`.gitcode/workflows/release.yml`）自动构建并提交产物到 `dist/release/{tag}/`。

## 任务详情

### 任务 1：版本包与 CI 注入

#### 需求

**Objective（目标）** — 在构建时将发布版本注入二进制，使应用能报告当前运行版本。

**Outcomes（成果）** — `internal/version/version.go` 暴露 `Version` 变量；GitHub Actions、GitLab CI 和 Dockerfile 通过 `-ldflags "-X"` 注入版本；`/api/status` 接口包含 `version` 字段。

**Evidence（证据）** — 本地使用 `-ldflags "-X magic-claude-code/internal/version.Version=v0.0.1-test"` 构建成功；状态接口返回 `"version": "v0.0.1-test"`。

**Constraints（约束）** — 本地构建默认值为 `"dev"`；除了添加 `-X` 标志外不改变现有 CI 构建流程。

**Edge Cases（边界）** — 不使用 ldflags 构建（版本保持 `"dev"`）；CI tag 不符合 semver 格式（流水线已有验证）。

**Verification（验证）** — 使用测试版本本地构建并验证状态接口。

#### 计划

1. 创建 `internal/version/version.go`，定义 `var Version = "dev"`。
2. 更新 `.github/workflows/release.yml` 构建命令，添加 `-X magic-claude-code/internal/version.Version=${RELEASE_TAG}`。
3. 更新 `.gitlab-ci.yml` 构建命令，添加 `-X magic-claude-code/internal/version.Version=${CI_COMMIT_TAG}`。
4. 更新 `Dockerfile`，添加 `ARG APP_VERSION=dev` 并传入构建命令。
5. 修改 `internal/admin/handler.go` 的 `handleStatus`，在 JSON 响应中添加 `"version": version.Version`。

#### 验证

- [ ] 本地使用测试版本构建后正确报告版本。
- [ ] 状态接口包含 `version` 字段。
- [ ] CI ldflags 语法与现有 `-ldflags="-s -w"` 模式匹配。

### 任务 2：更新源层

#### 需求

**Objective（目标）** — 创建源站抽象，能从 GitHub 和 GitCode 获取最新发布元数据。

**Outcomes（成果）** — `ReleaseSource` 接口及 `GitHubSource` 和 `GitCodeSource` 实现；两者返回 `ReleaseInfo`，包含 tag name、HTML URL 和可下载资产列表。

**Evidence（证据）** — 单元测试使用 `httptest.Server` 模拟 API 响应；GitHub 源正确解析标准 releases JSON；GitCode 源正确解析 GitCode API 响应。

**Constraints（约束）** — 源站必须按顺序尝试（先 GitHub）；每个源站通过共享 HTTP 客户端拥有独立超时；接口必须可扩展以支持未来源站，无需修改 updater 核心。

**Edge Cases（边界）** — API 返回非 200 状态；响应 JSON 缺少预期字段；资产列表为空；网络超时。

**Verification（验证）** — 使用 mock HTTP 服务器的两个源站单元测试。

#### 计划

1. 在 `internal/updater/source.go` 定义 `ReleaseAsset`、`ReleaseInfo` 和 `ReleaseSource` 接口。
2. 实现 `GitHubSource`，`baseURL` 可配置（默认 `https://api.github.com`）。
3. 实现 `GitCodeSource`，`baseURL` 可配置（默认 `https://gitcode.com`）。
4. 为 `ReleaseInfo` 添加 `findAsset` 辅助方法。
5. 编写使用 `httptest.Server` 返回 mock release JSON 的测试。

#### 验证

- [ ] `GitHubSource.FetchLatestRelease` 正确解析 tag、URL 和资产。
- [ ] `GitCodeSource.FetchLatestRelease` 正确解析 tag、URL 和资产。
- [ ] 非 200 状态返回错误。
- [ ] `findAsset` 返回正确的资产或 nil。

### 任务 3：核心更新逻辑

#### 需求

**Objective（目标）** — 实现完整的更新流程：版本比较、资产选择、下载、SHA256 校验、解包和原子二进制替换。

**Outcomes（成果）** — `Updater` 结构体含 `CheckForUpdate` 和 `DownloadAndApply` 方法；辅助函数处理资产名映射、SHA256 解析/校验、tar.gz 解包和二进制替换。

**Evidence（证据）** — 单元测试覆盖 `assetNameFor`（全部 6 种平台/架构组合 + 无效输入）、`parseSHA256Sums`（标准格式 + 边界情况）、`verifyChecksum`（匹配 + 不匹配）、`extractBinary`（含嵌套二进制的 tar.gz）、`isNewer`（版本比较逻辑）。

**Constraints（约束）** — 二进制替换支持 Linux/macOS/Windows；Linux/macOS 通过 `syscall.Exec` 自动重启；Windows 替换二进制但需要手动重启；替换必须先完整写入临时文件，再进行路径替换，安装失败时自动回滚；压缩包下载超过 200 MB 或校验和下载超过 1 MB 时必须拒绝。

**Edge Cases（边界）** — 不支持的 OS/arch；压缩包中缺少二进制；SHA256 不匹配；可执行文件路径含符号链接；备份后写入失败（必须回滚）。

**Verification（验证）** — 所有单元测试通过；`go test ./internal/updater/ -v` 全绿。

#### 计划

1. 实现 `assetNameFor(goos, goarch, tag)`，将 `runtime` 值映射到资产命名约定。
2. 实现 `parseSHA256Sums(r io.Reader)`，解析 `sha256sum` 输出格式。
3. 实现 `verifyChecksum(data []byte, expectedHex string)`，使用 `crypto/sha256`。
4. 实现 `extractBinary(r io.Reader, binaryName string)`，使用 `archive/tar` + `compress/gzip`。
5. 实现 `isNewer(current, latest string)` 版本比较。
6. 实现 `Updater` 结构体，`CheckForUpdate`（按顺序尝试源站）和 `DownloadAndApply`（下载 → 校验 → 解包 → 替换）。
7. 实现 `replaceBinary(newBinary []byte)`，备份 + 重命名 + 回滚。

#### 验证

- [ ] `assetNameFor` 对所有支持平台返回正确名称。
- [ ] `parseSHA256Sums` 处理包含多个条目的标准格式。
- [ ] `verifyChecksum` 接受正确哈希、拒绝错误哈希。
- [ ] `extractBinary` 从 tar.gz 中提取正确的二进制。
- [ ] `isNewer` 正确比较 `dev`、相等版本和标准 semver。
- [ ] `replaceBinary` 创建备份、写入新二进制、成功后删除备份。

### 任务 4：管理端更新 API

#### 需求

**Objective（目标）** — 通过认证的管理 API 端点暴露更新检查和应用功能。

**Outcomes（成果）** — `GET /api/update/check` 返回版本比较信息；`POST /api/update/apply` 触发下载和应用；两者均在 `authMiddlewareFunc` 之后；未配置 updater 时返回 HTTP 503；Docker 环境下 check 保持可用，apply 返回明确业务错误，提示用户通过镜像更新。

**Evidence（证据）** — Handler 测试验证 updater 为 nil 时返回 503；检查端点返回正确 JSON 结构；应用端点拒绝非 POST 方法。

**Constraints（约束）** — 保持现有 `NewServer` 签名不变；使用 `SetUpdater` setter 保持向后兼容；检查超时 15 秒；应用超时 5 分钟。

**Edge Cases（边界）** — Updater 未配置；Docker 环境禁用 apply 但保留 check；所有源站失败；已是最新版本；下载或校验失败；用 GET 方法调用 apply。

**Verification（验证）** — 管理 handler 单元测试通过；手动 curl 测试显示正确响应。

#### 计划

1. 为 `Server` 添加 `updater *updater.Updater` 字段和 `SetUpdater` 方法。
2. 在 `Start()` 中注册 `/api/update/check` 和 `/api/update/apply` 路由。
3. 实现 `handleUpdateCheck`，使用 15 秒 context 超时。
4. 实现 `handleUpdateApply`，使用 5 分钟 context 超时。
5. 编写 handler 测试。

#### 验证

- [ ] 无 updater 时 `GET /api/update/check` 返回 503。
- [ ] 有 updater 时 `GET /api/update/check` 返回版本信息。
- [ ] 无 updater 时 `POST /api/update/apply` 返回 503。
- [ ] 用 GET 方法调用 `POST /api/update/apply` 返回 405。

### 任务 5：前端版本标签与更新通知 UI

#### 需求

**Objective（目标）** — 在头部标题旁始终显示当前版本标签。有新版本时，标签变为高亮可点击元素，打开更新对话框。

**Outcomes（成果）** — `AppHeader.vue` 在"Magic Claude Code"标题旁始终显示版本标签。无更新时为静态灰色文字（如 `v0.1.0`）。有更新时变为带微妙脉冲动画的高亮可点击元素，展示版本过渡（如 `v0.1.0 → v0.2.0` 带向上箭头图标）；点击打开对话框，展示当前版本 vs. 最新版本详情及"立即更新"按钮。zh 和 en 的 i18n 字符串已添加。

**Evidence（证据）** — 前端构建无错误；版本标签始终在标题旁可见；有更新时标签外观变化；点击高亮标签打开对话框；对话框显示正确版本信息；点击"立即更新"调用应用 API。

**Constraints（约束）** — 检查失败必须静默（标签保持静态版本文字，无错误指示）；页面加载触发的更新检查必须通过 local storage 按浏览器限制为每 24 小时一次，且必须在网络请求前记录本次尝试，避免失败后 24 小时内反复重试；标签在两种状态间切换时不得改变 header 布局；对话框必须显示服务中断确认消息；更新按钮在更新过程中必须显示加载状态。

**Edge Cases（边界）** — 版本为 `dev`（本地构建未注入 ldflags）— 在允许执行的更新检查返回新版本前标签显示 `dev`；更新检查静默失败 — 标签保持静态版本文字，且页面加载检查 24 小时内不再重试；应用失败并显示错误消息；应用成功且服务重启（UI 显示"正在重启"消息）；用户关闭对话框。

**Verification（验证）** — 前端构建通过；手动验证两种状态下标签 + 对话框交互。

#### 计划

1. 在 `useApi.ts` 添加 `UpdateCheckResult` 和 `UpdateApplyResult` 类型。
2. 添加 `checkForUpdate()` 和 `applyUpdate()` 方法到 API composable。
3. 在 zh 和 en 中添加更新 UI 的 i18n 字符串。
4. 在 `AppHeader.vue` 标题旁添加版本标签：
   - 静态状态：灰色 `text-[11px]` 显示当前版本。
   - 更新状态：高亮背景 + 主题色文字显示 `vX.Y.Z → vA.B.C` 带向上箭头图标，可点击打开对话框。
5. 添加更新确认对话框（Teleport to body）。
6. 组件挂载时仅在上次页面加载检查超过 24 小时时调用 `checkUpdate()`（尽力而为，失败静默）。

#### 验证

- [ ] 版本标签始终在标题旁可见。
- [ ] 无更新时标签为静态灰色文字。
- [ ] 有更新时标签变为高亮可点击元素，展示 `vX.Y.Z → vA.B.C`。
- [ ] 点击高亮标签打开更新对话框。
- [ ] 对话框显示当前版本和最新版本。
- [ ] 更新按钮触发更新 API。
- [ ] 检查失败不改变标签外观（保持静态灰色）。
- [ ] 上次检查尝试在 24 小时内时，页面加载不再请求更新检查接口。

### 任务 6：Updater 接线与 Docker 检测

#### 需求

**Objective（目标）** — 将 updater 接入 `main.go`，实现 Docker 环境检测，并取消服务端启动自动更新检查。

**Outcomes（成果）** — 裸机和 Docker 环境启动时都实例化 updater 并通过 `SetUpdater` 注入管理服务；Docker 下禁用 apply 并输出镜像更新引导消息，同时保留检查能力。

**Evidence（证据）** — 应用成功启动；Docker 日志输出镜像更新引导消息；Docker 和非 Docker 环境下管理更新检查端点正常工作；Docker 环境下应用更新端点被阻止。

**Constraints（约束）** — 服务端启动不得发起外部更新检查；更新检查必须通过认证后的管理 API 请求执行；Docker 环境不得执行应用内二进制替换。

**Edge Cases（边界）** — 运行在 Docker 中（通过 `/.dockerenv` 检测）；两个源站都不可达；已是最新版本；有新版本可用。

**Verification（验证）** — 本地构建并运行；验证日志消息；验证 Docker 检测。

#### 计划

1. 在 `main.go` 添加 `updater` 和 `version` 导入。
2. 检查 `/.dockerenv` 检测 Docker 环境。
3. 使用 `GitHubSource` 和 `GitCodeSource` 实例化 `Updater`。
4. 调用 `adminServer.SetUpdater(updaterInstance)`。
5. 不启动用于更新检查的启动 goroutine。

#### 验证

- [ ] 非 Docker 启动接线 updater，但不发起外部更新检查。
- [ ] Docker 启动记录引导消息且仍创建 updater 用于检查。
- [ ] Docker 环境下源站可达时，管理更新检查端点返回 200。
- [ ] Docker 环境下管理更新应用端点返回 200 业务错误，并包含 `restarting:false`。
- [ ] 启动不被任何更新检查延迟。

### 任务 7：端到端手动验证

#### 需求

**Objective（目标）** — 验证完整更新流程在 mock 或真实发布下正常工作。

**Outcomes（成果）** — 一份验证记录，记录测试环境、步骤和结果。

**Evidence（证据）** — 日志输出显示成功的 检查 → 下载 → 校验 → 应用；重启后状态接口显示新版本。

**Constraints（约束）** — 不泄露 API 密钥或敏感信息；真实发布不可用时使用 mock 发布服务器；记录 GitCode API 验证状态。

**Edge Cases（边界）** — 应用过程中网络不可达；损坏下载的 SHA256 不匹配；GitCode API 格式与预期不同。

**Verification（验证）** — 至少完成一次全链路测试（检查 → 应用 → 重启 → 验证新版本）。

#### 计划

1. 搭建 mock 发布服务器，包含测试压缩包和 SHA256SUMS.txt。
2. 配置 updater 使用 mock 服务器作为源站。
3. 触发 `GET /api/update/check` 并验证响应。
4. 触发 `POST /api/update/apply` 并验证成功。
5. 重启服务并验证状态接口显示新版本。
6. 记录 GitCode API 端点格式以备后续参考。

#### 验证

- [ ] 检查端点返回正确的版本比较。
- [ ] 应用端点下载、校验并替换二进制。
- [ ] SHA256 校验拒绝篡改的压缩包。
- [ ] 重启后新版本已激活。
- [ ] Docker 检测保留更新检查能力，并仅禁用应用内自更新。
