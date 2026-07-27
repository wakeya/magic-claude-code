# Frame 多文件发布写门禁规格

**代理入口：** `internal/proxy/frame.go`（`handleFrameEndpoint`，约 L20-74；403 写门分支 L46-53）；`internal/proxy/hardcoded.go`（`/api/frame/` 前缀命中 L88、转发 `handleFrameEndpoint` L297；`isHardcodedEndpoint` 不变）；`internal/proxy/helpers.go`（`methodAllowed`，405 写入处）
**测试入口：** `internal/proxy/frame_test.go`（`TestFrameEndpointCompatibility`，新增子测试）
**参考来源：** `/home/www/workspace/open-software/claude_code/073_claude_spy/claude_code_src_2.1.220.js`（`Pi.post("/api/frame/deploy/prepare"...)`、`Pi.post("/api/frame/upload"...)`、降级函数 `T`）；`sdd-docs/research/2026-07-15-intercepted-endpoints.md`（C 节 Frame 子端点展开）；2.1.211 vs 2.1.220 路径字面量 diff（仅 2 条新增，均在本族）
**技术栈：** Go 1.26 标准库
**最后更新：** 2026-07-26
**进度：** 0 / 3

## 整体分析（源站分析）

### 现象

Claude Code 2.1.220 新增**多文件 frame 发布**流程，引入两条新的一阶 Anthropic API 端点：

- `POST /api/frame/deploy/prepare` —— 预检：客户端发 `{slug?, shas:[SHA…]}`，服务端返回 `{slug, missing:[SHA…]}`（哪些文件需上传）。
- `POST /api/frame/upload` —— 上传：客户端发 `{slug, files:[{path, content, contentType}]}`，补传 `missing` 列出的文件内容。

完整发布流程：`prepare`（查缺）→ `upload`（补传）→ `deploy/init`|`deploy/direct`（提交）→ `deploy/complete`（收尾）。其中 `init`/`direct`/`complete` 在 2.1.211 即存在（mcc 已处理），`prepare`/`upload` 是 2.1.220 新增。

mcc 当前对这两条新路径**无显式 case**：`/api/frame/` 前缀命中后进入 `handleFrameEndpoint`，落到 `default` 分支（`frame.go:66`），`methodAllowed(GET, DELETE)` 对 POST 失败 → `helpers.go` 的 `methodAllowed` 写入 **405 `method_not_allowed` + `Allow: GET, DELETE`**。

### 客户端源码分析（2.1.220）

**调用形态**（均为 `Pi.post`，`host:"frame"` 分发表 → `BASE_API_URL` → 生产默认 `https://api.anthropic.com`，属 mcc 必须处理的一阶流量）：

```js
// prepare
Pi.post("/api/frame/deploy/prepare",
  {...slug!==void 0&&{slug}, ...shas.length>0&&{shas}},
  {host:"frame", auth:"required", refreshOAuth:!0, timeout:30000, validateStatus:()=>!0})
// upload
Pi.post("/api/frame/upload",
  {slug, files:files.map(({f,wire})=>({path:f.path, content:wire, contentType:f.contentType}))},
  {host:"frame", auth:"required", refreshOAuth:!0, timeout:60000, validateStatus:()=>!0, maxBodyLength:2*I6})
```

**响应处理（决定是否需要兼容响应）：**

- `validateStatus:()=>!0` —— axios 不因任何状态码抛错，客户端自行判 status。
- prepare 循环 `for(G=0;G<2;G++)`：`let j=await F(...); if(!j.ok) break;` —— **非 2xx（含 mcc 的 405）立即退出循环，不重试**；仅 `j.ok && j.status===429` 时按 `retry-after` 重试一次。upload 同构（`if(L.ok&&L.status===429)` 重试）。
- 失败兜底函数 `T`：`Oh(\`multi-file publish is not available here yet (server or proxy rejected it: ${I}${R?` — ${R.slice(0,200)}:""})…\`)` —— **客户端显式预判了「代理拒绝」**，把错误详情（截前 200 字）拼进用户可见文案。
- 前置客户端开关：源码另有 `pe("artifact_publish","multifile_flag_off",...)` + `Oh("multi-file publish is not enabled for ...")` 分支——标志关闭时客户端直接报「未启用」，**根本不发 prepare/upload**。

→ **安全结论**：当前 mcc 的 405 已被客户端优雅降级（`!ok` break → `T` 兜底）。**无泄露（fail-closed，不转发上游）、无重试风暴、无崩溃**。功能降级层面「能用」。

### 问题（为何仍要改）

