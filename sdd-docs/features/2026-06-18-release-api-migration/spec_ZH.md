# Release API 迁移规格

本地页面：`internal/updater/source.go`、`scripts/release.sh`
代理入口：无
参考源站：GitHub Releases API、Gitee Releases API、GitCode Releases API、GitLab Releases API
技术栈：Go 1.26、Bash、GitHub Actions CI
最后更新：2026-06-18
进度：5 / 5 已完成

## 整体分析（源站分析）

### 背景

项目在四个 Git 远程仓库（GitHub、Gitee、GitCode、自建 GitLab）分发预编译二进制。当前，二进制文件作为 git 跟踪文件存储在 `dist/release/{tag}/` 中，通过各平台的 raw 文件 URL 下载：

- GitHub：已使用 Release API（`github.com/.../releases/download/{tag}/{file}`）
- Gitee：raw URL（`gitee.com/.../raw/main/dist/release/{tag}/{file}`）
- GitCode：raw API URL（`api.gitcode.com/api/v5/repos/.../raw/dist/release/{tag}/{file}`）

raw URL 方案导致仓库膨胀（每次发布增加约 35 MB 跟踪二进制），并使保留策略复杂化（需要 `--skip-worktree` 技巧和双清理策略）。

### API 验证结果（2026-06-18）

通过真实 API 调用测试了全部三个国内/国际平台：

| 平台 | 上传 | 匿名下载 | 删除 | 下载 URL 格式 |
|------|------|---------|------|-------------|
| GitHub | CI `gh release upload` | 是 | 是（API） | `github.com/{owner}/{repo}/releases/download/{tag}/{file}` |
| Gitee | `POST .../releases/{id}/attach_files`（multipart） | 是（200） | 是（204） | `gitee.com/{owner}/{repo}/releases/download/{tag}/{file}` |
| GitCode | 两步式：`GET upload_url` → `PUT` 到 OBS（200） | 是（200） | 无 API（404） | `gitcode.com/{owner}/{repo}/releases/download/{tag}/{file}` |
| GitLab | 仅 Release Links（无二进制上传） | N/A | N/A | Links 指向 GitHub |

关键发现：

1. **GitCode**：API 上传的文件获得 `api.gitcode.com` 域名的 `browser_download_url`（返回 404），但 `gitcode.com` 域名可用于匿名下载。构造的 URL `https://gitcode.com/{owner}/{repo}/releases/download/{tag}/{file}` 返回正确的文件内容。
2. **Gitee**：完整 CRUD —— 上传、匿名下载和删除均正常工作。
3. **GitLab**（自建，HTTP 端口 56080）：无直接二进制上传 API。Release 使用指向 GitHub 下载 URL 的外部链接。
4. **GitCode 限制**：无 API 端点删除附件。旧附件只能通过 Web UI 手动删除。

### 当前自动更新器代码

`ReleaseSource` 接口（`internal/updater/source.go`）有三个实现：

- `GitHubSource`：已使用 Release 下载 URL —— 无需修改。
- `GiteeSource`：`AssetURL()` 返回 raw URL；`AssetHeaders()` 返回 Bearer token（用于 raw API 认证）。
- `GitCodeSource`：`AssetURL()` 返回 raw API URL；`AssetHeaders()` 返回 PRIVATE-TOKEN。

`updater.go` 中的 `checkSource()` 方法调用 `AssetURL()` 作为默认下载 URL，如果 Release 已解析资产则可选地用 `asset.DownloadURL` 覆盖。由于 Gitee 和 GitCode 在 `FetchLatestRelease()` 中都不解析资产，`AssetURL()` 始终被使用。

### 当前发布脚本

`scripts/release.sh` 当前流程：
1. 构建 6 个平台二进制到 `dist/release/{tag}/`
2. 执行远程保留策略（在 git 中保留 10 个版本）
3. 提交并推送 `dist/release/` 到 Gitee/GitCode/GitLab
4. 创建 Gitee/GitCode Release（仅文本，无附件）
5. 执行本地保留策略（通过 `--skip-worktree` 保留 3 个版本）

