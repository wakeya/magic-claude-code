# CC 2.1.211 端点兼容审查笔记

日期：2026-07-16
审查者：Claude Code（静态分析 + 从真实混淆客户端 `claude_code_src_2.1.211.js` 独立重新
推导契约，并经第二遍读取交叉核对）
审查类型：对 glm-5.2 实现的安全 + 功能审查
审查提交范围：`67c783b`（代码）+ `fa8ae42` / `f0c94b8`（文档）

## 范围

本次变更拦截 Claude Code 2.1.211 新增的三类端点，在本地伪造响应，避免客户端抛异常
或落入 fail-closed 的 404（`mcc_blocked_unknown_endpoint`）：

| 端点 | 代码位置 | 响应 |
|------|----------|------|
| `GET /v1/design/grants` | `hardcoded.go:317` | `200 {"grants":[]}` |
| `POST /v1/design/grants` | `hardcoded.go:321` | `403 {"reason":"write_gate_disabled"}` |
| `GET /v1/ultrareview/preflight` | `hardcoded.go:345` → `handleEmptyResponse` | `200 {}` |
| `GET /v1/code/triggers[/{id}][/run]` | `hardcoded.go:302` → `triggers.go` | `200 {"data":[]}` |
| `POST /v1/code/triggers[/{id}]` | `triggers.go:22` | `403 {"reason":"write_gate_disabled"}` |

变更文件：`internal/proxy/hardcoded.go`、`internal/proxy/triggers.go`、
`internal/proxy/hardcoded_test.go`、`internal/proxy/triggers_test.go`，外加 spec
双语文档与基线研究文档。`endpoint_policy.go`（`modelForwardPaths`）未改动——已核实。

## 已执行的验证

1. 从真实混淆源码独立重新推导每个客户端契约（两遍独立读取：直接 grep + 一次彻底
   清扫，不盲信 spec）：
   - `GET /v1/design/grants`——客户端读 `e.data?.grants`，要求 `Array.isArray`
     （否则 `probe_shape`），404 发 `probe_404_old_server` 遥测；返回 null/非数组时
     降级到 per-batch plan flow。`200 {"grants":[]}` → 空 Set → Design 授权禁用、无
     404 噪声。**一致。**
   - `POST /v1/design/grants`——`validateStatus:(n)=>n<300||n===404`，`!r.ok→throw`。
     403 落入 `!r.ok` → 写入失败关闭。**一致。** 客户端对 404 与 403 功能等价（都
     throw）；实现选 403，语义是「被策略门阻断」，而非 404 的「项目被拉黑、无法持有
     持久授权」——对 MCC 本地模式禁用写入而言，403 是正确语义。
   - `GET /v1/ultrareview/preflight`——客户端用 zod schema 校验响应体
     （`action: enum["proceed","confirm","blocked"]`，可选 `billing_note`/`confirm`/
     `blocked`）。`!t.ok` 时按 `t.reason` 分支；200 但 body 未过 `safeParse` 时警告
     `fetchUltrareviewPreflight schema mismatch` + 遥测 `schema_mismatch` → 返回
     `null`；门控 `kDo()` 把 `null` 视为 `proceed`（放行）。因此 `200 {}` 能工作——
     但走的是「畸形数据 ⇒ 放行」路径，会多发一条 `schema_mismatch` 遥测，而非匹配到
     真正的 `proceed`。「不阻断」的目标仍达成。详见残留说明。
   - `GET /v1/code/triggers`——`validateStatus:()=>!0`（从不因 HTTP 状态抛错），
     `e.data.data??[]`，`!e.ok→throw "triggers unavailable"`。`200 {"data":[]}` →
     `u.ok` true、空数组、不抛错。POST 403 → `!u.ok` → throw "Remote triggers
     unavailable"。**一致。** triggers 是一个工具，action 含 list/get/create/update/run，
     全部落在 `/v1/code/triggers[/{id}][/run]`——前缀拦截覆盖所有变体。
2. fail-closed 防护：写了临时测试，断言 5 条路径 × GET/POST/PUT/DELETE 都不会被
   分类为 `endpointActionForwardModel`。通过（临时测试已删除）。
3. `go test ./internal/proxy/ -race -count=1`——全部通过，无数据竞争。
4. 工作区干净，无残留文件。

## 关键发现与结论

1. 三个端点处理器均无逻辑缺陷。
   - 响应结构、状态码、`write_gate_disabled` 原因都与真实客户端处理一致。405 分支
     复用共享的 `methodAllowed`（Allow 头 + JSON body），与 spec Plan 中手写形式等价
     且更一致。