405 的响应体 `Allow: GET, DELETE` 对**POST 端点**是**误导性**的——这两条端点在 Anthropic API 本就是 POST，mcc 只是不实现写门而非「方法不对」。该误导性文案会原样进入客户端 `T` 的错误详情（`server or proxy rejected it: ...Only GET, DELETE are allowed...`），让用户误以为是方法用错。

对比同族 `deploy/init`|`deploy/direct`（`frame.go:46-53`）已显式返回 **403 `write_gate_disabled`**（注释：「客户端发布路径能识别 write_gate_disabled」），`prepare`/`upload` 落到 default → 405 是**不一致**且语义错误。

### 设计决策

把 `deploy/prepare` 与 `upload` 并入现有 403 `write_gate_disabled` 分支（与 `deploy/init`|`deploy/direct` 同族同响应）。

| 方案 | 说明 | 是否采用 |
| --- | --- | --- |
| A. 并入现有 403 case | 两条路径加进 `deploy/init\|\|deploy/direct` 的 case，复用同一 403 `write_gate_disabled` 响应 | ✅ |
| B. 新增独立 case | 单独 case 返回不同 body | — （与同族无差异，徒增重复） |
| C. 保持 405 | 不改 | — （文案误导、与同族不一致） |
| D. feature flag 关闭 | 在 `/api/feature/` 关闭 `multifile_flag`，让客户端不发请求 | — （flag 名不稳定、且即便发了也已被 403 优雅降级，收益不抵维护成本；留待后续按需） |

选 A 的理由：

1. **语义正确**：写门关闭，而非方法不对；
2. **与同族一致**：四个 deploy-族写端点（init/direct/prepare）+ upload 统一 403 `write_gate_disabled`；
3. **客户端体验**：`T` 兜底文案的错误详情从 `Only GET, DELETE allowed` 变为 `write_gate_disabled`，清晰；
4. **用户最终所见不变**：仍走 `T` 的「not available here yet」降级，无功能回归；
5. **改动最小**：一处 case 条件扩展 + 两条测试。

### 影响面

| 文件 | 改动 |
|------|------|
| `internal/proxy/frame.go` | L46 case 条件加 `\|\| path=="/api/frame/deploy/prepare" \|\| path=="/api/frame/upload"`；函数头注释（L8-17）的第 4 条扩展列出 prepare/upload |
| `internal/proxy/frame_test.go` | 新增 2 个 `t.Run` 子测试：`deploy prepare returns write-gate denied`、`upload returns write-gate denied` |
| 其它 | **不动**：`isHardcodedEndpoint`（前缀 `/api/frame/` 已覆盖）、`hardcoded.go` 转发、其余 frame 子路径、路由、failover、usage |

### 向后兼容

- 客户端 2.1.211 及更早：不发 prepare/upload（该流程是 2.1.220 引入），新增 case 对它们不可达，零影响。
- 客户端 2.1.220：原 405 → 现 403 `write_gate_disabled`，均被 `T` 优雅降级，用户所见「not available」不变；错误详情更清晰。
- 不影响 mcc 对 init/direct/complete/track/frames/contract/{slug} 的既有响应。

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | Planned | `frame.go`：403 写门 case 扩展覆盖 `deploy/prepare` + `upload`；更新函数头注释 | `internal/proxy/frame.go` | `go test ./internal/proxy/ -run TestFrameEndpointCompatibility` |
| 2 | Planned | `frame_test.go`：新增 2 个子测试断言 403 `write_gate_disabled` | `internal/proxy/frame_test.go` | 同上 |
| 3 | Planned | 全量回归 + 提交 | 验证记录 | `go test ./...` + `go vet ./...`；`git commit`（不 push） |

## 需求

### 交付物

1. `handleFrameEndpoint`（`frame.go:46`）的 403 写门 case 条件，由 `path == "/api/frame/deploy/init" || path == "/api/frame/deploy/direct"` 扩展为 additionally `|| path == "/api/frame/deploy/prepare" || path == "/api/frame/upload"`；响应体不变（`{"error":"Frame publishing is unavailable in MCC local mode","reason":"write_gate_disabled"}`，HTTP 403）。
2. `frame.go` 函数头注释（L8-17）第 4 条由「deploy/init|direct」更新为「deploy/init|direct|prepare、frame/upload」，保持路由表文档与代码一致。
3. `frame_test.go` 新增两个 `t.Run` 子测试（结构对齐既有 `deploy init returns write-gate denied`，L57-75）：分别对 `POST /api/frame/deploy/prepare`、`POST /api/frame/upload` 断言 `rec.Code == 403` 且 `resp.Reason == "write_gate_disabled"`、`resp.Error` 含 "unavailable"。
4. 不改 `isHardcodedEndpoint`、`hardcoded.go` 转发逻辑、其它 frame 子路径、`methodAllowed`、路由、failover、usage。