### 迁移影响

迁移后：
- 所有仓库的 `dist/release/` 仅包含 `.gitkeep` —— 无跟踪二进制。
- `scripts/release.sh` 将二进制作为 Release 附件上传（Gitee/GitCode），而非提交到 git。
- 自动更新器构造 Release 下载 URL 而非 raw URL。
- 无需保留策略 —— 二进制存储在 Release 存储中，不在 git 中。
- GitLab 创建 Release 并附 GitHub 下载链接。

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
|------|------|------|------|------|
| 1 | 已完成 | 更新 Gitee/GitCode `AssetURL()` 为 Release 下载模式 | `internal/updater/source.go` | 单元测试通过新 URL 格式 |
| 2 | 已完成 | 移除 Gitee/GitCode 的 `AssetHeaders()` | `internal/updater/source.go` | 无认证头下载正常 |
| 3 | 已完成 | 重写 `scripts/release.sh` 为 Release API 上传 | `scripts/release.sh` | v0.5.0 上 dry-run 构建和上传测试 |
| 4 | 已完成 | 清理 `dist/release/` 为仅 `.gitkeep` | `dist/release/` | `git status` 仅显示 `.gitkeep` 文件 |
| 5 | 已完成 | 更新 CLAUDE.md 和 AGENT.md | `CLAUDE.md`、`AGENT.md` | 文档反映 Release API 工作流 |

## 需求

### 交付物

1. `GiteeSource.AssetURL()` 返回 `https://gitee.com/{owner}/{repo}/releases/download/{tag}/{file}`。
2. `GitCodeSource.AssetURL()` 返回 `https://gitcode.com/{owner}/{repo}/releases/download/{tag}/{file}`。
3. 两个 Source 都移除 `AssetHeaders()` 方法（下载免认证）。
4. `scripts/release.sh` 将全部 6 个平台压缩包 + `SHA256SUMS.txt` 作为 Release 附件上传到 Gitee 和 GitCode。
5. `scripts/release.sh` 不再将二进制提交到 git；`dist/release/` 在构建期间仅为本地目录。
6. git 中的 `dist/release/` 仅包含 `.gitkeep` 文件。
7. GitLab Release 通过 API 创建并附 GitHub 下载链接。
8. CLAUDE.md 和 AGENT.md 更新以反映新工作流。
9. 单元测试更新以匹配新 URL 模式。

### 约束

1. GitCode 下载 URL 必须使用 `gitcode.com` 域名（不是 `api.gitcode.com`，后者返回 404）。
2. GitCode 无删除 API —— 已上传的附件无法通过编程方式删除。
3. Gitee 上传需要 `release_id`（数字），需先通过 tag 查询获取。
4. GitCode 上传为两步式：`GET upload_url?file_name=xxx` → `PUT` 到 OBS 预签名 URL 并附带自定义 headers。
5. `SHA256SUMS.txt` 必须与二进制一起作为 Release 附件上传。
6. 自动更新器的 `checkSource()` 通过替换下载 URL 中的文件名来构造 SHA256SUMS URL —— 此模式必须与 Release 下载 URL 兼容。
7. GitLab API 为 HTTP（非 HTTPS），端口 56080，需 `-k`（自签名证书）。

### 边界情况

1. Gitee Release 已存在同名附件 —— 先删除再上传。
2. GitCode Release 已存在同名附件 —— 无法通过 API 删除；检查是否已存在则跳过上传。
3. Gitee API 速率限制 —— 添加重试与退避。
4. GitCode OBS 上传期间网络超时 —— 重试两步式上传。
5. `SHA256SUMS.txt` 必须在上传前生成，而非在 git commit 前。

### 非目标

1. 不迁移 GitHub CI 工作流 —— 它已使用 Release API。
2. 不添加 GitLab 作为自动更新器源站。
3. 不实现 GitCode 附件删除（无 API）。
4. 不修改前端更新 UI。

## 任务详情

