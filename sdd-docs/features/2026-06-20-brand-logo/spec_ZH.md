# 品牌 Logo 替换规格

本地页面：登录页面、管理后台头部、浏览器标签页（favicon）  
代理入口：无  
参考源站：源 PNG（`/mnt/hgfs/VMShare/design-logo/bfbe6b50-322d-4838-861e-6a26c10c092e.png`，1254×1254，8-bit RGB）  
技术栈：Vue 3 + Vite + Tailwind、Go `embed.FS`  
最后更新：2026-06-20  
进度：3 / 3 已验证

## 整体分析（源站分析）

### 当前品牌状态

项目当前使用内联 SVG 几何图标（层叠的 V 形）作为 logo，出现在两处：

1. **登录页面**（[LoginView.vue:8-12](internal/frontend/src/views/LoginView.vue#L8-L12)）：`app-logo-mark` 容器 `w-14 h-14`（56×56 px），内部为 28×28 内联 SVG。用户反馈此处 logo 太小。
2. **管理后台头部**（[AppHeader.vue:4-8](internal/frontend/src/components/AppHeader.vue#L4-L8))：相同 SVG，容器 `w-8 h-8`（32×32 px），内部 SVG 18×18。
3. **Favicon**：无。[dist/index.html](internal/frontend/dist/index.html) 中无 `<link rel="icon">` 标签，不存在 `favicon.ico` 文件。

`app-logo-mark` 类在 SVG 后面应用了强调色实色背景（`var(--app-accent)`）。当切换为拥有自身视觉特征的 PNG 位图 logo 时，这个实色背景框需要作为设计决策来考虑，而非默认行为。

### 源资产

| 属性 | 值 |
| --- | --- |
| 路径 | `/mnt/hgfs/VMShare/design-logo/bfbe6b50-322d-4838-861e-6a26c10c092e.png` |
| 尺寸 | 1254 × 1254 px |
| 宽高比 | 1:1（正方形） |
| 格式 | PNG，8-bit RGB，非隔行 |
| 大小 | 956 KB |

源图为正方形高分辨率，所有目标尺寸（favicon 16/32/48、header 32、login 96）均可通过等比例下采样生成，无需裁剪。

### 静态资产管线

- Vite 构建输出至 `internal/frontend/dist/`；Go 通过 `embed.FS` 嵌入该目录，并由 `http.FileServer` 提供服务。
- Vite 默认的 `public/` 目录当前未使用。放在 `internal/frontend/public/` 中的资产在构建时会被原样拷贝到 `dist/`，这正是需要稳定 URL 引用的 `favicon.ico` 和 PNG logo 的正确存放位置。
- Vue 组件通过 URL（`public/` 文件）或 import（打包资产）引用资源。对于不应被哈希处理的 logo，`public/` 是正确选择。

### 图片处理工具

生成多尺寸 `favicon.ico` 和缩放 PNG 有两种候选方案：

1. **ImageMagick**（`convert`/`magick`）：Linux 上普遍可用，可直接从 PNG 生成多分辨率 `.ico`。
2. **Go 图像库**（如 `github.com/disintegration/imaging` + `.ico` 编码器）：会增加 Go 模块依赖。

建议：在构建时使用 ImageMagick 一次性生成资产；将生成结果提交到 `internal/frontend/public/`。无需运行时图像处理。

### 风险总结

1. 源 PNG 为 8-bit RGB（无 alpha 通道）。如果 logo 含白色/背景像素，在深色 UI 主题下会可见。需要决策：保持原样，还是预处理添加透明度。
2. 移除 `app-logo-mark` 强调色背景会改变登录页和头部的视觉层级。需要决策是否保留。
3. `favicon.ico` 必须从站点根路径（`/favicon.ico`）提供。Vite 的 `public/` 目录会自动处理。
4. 提交的 PNG 资产会通过 `embed.FS` 嵌入 Go 二进制，增大二进制体积。源 PNG 956 KB；下采样后的变体会小得多（估计总共 < 50 KB）。

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | 已规划 | 生成图片资产（favicon.ico + 缩放 PNG） | `internal/frontend/public/favicon.ico`、`public/logo-header.png`、`public/logo-login.png` | `file favicon.ico` 显示多分辨率 ICO；PNG 尺寸符合规格 |
| 2 | 已规划 | 集成 logo 到前端 | 修改后的 `LoginView.vue`、`AppHeader.vue`、`index.html`、`main.css` | 前端构建通过；logo 在浅色/深色主题下按预期尺寸渲染 |
| 3 | 已规划 | 构建与视觉验证 | 构建产物、截图证据 | `npm run build` 成功；手动浏览器检查登录页 + 头部 + 标签页 |

## 需求

### 交付物

1. `internal/frontend/public/favicon.ico` — 多分辨率 ICO，包含 16×16、32×32 和 48×48 图像，通过对源 PNG 等比例下采样生成。
2. `internal/frontend/public/logo-header.png` — 64×64 px PNG（为 retina 屏的 2× 显示尺寸），用于管理后台头部。
3. `internal/frontend/public/logo-login.png` — 192×192 px PNG（为 retina 屏的 2× 显示尺寸），用于登录页。
4. `internal/frontend/dist/index.html`（通过 Vite `index.html` 模板）在 `<head>` 中包含 `<link rel="icon" href="/favicon.ico" />`。
5. `LoginView.vue` 将内联 SVG 替换为 `<img>` 引用 `/logo-login.png`，显示尺寸 `w-24 h-24`（96×96 px）—— 从当前 56 px 增大，解决"太小"的反馈。
6. `AppHeader.vue` 将内联 SVG 替换为 `<img>` 引用 `/logo-header.png`，显示尺寸 `w-8 h-8`（32×32 px）—— 与当前尺寸一致，保持头部视觉连续性。
7. `app-logo-mark` 强调色背景在两处 logo 位置均移除；PNG 独立呈现。（决策点 — 见下文。）
8. 生成的资产提交到 `internal/frontend/public/`；`npm run build` 后会被拷贝到 `dist/` 并嵌入 Go 二进制。
9. 在 `CLAUDE.md` "常见问题"或项目 README 中添加简短说明，记录 logo 源资产位置和重新生成方式。

### 目录结构

```text
internal/frontend/
  public/                 ← 新增目录（Vite 静态资产）
    favicon.ico           ← 多分辨率（16, 32, 48）
    logo-header.png       ← 64×64
    logo-login.png        ← 192×192
  src/
    views/
      LoginView.vue       （修改：将 SVG 替换为 <img>，移除 app-logo-mark）
    components/
      AppHeader.vue       （修改：将 SVG 替换为 <img>，移除 app-logo-mark）
    styles/
      main.css            （修改：删除或重新定义 .app-logo-mark）
  index.html              （源模板，若存在 — 或通过 Vite 处理 dist/index.html）
```

### 约束

1. 源 PNG 为 8-bit RGB 无 alpha 通道。如需透明度（如适配深色主题），必须增加预处理步骤将背景透明化。**需要决策**（见下文决策点）。
2. 所有下采样必须保持 1:1 宽高比 — 不得拉伸。
3. `favicon.ico` 至少包含 16×16 和 32×32 尺寸以支持跨浏览器；推荐加入 48×48 以支持 Windows 桌面快捷方式。
4. 用于显示的 PNG 资产必须为 CSS 显示尺寸的 2×，以支持高 DPI 屏幕。
5. logo 在浅色和深色主题下均需保持清晰可读。如果源 PNG 的背景像素在深色模式下产生对比度问题，则必须进行透明度处理。
6. Go 二进制中无运行时图像处理 — 所有资产在构建时预先生成。
7. 资产文件名必须稳定（无内容哈希），以便 `<img src="/logo-*.png">` 引用无需构建时 URL 解析。

### 边界情况

1. 浏览器直接请求 `/favicon.ico`（不通过 HTML link） — Vite 的 `public/` 处理会在根路径正确提供服务。
2. 用户使用高 DPI 显示器 — 2× PNG 资产确保清晰渲染。
3. 深色主题激活 — logo 必须保持可见；取决于决策 D-002。
4. 源 PNG 有白色/浅色背景，与深色主题冲突 — 需要透明度预处理。
5. 开发机未安装 ImageMagick — 在规格中记录安装命令。
6. `internal/frontend/public/` 目录此前不存在 — Vite 会自动创建，无需特殊处理。

### 非目标

1. logo 的 SVG 版本 — 本次仅栅格 PNG/ICO 在范围内。
2. 深色主题专用变体 — 单一资产必须在两种主题下通用（必要时通过透明度实现）。
3. logo 动画或过渡效果。
4. GitHub/GitLab/GitCode 仓库头像变更 — 仅应用内 logo 在范围内。

## 任务详情

### 任务 1：生成图片资产

#### 需求

**Objective（目标）** — 通过对源 PNG 等比例下采样，生成三个所需的图像文件。

**Outcomes（成果）** — `internal/frontend/public/favicon.ico`（16+32+48）、`internal/frontend/public/logo-header.png`（64×64）、`internal/frontend/public/logo-login.png`（192×192）。

**Evidence（证据）** — `file`/`identify` 输出确认尺寸和 ICO 多分辨率结构；目视检查无变形。

**Constraints（约束）** — 必须保持源宽高比 1:1。推荐使用 ImageMagick。如果源的非透明背景与深色 UI 冲突，先执行 alpha 通道预处理（见决策 D-002）。

**Edge Cases（边界）** — 未安装 ImageMagick（记录 `apt-get install imagemagick` 或等价命令）；源 PNG 模式为 RGB 而非 RGBA（透明处理前可能需要 `-alpha set`）。

**Verification（验证）** — 运行 `identify internal/frontend/public/favicon.ico internal/frontend/public/logo-*.png` 并确认尺寸。

#### 计划

1. 创建 `internal/frontend/public/` 目录。
2. 生成多分辨率 ICO：`convert source.png -define icon:auto-resize=48,32,16 public/favicon.ico`。
3. 生成头部 PNG：`convert source.png -resize 64x64 public/logo-header.png`。
4. 生成登录 PNG：`convert source.png -resize 192x192 public/logo-login.png`。
5. （取决于 D-002）在步骤 2–4 前执行透明度预处理。

#### 验证

- [ ] `favicon.ico` 包含 16、32、48 px 尺寸。
- [ ] `logo-header.png` 为 64×64。
- [ ] `logo-login.png` 为 192×192。
- [ ] 所有资产宽高比为 1:1。

### 任务 2：集成 Logo 到前端

#### 需求

**Objective（目标）** — 将内联 SVG 占位符替换为 `<img>` 引用生成的 PNG 资产，添加 favicon 链接，调整登录页 logo 尺寸。

**Outcomes（成果）** — `LoginView.vue` 使用 `<img src="/logo-login.png">`，显示尺寸 `w-24 h-24`；`AppHeader.vue` 使用 `<img src="/logo-header.png">`，显示尺寸 `w-8 h-8`；`index.html` 包含 favicon `<link>`；`app-logo-mark` 背景按决策 D-001 移除或调整。

**Evidence（证据）** — 前端构建无错误；浏览器中目视检查浅色/深色主题下登录页、头部和标签页 logo 正确渲染。

**Constraints（约束）** — logo `<img>` 必须有 `alt` 属性（无障碍）。登录 logo 显示尺寸从 56 px 增至 96 px。头部 logo 保持 32 px。`app-logo-mark` CSS 类按 D-001 移除或保留。

**Edge Cases（边界）** — 图片加载失败（显示 `alt` 文本）；深色主题下背景冲突（由 D-002 缓解）；登录页因 logo 变大导致布局偏移（检查间距）。

**Verification（验证）** — `npm run build` 成功；两种主题下手动浏览器检查。

#### 计划

1. 修改 `LoginView.vue`：将 `app-logo-mark` div + 内联 SVG 替换为 `<img src="/logo-login.png" alt="Magic Claude Code" class="w-24 h-24 mx-auto mb-7" />`。
2. 修改 `AppHeader.vue`：将 `app-logo-mark` div + 内联 SVG 替换为 `<img src="/logo-header.png" alt="Magic Claude Code" class="w-8 h-8" />`。
3. 在 Vite `index.html` 模板中添加 `<link rel="icon" href="/favicon.ico" />`（如果仅存在 `dist/index.html` 则创建模板）。
4. 按决策 D-001 在 `main.css` 中移除或重新定义 `.app-logo-mark`。
5. 运行 `npm run build` 重新生成 `dist/`。

#### 验证

- [ ] 登录页显示新 logo，尺寸 96×96。
- [ ] 头部显示新 logo，尺寸 32×32。
- [ ] 浏览器标签页显示 favicon。
- [ ] 两种主题下 logo 对比度可接受。
- [ ] `npm run build` 通过。

### 任务 3：构建与视觉验证

#### 需求

**Objective（目标）** — 确认完整构建管线正常工作，logo 在运行的应用中正确渲染。

**Outcomes（成果）** — 嵌入了 logo 的构建产物；登录页、管理头部和浏览器标签页的截图证据。

**Evidence（证据）** — `npm run build` 输出；`go build` 成功；浏览器会话截图。

**Constraints（约束）** — 必须验证浅色和深色两种主题。必须验证 favicon 出现在浏览器标签页。

**Edge Cases（边界）** — 浏览器缓存 favicon（强制刷新或无痕模式）；`public/` 未被 Vite 拾取导致 logo 未嵌入（构建后验证 `dist/favicon.ico` 存在）。

**Verification（验证）** — 完整构建 + 手动浏览器检查。

#### 计划

1. 运行 `npm --prefix internal/frontend run build`。
2. 验证 `internal/frontend/dist/favicon.ico`、`logo-header.png`、`logo-login.png` 存在。
3. 运行 `go build ./...`。
4. 启动服务，在浏览器中打开，截取两种主题下的登录页 + 管理页。
5. 确认浏览器标签页显示 favicon。

#### 验证

- [ ] `dist/` 包含全部三个资产文件。
- [ ] Go 二进制构建成功。
- [ ] 登录页 logo 明显大于之前。
- [ ] 头部 logo 正确渲染。
- [ ] 浏览器标签页显示 favicon。
- [ ] 两种主题下外观正确。

## 决策（2026-06-20 已批准）

### D-001：`app-logo-mark` 背景处理 — 选项 (B)

保留圆角容器但背景透明 — 提供一致的间距/圆角，无颜色冲突。`.app-logo-mark` 类移除 `background: var(--app-accent)`；保留 `border-radius`。

### D-002：透明度 / 深色主题处理 — 选项 (B)

通过 ImageMagick 预处理添加透明度（`-fuzz` + `-transparent white`）。将近白色像素转为透明，使 logo 能适配浅色和深色两种主题。

### D-003：登录页 Logo 最终尺寸 — 选项 (B)

96 px（`w-24`）— 从当前 56 px 显著增大，但不会主导登录面板。