2. 无安全缺陷。
   - 三个端点都非模型端点，`classifyForwardingEndpoint` 阻止其进入转发白名单（已用
     测试验证）。请求体不解析（经 `drainRequestBodyLimited` 有界丢弃，上限 1MB，无
     DoS）。这些路径不记录任何认证/请求头/query。没有新的上游转发，因此没有引入
     数据泄露面。
3. `GET /v1/code/triggers/{id}`（单条）返回列表形态 `{data:[]}`。
   - 结论：语义不完美，但客户端容忍（`data.data??[]`），第三方用户不用 CCR 触发器，
     且 spec 风险小结 #2 已明确接受。可接受。

## 最终审查结论

glm-5.2 对 CC 2.1.211 端点兼容的实现是正确且安全的。三类端点足够忠实地复现了客户端
契约，达成了预期的客户端行为（不抛异常、不产生误阻断），fail-closed 转发防护保持完好，
测试（含 `-race`）通过。无遗留逻辑或安全缺陷。

## 残留说明

- **preflight 的 `{}` 是经 `schema_mismatch` 走到「放行」——但在第三方场景下，响应体
  根本不会被读取。**
  已对源码复核：preflight 经由 `kDo(){ let e=await Hpp(); if(!e) return {kind:"proceed"}
  ... }` 触发，`Hpp` 以 `auth:"teleport-org"` 发起 `GET /v1/ultrareview/preflight`。当没有
  claude.ai OAuth token（第三方 provider 场景恒如此）时，客户端解析出
  `t.reason==="no-auth"` → `!t.ok` → **自行**返回 blocked（「Ultrareview requires a
  Claude.ai account」），根本走不到解析响应体。因此无论 MCC 返回什么 body，ultrareview 在
  目标场景下本就「不可用」；`{}` 与 `{"action":"proceed"}` 在此是个无差别命题（两者都是
  永远到不了的 200 body）。
  - 因此保持 `{}` 是对的——但理由是 YAGNI／最小实现，而非「更保守」。`{}` 语义上其实是
    「经 schema_mismatch 放行」（会多发一条 `api_ultrareview_preflight / schema_mismatch`
    遥测），只是该路径在 `no-auth` 下不可达。改成 `{"action":"proceed"}` 既不会激活
    ultrareview（teleport-org 认证仍会挡下），也无实际价值。不建议改；对一个在目标场景
    下 body 从不被读取的端点，没必要精心构造响应体。
  - **对一条复查意见的更正**：有复查断言 `fetchUltrareviewPreflight`（`Hpp`）是无调用者的
    dead code。该判断**有误**——`kDo` 调用了 `Hpp`，而 `kDo` 又被两个真实 ultrareview 入口
    （`runUltrareviewHeadless` 与交互流程）调用。朴素 `grep` 词边界匹配会漏掉这个调用，
    因为混淆后的调用点书写紧凑（`await Hpp()` 无空格）。「preflight 从不被请求」的结论
    不成立；该端点**确实可达**，只是在第三方场景被 `no-auth` 提前短路（如上所述）。
    客户端另支持 `CLAUDE_CODE_ULTRAREVIEW_PREFLIGHT_FIXTURE` 环境变量在本地短路 preflight。
- **前缀 `/v1/code/triggers` 无尾斜线**（相邻的 `/v1/code/sessions/` 等有），因此
  `/v1/code/triggersXXX` 也会被拦截。已核实真实 2.1.211 客户端无此相邻路径（仅
  `triggers`、`triggers/{id}`、`triggers/{id}/run`），故属健壮性/风格提示而非缺陷。
  若未来客户端新增如 `/v1/code/triggersettings`，应收紧为
  `path == "/v1/code/triggers" || strings.HasPrefix(path, "/v1/code/triggers/")`。
- spec 的「8 个端点」表述：实际只有 3 个需要新增响应（Group B）；其余 5 个（Group A：
  memory 链、github import-token、filestore readFile）正确地保持 fail-closed 404——
  已确认它们不在 `isHardcodedEndpoint` 中，因此 memory 链未被过度激活（遵守 spec 风险
  小结 #1）。
- `design/grants` 需要带 `user:design:read` scope 的 OAuth token，且走的是独立的
  `auth:"none" + Bearer` 路径（不同于 preflight/triggers 用的 `auth:"teleport-org"`）。
  第三方 provider 场景永远拿不到该 token，故此路径基本是防御性的。保留无害。