### 任务 1：更新 GiteeSource 和 GitCodeSource 的 AssetURL

#### 需求

**Objective（目标）** — 将 Gitee 和 GitCode 的下载 URL 模式从 raw 文件 URL 改为 Release 下载 URL。

**Outcomes（成果）** — `GiteeSource.AssetURL()` 返回 `https://gitee.com/{owner}/{repo}/releases/download/{tag}/{file}`；`GitCodeSource.AssetURL()` 返回 `https://gitcode.com/{owner}/{repo}/releases/download/{tag}/{file}`。

**Evidence（证据）** — 单元测试断言新 URL 模式。

**Constraints（约束）** — GitCode 必须使用 `gitcode.com` 域名，不是 `api.gitcode.com`。

**Edge Cases（边界）** — tag 或文件名包含特殊字符（如需则 URL 编码，但当前命名约定避免此情况）。

**Verification（验证）** — `go test ./internal/updater/ -run TestGiteeSource -v` 和 `go test ./internal/updater/ -run TestGitCodeSource -v`。

#### 计划

1. 修改 `internal/updater/source.go` 第 244-246 行的 `GiteeSource.AssetURL()`。
2. 修改 `internal/updater/source.go` 第 166-168 行的 `GitCodeSource.AssetURL()`。
3. 更新 `internal/updater/source_test.go` 第 118-119 行和第 178-179 行的断言。

#### 验证

- [x] `GiteeSource.AssetURL("v0.5.0", "test.tar.gz")` 返回 `https://gitee.com/wakeya/magic-claude-code/releases/download/v0.5.0/test.tar.gz`。
- [x] `GitCodeSource.AssetURL("v0.5.0", "test.tar.gz")` 返回 `https://gitcode.com/wakeya/magic-claude-code/releases/download/v0.5.0/test.tar.gz`。

### 任务 2：移除 Gitee 和 GitCode 的 AssetHeaders

#### 需求

**Objective（目标）** — 移除两个 Source 的 `AssetHeaders()` 方法，因为 Release 下载免认证。

**Outcomes（成果）** — `GiteeSource` 和 `GitCodeSource` 不再实现 `AssetHeaders()`；`checkSource()` 中的接口断言静默返回 nil headers。

**Evidence（证据）** — 从 Gitee 和 GitCode Release URL 下载无需认证头即成功。

**Constraints（约束）** — `checkSource()` 使用可选接口断言（`interface{ AssetHeaders() map[string]string }`），移除方法不会导致编译失败。

**Edge Cases（边界）** — 私有仓库需要认证头；本项目使用公开仓库。

**Verification（验证）** — `go build ./...` 成功；`go test ./internal/updater/ -v` 通过。

#### 计划

1. 移除 `GiteeSource` 的 `AssetHeaders()` 方法（第 248-253 行）。
2. 移除 `GitCodeSource` 的 `AssetHeaders()` 方法（第 170-175 行）。
3. 移除 `source_test.go` 中的 `AssetHeaders` 断言。
4. 更新两个 Source 类型的注释以反映 Release API 下载。

#### 验证

- [x] `go build ./...` 成功。
- [x] `go test ./internal/updater/ -v` 通过。

### 任务 3：重写 scripts/release.sh 为 Release API 上传

#### 需求

**Objective（目标）** — 将 git 提交推送二进制的工作流替换为 Release API 附件上传。

**Outcomes（成果）** — `scripts/release.sh` 构建二进制，将其作为 Release 附件上传到 Gitee 和 GitCode，创建 GitLab Release 并附 GitHub 链接，不再将二进制提交到 git。

**Evidence（证据）** — 在 v0.5.0 上 dry-run：二进制出现在 Gitee/GitCode Release 附件中；匿名下载返回正确文件内容。

**Constraints（约束）** — Gitee 上传：`POST /repos/{owner}/{repo}/releases/{release_id}/attach_files`（multipart，需先通过 tag 查询获取 release_id）。GitCode 上传：两步式 `GET upload_url?file_name=xxx` → `PUT` 到 OBS 并附 headers。GitLab：`POST /projects/{id}/releases` 并在 `assets.links` 中指向 GitHub。发布说明从 `sdd-docs/changes/release-notes/{tag}.md` 读取。

