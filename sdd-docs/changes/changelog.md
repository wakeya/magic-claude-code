# Changelog

所有重要变更记录在此文件中。

格式遵循 [Keep a Changelog](https://keepachangelog.com/zh-CN/1.1.0/)。
版本号对应 git tag（semver，自 v0.1.0 起），与 `release-notes/vX.Y.Z.md` 一一对应；早期条目以日期标识。

---

## v0.13.0 (2026-07-06)

### Added
- **Node.js 客户端 CA 信任自动配置**：Node.js 忽略操作系统 CA 信任库，系统级 CA 安装无法让 Claude Code（Node 客户端）信任 mcc 的 MITM 证书。新增 bootstrap 步骤将 `NODE_EXTRA_CA_CERTS` 持久化到用户环境——Windows 走 `setx`（用户级注册表）+ pwsh `$PROFILE` 兜底，macOS 走 `launchctl setenv` + shell profile，Linux 走 shell profile。通过 CA 指纹 marker 实现幂等；检测到用户自定义值时保留不覆盖；部分失败（setx/launchctl 失败但 profile 已写入）不标记、下次启动重试；Docker 容器内跳过
- **mcc.exe 应用图标**：Windows 二进制内嵌应用图标，任务栏 / 开始菜单显示品牌图标。新增 `make icon` 目标从 logo 自动重新生成（裁剪透明边距 + 230px 适配 256 画布，预留 5% 安全边距避免边缘裁切）

### Security
- **PowerShell 注入防护（P2-1）**：CA 路径渲染为 PowerShell 单引号字面量（home 相对路径走 `Join-Path`，绝对路径单引号包裹），拒绝 CR/LF 以阻断 `$()` / 反引号 / 换行命令注入
- **symlink 与特权运行防护（CWE-59 / P2-2）**：拒绝符号链接 profile 与 marker，防止跨路径篡改；检测到特权运行（管理员 / root）时拒绝 profile 变更，避免写入错误的用户配置
- **marker 身份绑定（F-4）**：NodeCA marker 绑定证书路径与用户身份，写入 / 读取均校验身份一致性，防止跨用户 marker 误用
- **fail-closed 校验链（F-1 / F-2）**：环境变更前先检查 pwsh / POSIX profile 的用户自定义值，有则跳过 `setx` / `launchctl` 不覆盖；profile 父链与文件系统根校验失败、profile 读取异常时 fail-closed；pwsh profile 部分失败返回 `ErrPartialSuccess`，不再因任一成功而掩盖整体失败

### Changed
- **Windows 环境变更广播**：`setx` 写入后广播环境变量变更，确保已运行进程能感知新的 `NODE_EXTRA_CA_CERTS`；Windows 客户端重启序列说明同步更新
- **旧版 pwsh profile 自动迁移**：升级场景下检测并清理旧版 `$mccCa` 残留行，一次性迁移到 mcc marker 块，恢复 marker + `setx` 持久化路径执行

### Docs
- Node.js 客户端 CA 信任自动配置 feature spec 与评审记录（中英双语）：`sdd-docs/features/2026-07-04-node-extra-ca-certs-auto-setup/`
- Node CA 持久化正确性修复、Windows 环境刷新、pwsh profile 迁移的 design / plan / review 文档

---

## v0.12.0 (2026-07-04)

### Added
- **智谱 Web 工具参数错误的反应式 recovery**：上游返回结构化 `error.code == "1210"`（或精确的 `[1210]` + `API 调用参数有误` 消息）时，识别为工具 schema 兼容问题，复用既有 `cleanTools` 删除根级 `$schema` / `$id` / `$comment` / `additionalProperties` 与工具级 `cache_control`，仅在清理实际改变请求时重试一次。修复 Claude Code 动态加载 `WebFetch` / `WebSearch` 后被智谱端点以 400 拒绝的问题（v0.11.0 Known Issues 的 recovery 落地）
- **有界 SSE 异常诊断**：SSE 流被中断、不完整、解析失败或包含明确 error event 时，输出一条 `[Stream] anomaly` 日志，含事件计数、content block 类型、stop reason、数字错误码、字节数与安全请求摘要。仅保留白名单枚举值，不记录 text / thinking / tool input / error.message / 原始 payload

### Security
- **重试请求继承客户端 context（CWE-400）**：rectifier 重试改用 `http.NewRequestWithContext(origReq.Context(), ...)`，客户端断开时立即取消重试，避免无限占用供应商资源
- **SSE 数字错误码内存硬化（CWE-400）**：异常诊断的数字错误码 map 限制不同 key 上限 16 个，超限归入 `other`，防止恶意或被攻陷供应商用大量不同错误码消耗内存

### Changed
- **错误分类优先级明确化**：`unsupported/unknown content type` 短语拆为独立高优先级分支，先于智谱 1210 与通用 invalid-request 兜底判断；结构化 `code == "1210"` 优先于通用 invalid-request 短语，避免误路由到错误的清理路径

### Docs
- 智谱 Web 工具兼容性恢复 feature spec（中英双语）：`sdd-docs/features/2026-07-01-zhipu-web-tools-compat/`
- 评审记录（中英双语，确认 recovery 正确性、安全边界与无遗留缺陷）

---

## v0.11.0 (2026-07-01)

### Security
- **请求参数日志脱敏（CWE-532）**：上游返回 ≥400 时，`[Proxy] Error` 日志的请求摘要改为类型安全白名单——只保留 `model`、`stream`、数值生成参数（`max_tokens`/`max_output_tokens`/`temperature`/`top_p`/`top_k`）与集合计数（`messages`/`tools`/`input`）。`system` 系统提示词、`metadata`、凭据、未知扩展与消息正文不再写入进程日志

### Fixed
- **SSE-labeled HTTP 错误不再静默丢失**：上游以 `Content-Type: text/event-stream` 返回 4xx/5xx 时，原先被 SSE 心跳路径抢先，错误不记日志。现改为先判状态码再判 SSE（`StatusCode < 400 && isSSEStream`），此类错误正确落到 `[Proxy] Error` 日志

### Changed
- **结构化协议诊断**：错误日志的请求摘要升级为结构化协议摘要——`stream` 区分缺失/false/true；`messages` 细化到角色（user/assistant）计数与 content block 类型计数；`tools` 记录名称摘要与 schema 关键字（`$schema`/`additionalProperties`/`format` 等）出现次数。仅保留定位上游协议兼容问题所需的结构信息，不含 prompt、正文或凭据

### Docs
- SSE-labeled HTTP 错误处理 feature spec（中英双语）：`sdd-docs/features/2026-06-30-non-2xx-sse-error-handling/`
- 智谱 Web 工具兼容性恢复 spec（中英双语）：`sdd-docs/features/2026-07-01-zhipu-web-tools-compat/`
- 安全审查记录（中英双语，确认 CWE-532 闭合、无遗留缺陷）

### Known Issues
- 智谱 GLM 上游对 Claude Code 动态加载 `WebFetch`/`WebSearch` 后的请求可能返回 `1210` 参数错误（与 `stream` 字段缺失触发的 fallback 路径相关）。本版提供结构化诊断能力便于定位，recovery 实现留待后续版本

---

## v0.10.0 (2026-06-30)

### Added
- **供应商额度查询**：供应商管理页新增额度查询能力，支持手动查询与自动定时刷新，结果以快照形式缓存，并在供应商卡片与服务状态页展示
  - **多供应商适配**：内置 Token Plan（Kimi、智谱、MiniMax、ZenMux、火山方舟）与官方余额（DeepSeek、StepFun、SiliconFlow、OpenRouter、Novita）适配器，按各供应商真实协议解析用量；另支持自定义脚本、通用余额、NewAPI 三种通用模板
  - **凭证安全**：脚本与 ZenMux 凭证独立存储并按供应商绑定，供应商卡片密钥不会被路由到其他供应商或 ZenMux 端点；AccessKeyID 回显时掩码；自动迁移历史凭证格式
  - **定时与并发**：生产查询按供应商去重，避免重复请求；启动按供应商抖动分散首次查询；可配置自动查询间隔与并发上限
  - **配置编辑**：额度配置以模态框形式编辑，自动检测供应商类型并支持显式选择，MiMo 显示延期提示，保存前可草稿测试
  - **脚本安全边界**：自定义脚本请求强制同源校验（含重定向与 HTTPS→HTTP 降级防护）、禁止 userinfo、错误信息对密钥逐字脱敏、JS 沙箱解析/提取超时限制、请求与响应体大小上限
- **服务状态页增强**：当前供应商卡片右上角展示 5 小时/7 天用量利用率与余额（与供应商管理卡片一致），数据复用已加载的额度快照；监听状态块的代理/配置/路由三个地址改为单行三列等分展示

### Fixed
- 供应商导入后同步刷新额度快照状态，并校验导入响应状态码，正确识别部分失败
- CNY 余额显示符号修正为 ¥（原先误用 $）
- 额度快照加载与清理失败不再被静默吞掉，显式上报给前端

### Docs
- 供应商额度查询功能 spec、设计与实现计划（中英双语）：`sdd-docs/features/2026-06-27-provider-quota-query/`
- 复审记录：`review-notes`（针对初始实现 `33af2ed`）、`review-followup`（针对当前 HEAD，确认全部阻断/高危问题闭环）

---

## v0.9.3 (2026-06-24)

### Added
- 全局回到顶部悬浮按钮：从 `SessionBrowser` 提升到 `DashboardView` 层级，所有 tab 页共享；页面滚动超过 100px 时在右下角显示，点击平滑回到顶部；`z-30` 层级低于所有 modal（outline `z-40`、cleanup `z-50`），不干扰弹窗交互；短内容 tab（如证书信息）自动隐藏

### Docs
- 全局回到顶部功能 spec（中英双语，3/3 任务已实现并验证）：`sdd-docs/features/2026-06-24-global-back-to-top/`

---

## v0.9.2 (2026-06-24)

### Fixed
- 管理面板标签页切换布局抖动：`html { scrollbar-gutter: stable; overflow-y: auto }` 消除滚动条 reflow（约 15px 横向位移）；会话列表数据预加载到 `DashboardView.onMounted`，消除 `SessionBrowser` 每次激活的异步空→满二次布局；sessions 列表骨架屏兜底首次加载；加载失败优先显示错误信息

### Docs
- 管理面板标签页切换布局抖动修复 spec（中英双语，4/4 任务已实现并验证）：`sdd-docs/features/2026-06-24-dashboard-tab-layout-shift/`

---

## v0.9.1 (2026-06-24)

### Added
- 供应商导入导出：供应商页新增 JSON 导入导出，支持多主机批量迁移供应商配置。每卡片左上角复选框，工具栏全选/导出/导入按钮；导出含真实 token 的 JSON（下载前确认风险），导入带预览（新增/冲突计数）和冲突策略（skip/overwrite/duplicate，默认 skip）
- 后端 API：`POST /api/providers/export` 按 ID 导出含真实 token；`POST /api/providers/import` 单次 Load→Save，version 校验，无效供应商跳过计入 errors，显式拒绝非 POST（405）
- 全选控件：标题左侧三态复选框（全选/部分选中 indeterminate/未选）

### Fixed
- 导入版本校验：前端解析校验 `version === 1`，预览前拒绝非 1 或缺失版本
- duplicate 策略语义：仅冲突项生成新 ID，非冲突项保留原 ID 正常导入
- 导出失败提示：新增 `providers.export_failed` 文案，不再误用导入格式错误文案

### Docs
- 新增供应商导入导出功能 spec（中英双语）：`sdd-docs/features/2026-06-23-provider-import-export/`

---

## v0.9.0 (2026-06-23)

### Added
- 监听地址可配置：proxy/admin 监听地址和端口支持 CLI flag（`-proxy-listen`/`-proxy-port`/`-admin-listen`/`-admin-port`）、环境变量（`MCC_*`）和配置文件三层覆盖，默认行为不变；前端只读展示实际监听地址
- CLI 本地化帮助：`mcc -h` 按系统语言显示 flag 说明；`mcc -v` 打印版本并退出
- `/api/status` 新增 6 个监听字段（proxy/admin/gateway addr+port），反映 CLI/env/config 解析后的实际生效值
- 前端"监听状态"只读区块，附操作风险提示（非 443 需端口转发、127.0.0.1 仅本机可达）
- IPv6 地址归一化：`normalizeListenAddr` 统一剥离 RFC 2732 方括号

### Fixed
- 启动失败立即退出：服务监听失败通过 `startupErr` 通道触发 `log.Fatalf`，不再以"部分服务可用"状态继续运行
- Gateway 热重启不误杀进程：gateway goroutine 过滤 `http.ErrServerClosed`
- IPv6 地址拼接全面修复：`fmt.Sprintf` 全部替换为 `net.JoinHostPort`（启动、handler restart、bootstrap 指令、前端展示）
- 前端 IPv6 防御性格式化：`formatListenAddress` 先剥离已有括号再按需添加

### Changed
- 推荐 hook 改用原生 `rtk hook claude`（跨平台，去除 jq 依赖）；config_path_note 移除"Windows hook 可能不生效"的过时结论

### Docs
- 新增监听地址配置功能 spec（中英双语）

---

## v0.8.1 (2026-06-23)

### Fixed
- Windows 引导乱码修复：`certutil`/`setx` 子进程输出按 GBK/CP936 解码为 UTF-8（`decodeCmdOutput`），错误信息不再乱码；已是 UTF-8 的输出不误转，解码失败回退原始字节

---

## v0.8.0 (2026-06-22)

### Security
- URL 凭证脱敏：`RedactURL` 剥离 `https://user:pass@host` 的 userinfo；代理入口/出口日志、usage 读取路径统一走脱敏，防止 provider URL 凭证或签名泄露
- usage 读取二次脱敏：防御历史脏数据，Coverage/Requests 两条输出路径均不泄露

### Added
- 透明模式自动引导：启动时自动尝试 hosts 修改、CA 信任安装、MCC_ROOT 环境持久化；失败不阻塞启动，按优先级降级
- 三连接模式与自动降级：透明 > 隧道 > 网关；header 模式按钮可持久化到后端，`/api/config`、`/api/status` 暴露首选/实际模式
- i18n 系统语言检测：`zh*` 默认中文，其他默认英文；`MCC_LANG` 可手动覆盖
- 首运行 MCC_ROOT 持久化：从任意工作目录启动均可自动定位证书
- fish shell profile 去重增强：导出行匹配更语义化，避免重复追加
- Docker 宿主机 helper 机制：`MCC_HOST_HELPER` 支持挂载 helper 检测/修改宿主机 hosts 与 CA 信任
- 宿主机一键配置脚本：`setup-host.sh`（Linux/macOS）、`setup-host.ps1`（Windows）；`docker-host-helper.sh` 作为容器内默认状态检测器
- docker-compose 部署：集成端口映射、数据卷、usage 同步目录、NET_BIND_SERVICE
- 前端连接模式入口与三模式说明弹窗，含 i18n
- CI 测试工作流：`.github/workflows/test.yml`，push/PR 跑 `make test`（含 race detector）
- Release archive 附带 `setup-host.sh`/`setup-host.ps1`

### Changed
- 请求日志增强：入口日志延后到 backendURL 确定后打印，附带 `provider_name` 与脱敏 `upstream_url`；rate-limit 日志改用 `provider_name` 替代 `provider`（ID）
- bootstrap 结果模型：hosts/CA/环境持久化独立记录，状态持久化到 data 目录抑制重复失败日志
- `AGENT.md` 重命名为 `AGENTS.md`

### Docs
- 新增透明模式自动引导 feature spec：`sdd-docs/features/2026-06-20-transparent-mode-bootstrap-and-fallback/`
- 新增 fish profile 去重 feature spec：`sdd-docs/features/2026-06-21-fish-profile-dedup-scanner/`
- README 新增英文版 `README.en.md` 并双语互链
- `CLAUDE.md` 关键文件表补充 `internal/bootstrap/` 和 `internal/i18n/`
- `sdd-docs/features/README.md` 索引补登记两个新 feature

---

## 2026-06-12

### Fixed
- 修复 Windows 二进制缺少 IANA 时区数据导致 `tz=Asia/Shanghai` 等浏览器时区查询失败，进而使服务状态和使用统计页面显示 0 的问题
- 修复部分上游 SSE 在 `message_stop` 后不关闭连接时，代理等待 EOF 导致流式 usage 迟迟不落库的问题
- 修复兼容 provider 将 usage 放在 `message_stop` payload 中时，终止事件 usage 可能被跳过的问题

### Docs
- 新增 Windows 使用统计可靠性修复 feature specs：`sdd-docs/features/2026-06-12-windows-usage-statistics-fixes/`
- 新增对应 change specs：`sdd-docs/changes/2026-06-12-windows-usage-statistics-fixes/`
- 更新 `sdd-docs/features/README.md`，说明新的 `spec.md` / `spec_zh.md` 双语单文件规格格式

---

## 2026-06-11

### Added
- 使用统计页新增快捷日期范围：今日、近 7 天、近 30 天；默认近 7 天，近 7/30 天不包含今天
- 使用统计页新增 `清除数据` 操作，支持默认保留 `session_log_sync`，也支持勾选后重置 Session Log 同步状态
- Admin API 新增 `POST /api/usage/clear`，用于清除 usage 统计数据并可选重置 Session Log 同步状态
- 会话记录清理提示新增 Windows 预览命令和 Windows 交互清理命令
- 统计口径筛选项新增问号提示，解释有效统计、实时请求、Session Log、全部原始
- 编辑供应商弹窗桌面端宽度加宽至约供应商列表内容区的四分之三，提升模型映射和多模态配置编辑体验

### Fixed
- 修复 Usage 覆盖率表格横向内容被遮挡且缺少底部横向滚动条的问题
- 修复 Usage 覆盖率表头提示框被表格区域遮挡的问题，表格内提示改为向下弹出
- 修复 Windows 清理命令路径提示：原生 `C:\Users\<user>\...` 路径保留盘符并替换用户名为 `用户名代理`
- 修复 Windows 清理命令路径中的双引号、控制字符和 Windows 非法路径字符清洗问题，降低复制提示命令后的解析风险
- 修复二进制未配置管理密码时生成随机密码但启动输出不展示的问题；随机密码现在会在启动输出中打印一次，显式密码不会回显

### Docs
- 更新 Usage statistics specs，补充清除统计数据 API、前端交互、迁移场景和验证项
- 更新 Session Browser specs，补充清理提示的 Linux/macOS 与 Windows 双平台命令、Windows 路径转换和安全清洗约束
- 更新 Multimodal Model Switch specs，补充 Provider 弹窗宽度约束
- 更新 Claude Proxy specs 和 README，说明二进制默认 `./data`、随机密码打印和 Windows 后台日志查看方式

---

## 2026-06-09

### Added
- Provider 配置新增“多模态切换”和“多模态模型 ID”，请求含图片、PDF、音频或视频等非文本内容时可自动切换到指定多模态模型
- 代理请求转换支持递归检测 `messages` / `system` 中的非文本内容，覆盖截图工具返回的 `tool_result.content` 图片
- SQLite Provider 表新增多模态配置字段，并支持旧数据库自动补列
- Admin API 创建、查询、更新、复制 Provider 时保留多模态配置，并校验开启开关时必须填写多模态模型 ID
- Provider 弹窗和卡片增加多模态配置 UI 与提示文案

### Fixed
- 修复 Mimo 文本模型收到截图图片时返回 `No endpoints found that support image input` 的配置层解决路径
- 修复 Session Browser 移动端大纲返回顶部按钮缺少 sticky 底部定位的问题

### Docs
- 新增多模态模型切换 feature specs：`sdd-docs/features/2026-06-09-multimodal-model-switch/`
- 同步英文主文档和中文 `_ZH` 文档：requirements、plan、decisions、validation、status

---

## 2026-05-29

### Added
- 识别 DeepSeek "thinking must be passed back" 和 "tool_use without tool_result" 两种新的 400 错误模式，支持被动重试恢复
- 非流式 >= 400 错误日志增加兼容性相关 headers 信息（`Anthropic-Version`、`Anthropic-Beta`、`Content-Type`）

### Fixed
- 修正待修复问题文档结构，新增 `sdd-docs/known-issues/` 目录记录未解决的兼容性问题

### Docs
- 新增 DeepSeek 模型在 Claude Code >= 2.1.150 中报 400 错误的待修复记录
- 将 `docs/superpowers/plans/` 合并到 `sdd-docs/superpowers/plans/`，清理空 `docs/` 目录
- 新增 `sdd-docs/changes/changelog.md`

---

## 2026-05-28

### Added
- 导出 HTML 增加右侧大纲导航面板（基于 IntersectionObserver 的滚动高亮）
- 导出 HTML 增加返回顶部按钮
- 导出 HTML 小屏幕下大纲面板切换为底部浮动按钮 + 弹窗模式
- Session Browser 大纲标题增加条目数量显示
- Session Browser 大纲标题支持中英文本地化

### Fixed
- Session Browser 返回顶部按钮在所有屏幕尺寸下可见
- Session Browser 返回顶部按钮改为 fixed 定位

---

## 2026-05-21

### Added
- Session Browser 项目目录折叠、标题重命名、UI 优化、跨平台路径修复
- 管理后台全局亮/暗色主题系统（CSS 变量 + 偏好持久化）
- 导出 HTML 主题与管理面板一致（暗色模式下导出暗色 HTML）
- Session Browser 大纲面板返回顶部按钮

### Fixed
- 主题 tooltip 溢出和 Session Browser 滚动问题
- Usage 覆盖率提示样式统一
- 移除冗余的 Session Browser 选择提示

---

## 2026-05-20

### Added
- 非标准 content block（如 `tool_reference`）反应式清理，修复第三方供应商 400 错误
- 请求日志分页总条数、状态页问号提示

### Fixed
- Usage 日期过滤和请求行展示修复

---

## 2026-05-18

### Added
- Claude Session Browser（浏览、搜索、导出会话）
- 管理后台前端重构为 Vue 3 + TypeScript + Tailwind CSS（Flat Design）
- 前端国际化支持，默认中文，支持中英文切换
- SSE 流心跳机制，防止上游空闲时连接超时

---

## 2026-05-15

### Added
- SQLite 配置存储（替代 JSON 文件）
- Usage 统计 dashboard（API + 前端图表）
- 流式 usage 解析与记录
- 反应式供应商兼容性错误恢复（400 错误自动清理重试）
- 供应商 thinking 支持开关

---

## 2026-05-13

### Added
- 硬编码端点拦截，覆盖 Claude Code 源码中所有 API 端点
- 供应商复制配置、Token 明文查看
- 统一代理请求日志格式（reqID 关联入口/出口）

### Fixed
- 保留 Claude Code Anthropic 协议字段
- 低优先级端点拦截增强

---

## 2026-05-11

### Added
- 管理后台 REST API 配置服务
- bcrypt 认证 + Session Token

---

## 2026-05-10

### Added
- 透明代理服务（Header 转发）
- CA 证书生成与管理
- 服务器证书生成与管理
- JSON 配置存储
- Docker 部署（Alpine）