### 约束

- 仅本地响应，绝不转发上游（Frame 族 fail-closed 不变）。
- 不改响应体 schema（`error`/`reason` 两字段），与 init/direct 完全一致，避免客户端识别分歧。
- 不引入新 import、不改 `methodAllowed` 签名。
- 中英双语文档/spec 同步（见全局 Bilingual Output Requirement）。

### 边界条件

- POST `/api/frame/deploy/prepare` → 403 `write_gate_disabled`（新增）。
- POST `/api/frame/upload` → 403 `write_gate_disabled`（新增）。
- 非 POST 方法（GET/DELETE/PUT）打这两条路径 → 仍走 `methodAllowed(POST)` 失败 → 405（保持现有行为：case 内 `methodAllowed(POST)` 短路）。
- `deploy/init`|`deploy/direct` 行为不变（仍 403 write_gate_disabled）。
- 其余 frame 子路径（frames/track/deploy/complete/contract/*/{slug}）行为不变。
- query string（如 `?beta=true`）由 `r.URL.Path` 剥离，不影响路径匹配。

## 任务详情

### 任务 1：frame.go —— 403 写门 case 扩展 + 注释同步

#### 需求

**Objective（目标）** —— 让 `handleFrameEndpoint` 对 2.1.220 新增的 `POST /api/frame/deploy/prepare` 与 `POST /api/frame/upload` 返回与同族 `deploy/init`|`deploy/direct` 一致的 403 `write_gate_disabled`，替换当前 default 分支的误导性 405，使写门语义正确、客户端降级文案清晰。

**Outcomes（成果）** —— `internal/proxy/frame.go:46` 的 case 条件由 `path == "/api/frame/deploy/init" || path == "/api/frame/deploy/direct"` 扩展 additionally `|| path == "/api/frame/deploy/prepare" || path == "/api/frame/upload"`；case 体（`methodAllowed(POST)` 短路 + 403 `write_gate_disabled` JSON）不变。函数头注释 L14 第 4 条「deploy/init|direct」更新为「deploy/init|direct|prepare 与 frame/upload（写门关闭）」。

**Evidence（证据）** —— `go test ./internal/proxy/ -run TestFrameEndpointCompatibility` 通过：`deploy prepare returns write-gate denied`、`upload returns write-gate denied` 两条新子测试断言 status=403、reason=write_gate_disabled、error 含 unavailable。

**Constraints（约束）** —— 仅扩展 case 条件，不改 case 体、不改响应 schema、不改 `isHardcodedEndpoint`；`methodAllowed(w,r,POST)` 短路保留（非 POST 仍 405）；不引入新 import。

**Edge Cases（边界）** —— GET/DELETE/PUT 打 prepare/upload → case 内 `methodAllowed(POST)` 失败 → 405（保持）；POST prepare/upload → 403 write_gate_disabled（新）；带 query（`?beta=true`）→ `r.URL.Path` 不含 query，匹配不受影响。

**Verification（验证）** —— `go test ./internal/proxy/ -run TestFrameEndpointCompatibility` 全绿；`go vet ./internal/proxy/` 干净。

#### 计划

1. **先写失败测试。** 在 `internal/proxy/frame_test.go` 的 `TestFrameEndpointCompatibility` 内、`deploy direct returns write-gate denied` 子测试之后，新增两个子测试（结构对齐 L57-75 的 `deploy init` 子测试）：
   ```go
   t.Run("deploy prepare returns write-gate denied", func(t *testing.T) {
       req := httptest.NewRequest(http.MethodPost, "/api/frame/deploy/prepare", strings.NewReader(`{"slug":"x","shas":["abc"]}`))
       rec := httptest.NewRecorder()
       handler.handleHardcodedEndpoint(rec, req)
       if rec.Code != http.StatusForbidden {
           t.Fatalf("status = %d, want 403", rec.Code)
       }
       var resp struct {
           Error  string `json:"error"`
           Reason string `json:"reason"`
       }
       json.NewDecoder(rec.Body).Decode(&resp)
       if resp.Reason != "write_gate_disabled" {
           t.Errorf("reason = %q, want write_gate_disabled", resp.Reason)
       }
       if !strings.Contains(resp.Error, "unavailable") {
           t.Errorf("error = %q", resp.Error)
       }
   })

   t.Run("upload returns write-gate denied", func(t *testing.T) {
       req := httptest.NewRequest(http.MethodPost, "/api/frame/upload", strings.NewReader(`{"slug":"x","files":[]}`))
       rec := httptest.NewRecorder()
       handler.handleHardcodedEndpoint(rec, req)
       if rec.Code != http.StatusForbidden {
           t.Fatalf("status = %d, want 403", rec.Code)
       }
       var resp struct {
           Error  string `json:"error"`
           Reason string `json:"reason"`
       }
       json.NewDecoder(rec.Body).Decode(&resp)
       if resp.Reason != "write_gate_disabled" {
           t.Errorf("reason = %q, want write_gate_disabled", resp.Reason)
       }
   })
   ```
2. **确认失败。** `go test ./internal/proxy/ -run TestFrameEndpointCompatibility` → 两条新子测试失败（status=405，want 403）。
3. **最小实现。** `internal/proxy/frame.go`：
   - L46 case 条件改为：
     ```go
     // deploy init/direct/prepare、frame/upload - POST 403，客户端发布路径能识别 write_gate_disabled
     case path == "/api/frame/deploy/init" || path == "/api/frame/deploy/direct" ||
         path == "/api/frame/deploy/prepare" || path == "/api/frame/upload":
     ```
     case 体（`methodAllowed(POST)` 短路 + 403 write_gate_disabled JSON）不变。
   - 函数头注释 L14 第 4 条由 `//  4. POST /api/frame/deploy/init|direct -> 403 write_gate_disabled` 改为：
     ```
     //  4. POST /api/frame/deploy/init|direct|prepare、POST /api/frame/upload -> 403 write_gate_disabled
     ```
4. **确认通过。** `go test ./internal/proxy/ -run TestFrameEndpointCompatibility` → 全部子测试通过（含两条新）。
5. **回归。** `go test ./internal/proxy/`、`go vet ./internal/proxy/`。
6. **提交。** `git add internal/proxy/frame.go internal/proxy/frame_test.go && git commit -m "feat(proxy): frame write-gate covers 2.1.220 prepare/upload endpoints"`。

#### 验证

- [ ] `go test ./internal/proxy/ -run TestFrameEndpointCompatibility` —— 全部子测试通过（含 deploy prepare、upload 两条新）。
- [ ] `go vet ./internal/proxy/` —— 干净。

### 任务 2：全量回归 + 提交收尾

#### 需求

**Objective（目标）** —— 确认 frame 写门扩展不破坏任何既有行为，`make test`（race + 覆盖率）全绿，工作区仅含本任务变更。

**Outcomes（成果）** —— `go test -race ./...` 全包 ok、0 失败；`go vet ./...` 干净；`git status --short` 仅 `internal/proxy/frame.go`、`internal/proxy/frame_test.go`（+ spec 目录）。

**Evidence（证据）** —— 测试输出 15 包全 ok；既有 `deploy init`/`deploy direct` 子测试仍通过（确认 init/direct 行为未变）；既有 default-405 子测试（`wrong method returns 405`）仍通过（确认非 POST 仍 405）。

**Constraints（约束）** —— 不为迁就测试改生产逻辑；只 commit 不 push（见全局 Local Commit Before Push）。

**Edge Cases（边界）** —— 无（任务 1 已覆盖功能边界）。

**Verification（验证）** —— `make test` 等价命令全绿。

#### 计划

1. `go test -race ./...`。
2. `go vet ./...`。
3. `git status --short && git diff --stat` 核对变更范围。
4. 汇总提交；**不 push**，等用户确认。

#### 验证

- [ ] `go test -race ./...` —— 全部包 ok，0 失败。
- [ ] `go vet ./...` —— 干净。
- [ ] `git status` 仅本功能变更；未 push。

### 任务 3（可选）：拦截接口清单 research 文档补注

#### 需求

**Objective（目标）** —— 把 2.1.220 审查结论与本次修复归档，便于下次版本审查有据可查。

**Outcomes（成果）** —— 在 `sdd-docs/research/2026-07-15-intercepted-endpoints.md` 的 C 节（Frame 子端点展开）补一行 deploy/prepare|upload → 403 write_gate_disabled（CC 2.1.220），或在文末加一段「2026-07-26 复审 2.1.220」纪要：仅 2 条新增（均 frame 族，已被前缀覆盖），无新增未拦截泄露端点；本次 PR 把 default-405 收敛为显式 403。

**Constraints（约束）** —— 文档增量，不改代码；与本 spec 语义一致。

**Verification（验证）** —— 文档可读、数量小计自洽。

#### 计划

1. 编辑 `sdd-docs/research/2026-07-15-intercepted-endpoints.md` C 节表格补 2 行 / 或文末加复审纪要段。
2. 提交（可并入任务 2 的提交或单独）。

#### 验证

- [ ] research 文档反映 2.1.220 复审结论与本次收敛。