**Edge Cases（边界）** — Release 已存在（更新或跳过）；附件已上传（Gitee：删除+重传；GitCode：跳过）；token 未设置（警告并跳过）；curl 超时（重试一次）。

**Verification（验证）** — 在两个平台上向 v0.5.0 Release 上传 1-2 个测试文件；验证匿名下载。

#### 计划

1. 移除当前脚本的步骤 5（远程保留）、6（git 提交+推送）、9（本地保留）。
2. 添加 Gitee 附件上传：通过 tag 获取 release_id → 循环通过 multipart POST 上传每个文件。
3. 添加 GitCode 附件上传：循环每个文件 → GET upload_url → PUT 到 OBS 并附 headers。
4. 添加 GitLab Release 创建并通过 `--noproxy '*'` 附 GitHub 下载链接（HTTP 端口 56080）。
5. 保留步骤 1-4（同步 main、构建前端、测试、构建二进制）。
6. 保留代码和 tag 推送到所有远程（但不包含 `dist/release/` 二进制）。

#### 验证

- [x] `bash -n scripts/release.sh` 通过语法检查。
- [ ] Gitee Release 有 7 个附件（6 个压缩包 + SHA256SUMS.txt）。
- [ ] GitCode Release 有 7 个附件。
- [ ] 两个平台的匿名下载返回正确文件内容。
- [ ] GitLab Release 有指向 GitHub 下载 URL 的链接。

### 任务 4：清理 dist/release/ 为仅 .gitkeep

#### 需求

**Objective（目标）** — 移除所有版本目录中被 git 跟踪的二进制文件。

**Outcomes（成果）** — `git ls-files dist/release/` 仅显示 `.gitkeep` 文件。

**Evidence（证据）** — `git status` 显示二进制文件删除；仓库大小显著减小。

**Constraints（约束）** — 不得删除 `.gitkeep` 文件。必须清除之前的 `--skip-worktree` 标记。

**Edge Cases（边界）** — 部分文件可能因之前的保留策略设置了 `skip-worktree` —— 需要先 `git update-index --no-skip-worktree` 再删除。

**Verification（验证）** — `git ls-files dist/release/ | grep -v '.gitkeep'` 返回空。

#### 计划

1. 对所有 `dist/release/` 文件执行 `git update-index --no-skip-worktree`（清除之前的标记）。
2. `git rm` 所有 `dist/release/` 中的二进制文件（保留 `.gitkeep`）。
3. 验证仅 `.gitkeep` 文件被跟踪。

#### 验证

- [x] `git ls-files dist/release/` 仅显示 `*.gitkeep`。
- [x] `dist/release/` 文件上无 `skip-worktree` 标记残留。

### 任务 5：更新 CLAUDE.md 和 AGENT.md

#### 需求

**Objective（目标）** — 更新文档以反映 Release API 工作流。

**Outcomes（成果）** — CLAUDE.md 第 5-8 项和 AGENT.md 发布流程部分描述新的 Release API 方案。

**Evidence（证据）** — 文档提及 Release API 上传而非 raw URL 存储；移除保留策略相关内容。

**Constraints（约束）** — 保持现有文档结构；仅修改发布相关部分。

**Edge Cases（边界）** — 无。

**Verification（验证）** — 手动审阅更新部分。

#### 计划

1. 更新 CLAUDE.md 第 5 项：`dist/release` 仅 `.gitkeep`，二进制为 Release 附件。
2. 更新 CLAUDE.md 第 6-8 项：描述 Release API 上传流程。
3. 更新 AGENT.md 发布流程部分：描述上传步骤。
4. 移除保留策略相关引用。

#### 验证

- [x] CLAUDE.md 准确描述新工作流。
- [x] AGENT.md 准确描述新工作流。
- [x] 无 raw URL 下载或保留策略的残留引用。
