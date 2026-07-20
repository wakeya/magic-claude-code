# 日志显示暴露模型 Label 规格说明

**代理入口：** `internal/proxy/handler.go`（请求/响应日志行，约 L144-147、L318）；`internal/proxy/helpers.go`（新增 `formatModelLog`）
**配置入口：** `internal/config/config.go`（`ModelRoute`、`ResolveRoute`，约 L271-307）；`internal/config/provider.go`（`ExposedModel`，约 L297-331）
**参考来源：** `sdd-docs/features/README.md`（单文件 spec 模板）；`sdd-docs/features/2026-07-17-kimi-quota-usage-parsing-fixes/spec.md`（任务详情示例）；`internal/failover/manager.go`（确认 ExposedModel 固定路由不参与故障切换）
**技术栈：** Go 1.26 标准库
**最后更新：** 2026-07-19
**进度：** 2 / 2

## 整体分析（源站分析）

### 现象

用户通过 Claude Code 的 `/model` 菜单选择模型时，MCC 通过 `ExposedModel` 把会话固定路由到某个 provider。每个 `ExposedModel` 带有一个随机 ID，例如 `em-fceb31a8`（由 `generateExposedModelID` 生成，`provider.go:329-331`）。Claude Code 把这个 ID 作为请求的 `model` 字段发回，MCC 原样打印到日志：

```
2026/07/19 20:31:58 [a9e7c8a5] >>> POST api.anthropic.com/v1/messages model=em-fceb31a8 -> k3 stream=true ...
```

左边的 `em-fceb31a8` 对运维者完全不透明。而用户真正配置的人类可读名称——`ExposedModel.Label`——已经存储，却从不出现在日志里。

### `em-` ID 的设计意图

根据 `provider.go:326-328`，ID **故意**设计成无语义的随机 hex：

> ID 纯内部用（Claude Code /model 菜单 value + mcc 路由键），用户无需感知，故用 em- 前缀 + 随机 hex，无语义、稳定（生成后写回 struct，不随重排变化）。

它必须全局唯一且在 provider 重排时保持稳定，因此不能复用用户可编辑的 `Label`。把它原样打到日志，等于泄露了设计上明确要求「用户无需感知」的内部键。

### 为什么 Label 当前拿不到

`ResolveRoute`（`config.go:284-307`）把请求的 `model` 解析为 `ModelRoute{Provider, BackendModel, DefaultRouted}`。命中 `ExposedModel` 时返回 provider 和 `em.BackendModel`，但**丢掉了 `em.Label`**。日志输出点（`handler.go:144`）只有 `metadata.OriginalModel`（原始的 `em-` ID）。

### 影响面

`grep` 确认 `ModelRoute` / `ResolveRoute` 只在 `internal/proxy/handler.go`（L114、L116、L401、L421）被消费。新增字段是安全的——现有读取（`Provider`、`BackendModel`、`DefaultRouted`）不受影响；新字段纯属附加。

### 故障切换路径不受影响

`ExposedModel` 命中是固定路由（`DefaultRouted=false`）；`shouldFailover` / `shouldFailoverOnError`（`handler.go:401-421`）在 `!route.DefaultRouted` 时直接短路。因此故障切换重放日志（`handler.go:468`，`model=%s->%s`）永远不会出现 `em-` ID，无需改动。

### 决策（显示格式）

**纯 Label（方案 A）。** 当请求命中 `ExposedModel` 时，日志左边显示 `em.Label`，而非原始的 `em-` ID。

| 方案 | 示例（Label="Kimi K3"） | 是否采用 |
| --- | --- | --- |
| A. 纯 Label | `model=Kimi K3 -> k3` | ✅ |
| B. Label(em-id) | `model=Kimi K3(em-fceb31a8) -> k3` | — |
| C. em-id(Label) | `model=em-fceb31a8(Kimi K3) -> k3` | — |

理由：`em-` ID 几乎没有诊断价值——关联排查用 `provider_name` + `upstream_url` 更准，这两项日志已有。方案 A 最直观。用户已于 2026-07-19 确认此选择。

### 不改动的部分

- **请求体 / 路由匹配：** `metadata.OriginalModel` 和 `ResolveRoute` 匹配仍使用原始 `em-` ID。只有**日志展示层**替换为 Label。
- **`->` 右侧：** `mappedModel`（即 `BackendModel`，如 `k3`）本身就是人类可读的后端模型名，无需再映射。
- **默认路由日志：** 回退到 active provider 的请求（如 `model=claude-opus-4-8 -> glm-5.2`）没有 `ExposedModel`、也没有 Label，保持原样。
- **故障切换重放日志**（`handler.go:468`）：不变（ExposedModel 固定路由永不进 failover）。
- **`[1m]` 后缀：** `Context1M` 菜单 value 带 `[1m]` 供 Claude Code 判定上下文窗口，但到达 MCC 的请求 `model` 已剥离该后缀。`ResolveRoute` 用纯 ID 匹配；返回的 Label 本身不含 `[1m]`。无需特殊处理。

## 开发检查清单

| 序号 | 状态 | 任务 | 产出 | 验证 |
| --- | --- | --- | --- | --- |
| 1 | ✅ | `ResolveRoute` 通过新增 `ModelRoute.ExposedLabel` 字段透出 `ExposedModel.Label` | `internal/config/config.go`；`internal/config/failover_test.go` | `go test ./internal/config/` — 扩展后的路由测试断言 Label |
| 2 | ✅ | 代理请求/响应日志显示 Label 而非 `em-` ID（提取 `formatModelLog` 保证可测） | `internal/proxy/helpers.go`；`internal/proxy/handler.go`；`internal/proxy/helpers_test.go` | `go test ./internal/proxy/` 与 `go test ./...` 全绿 |

## 需求

### 交付物

1. `config.ModelRoute` 新增字段 `ExposedLabel string`。当且仅当 `ResolveRoute` 命中 `ExposedModel` 时非空；active provider 回退和无 active 两种情况为空。
2. `Config.ResolveRoute` 在 ExposedModel 命中的返回路径（`config.go:297`）设置 `ExposedLabel: em.Label`。另外两个返回路径（active provider、无 active）保持零值。
3. `internal/proxy/helpers.go` 新增 `formatModelLog(originalModel, mappedModel, exposedLabel string) string`，封装日志 model 字段构造：`exposedLabel` 非空时优先用它，否则用 `originalModel`；当展示模型等于 `mappedModel` 时折叠为单 token（保留今天未映射模型的行为）；否则输出 `"<display> -> <mappedModel>"`。
4. `handler.go`（约 L144-147）将内联的 `modelStr` 构造替换为单次调用 `modelStr := formatModelLog(metadata.OriginalModel, mappedModel, route.ExposedLabel)`。入口日志（约 L159）和出口日志（约 L318）共用同一个 `modelStr`，一次改动同时覆盖。
5. 不改动请求体、路由匹配、`usage.RequestRecord`、故障切换逻辑以及故障切换重放日志行（`handler.go:468`）。
6. `Provider.Validate` 已强制 `Label` 非空（`provider.go:218`），因此 `formatModelLog` 内的 `exposedLabel != ""` 判断已足够，无需额外校验。

### 数据模型

```go
// internal/config/config.go
type ModelRoute struct {
    Provider      *Provider
    BackendModel  string
    DefaultRouted bool
    ExposedLabel  string  // 新增：当且仅当命中 ExposedModel 时非空；仅供日志展示层使用
}
```

## 任务详情

### 任务 1：ResolveRoute 透出 ExposedModel.Label

#### 需求

**Objective（目标）** — 让 `ResolveRoute` 把 `ExposedModel.Label` 带出来，使代理日志层能显示用户配置的人类可读名称，而非不透明的 `em-<hex>` ID，且不改变路由或请求体。

**Outcomes（成果）** — `internal/config/config.go` 在 `ModelRoute`（约 L271-275）新增字段 `ExposedLabel string`，并在 `ResolveRoute`（约 L297）的 ExposedModel 命中返回里设置 `ExposedLabel: em.Label`；active 回退和无 active 两个返回保持零值。`internal/config/failover_test.go` 给已有的四个 `ResolveRoute` 测试补上 `ExposedLabel` 断言。

**Evidence（证据）** — `go test ./internal/config/` 通过扩展后的断言；命中 `ExposedModel{ID:"em-deadbeef", Label:"Kimi K3", BackendModel:"k3"}` 时 `r.ExposedLabel == "Kimi K3"`，而 active 回退、disabled-exposed 回退、无 active 三种情况均 `r.ExposedLabel == ""`。

**Constraints（约束）** — `ModelRoute` 只在 `internal/proxy/handler.go` 被消费（grep 确认：L114、L116、L401、L421）；新字段为附加，不影响现有读取。路由匹配仍用原始 `em-` ID，只是返回结构体多了一个仅用于展示的字段。`ResolveModel`（向后兼容包装）不变，因为它只转发 `r.Provider` 和 `r.BackendModel`。

**Edge Cases（边界）** — active provider 回退（`DefaultRouted=true`）→ `ExposedLabel == ""`；disabled provider 的暴露模型被跳过 → 回退到 active → `ExposedLabel == ""`；无 active provider（`Provider == nil`）→ `ExposedLabel == ""`；`Context1M` 模型（请求 `model` 已剥离 `[1m]`）按纯 ID 命中并返回 Label（Label 本身不含 `[1m]`）。

**Verification（验证）** — `go test ./internal/config/` 全绿；四个扩展测试同时断言正向（命中返回 Label）和负向（三条回退路径返回空）契约。

#### 计划

1. **先写失败测试。** 在 `internal/config/failover_test.go` 给四个已有测试补 `ExposedLabel` 断言（不改路由 setup）：
   - `TestResolveRouteExposedModelIsNotDefaultRouted`（用 `ExposedModel{ID:"glm-4.6", Label:"GLM-4.6", ...}`）— 加：`if r.ExposedLabel != "GLM-4.6" { t.Fatalf("expected exposed label GLM-4.6, got %q", r.ExposedLabel) }`。
   - `TestResolveRouteActiveFallbackIsDefaultRouted` — 加：`if r.ExposedLabel != "" { t.Fatalf("active fallback must have empty ExposedLabel, got %q", r.ExposedLabel) }`。
   - `TestResolveRouteSkipsDisabledExposedModel` — 加：`if r.ExposedLabel != "" { t.Fatalf("disabled-exposed fallback must have empty ExposedLabel, got %q", r.ExposedLabel) }`。
   - `TestResolveRouteWithoutActiveProvider` — 加：`if r.ExposedLabel != "" { t.Fatalf("no-active route must have empty ExposedLabel, got %q", r.ExposedLabel) }`。
2. **确认失败。** `go test ./internal/config/ -run TestResolveRoute` → 编译报错（`r.ExposedLabel undefined`）。
3. **最小实现。** 在 `internal/config/config.go`：
   - 给 `ModelRoute`（约 L274）加字段 `ExposedLabel string`，文档注释 `// 命中 ExposedModel 时为 em.Label，其余为空；仅供日志展示层使用，不参与路由。`。
   - 在 `ResolveRoute`（约 L297）把命中返回改为：`return ModelRoute{Provider: p, BackendModel: em.BackendModel, DefaultRouted: false, ExposedLabel: em.Label}`。两个回退返回保持不变（零值）。
4. **确认通过。** `go test ./internal/config/ -run TestResolveRoute` → 四个全过。
5. **回归。** `go test ./internal/config/` 与 `go vet ./internal/config/`。
6. **提交。** `git add internal/config/config.go internal/config/failover_test.go && git commit -m "feat(config): ResolveRoute surfaces ExposedModel.Label for log display"`。

#### 验证

- [x] `go test ./internal/config/ -run TestResolveRoute` — 4/4 通过。
- [x] `go test ./internal/config/` — 108 项通过；`go vet ./internal/config/` 干净。
- [x] grep 确认 `ModelRoute` 仍只在 `internal/proxy/handler.go` 被消费，无其他读取方受影响。

### 任务 2：代理请求/响应日志显示 Label 而非 em- ID

#### 需求

**Objective（目标）** — 让 `>>>` 入口日志和 `<<<` 出口日志在请求命中暴露模型时，于 `model=` 左侧显示 `ExposedModel.Label`，替换不透明的 `em-<hex>` ID；保留 `-> <BackendModel>` 映射结构，并保留未映射模型时折叠为单 token 的既有行为。

**Outcomes（成果）** — `internal/proxy/helpers.go` 新增 `formatModelLog(originalModel, mappedModel, exposedLabel string) string`（引入 `fmt`）；`internal/proxy/handler.go`（约 L144-147）把内联 `modelStr` 块替换为单次调用 `modelStr := formatModelLog(metadata.OriginalModel, mappedModel, route.ExposedLabel)`；新增 `internal/proxy/helpers_test.go`，加表驱动 `TestFormatModelLog` 覆盖 Label 替换、Label 等于后端时折叠、默认路由、未映射折叠、空 Label 回退。

**Evidence（证据）** — `go test ./internal/proxy/` 通过；`TestFormatModelLog` 断言 `formatModelLog("em-fceb31a8", "k3", "Kimi K3") == "Kimi K3 -> k3"` 与 `formatModelLog("em-deadbeef", "k3", "k3") == "k3"`（折叠）。全套 `go test ./...` 全绿。

**Constraints（约束）** — 只改日志展示层；`metadata.OriginalModel`、请求体、路由、`usage.RequestRecord`、故障切换重放日志（`handler.go:468`）均不动。入口（约 L159）与出口（约 L318）日志已共用 `modelStr`，一次编辑同时覆盖。`formatModelLog` 为纯函数（无 I/O），保证可单测。

**Edge Cases（边界）** — `exposedLabel == ""` → 回退到 `originalModel`（默认路由，如 `claude-opus-4-8 -> glm-5.2`）；`exposedLabel == mappedModel` → 折叠为单 token（如 Label `k3`、后端 `k3` → `model=k3`）；`originalModel == mappedModel` 且 Label 为空 → 折叠（未映射默认，如 `model=claude-opus-4-8`）；`Context1M` 请求 — `route.ExposedLabel` 携带的是纯 Label（无 `[1m]`），日志显示干净的 Label。

**Verification（验证）** — `go test ./internal/proxy/` 全绿（新增 `TestFormatModelLog`）；`go test ./...` 全套全绿；`go vet ./...` 干净。

#### 计划

1. **先写失败测试。** 新建 `internal/proxy/helpers_test.go`，加表驱动 `TestFormatModelLog`：
   ```go
   package proxy

   import "testing"

   func TestFormatModelLog(t *testing.T) {
       tests := []struct {
           name          string
           originalModel string
           mappedModel   string
           exposedLabel  string
           want          string
       }{
           {"exposed label replaces em id", "em-fceb31a8", "k3", "Kimi K3", "Kimi K3 -> k3"},
           {"label equals backend collapses to single token", "em-deadbeef", "k3", "k3", "k3"},
           {"default route keeps original with mapping", "claude-opus-4-8", "glm-5.2", "", "claude-opus-4-8 -> glm-5.2"},
           {"unmapped default collapses", "claude-opus-4-8", "claude-opus-4-8", "", "claude-opus-4-8"},
           {"empty label falls back to original", "em-x", "k3", "", "em-x -> k3"},
       }
       for _, tt := range tests {
           t.Run(tt.name, func(t *testing.T) {
               if got := formatModelLog(tt.originalModel, tt.mappedModel, tt.exposedLabel); got != tt.want {
                   t.Fatalf("formatModelLog(%q, %q, %q) = %q, want %q",
                       tt.originalModel, tt.mappedModel, tt.exposedLabel, got, tt.want)
               }
           })
       }
   }
   ```
2. **确认失败。** `go test ./internal/proxy/ -run TestFormatModelLog` → 编译报错（`formatModelLog undefined`）。
3. **最小实现。** 在 `internal/proxy/helpers.go` 的 import 块加 `"fmt"`，并追加：
   ```go
   // formatModelLog 构造请求/响应日志里的 model 字段。
   // 命中 ExposedModel 时优先用人类可读的 Label 替代原始 em-<hex> ID（仅影响日志展示，
   // 不改路由/请求体）；mappedModel 与展示模型相同时折叠为单 token，
   // 与未做模型映射时的行为一致。
   func formatModelLog(originalModel, mappedModel, exposedLabel string) string {
       display := originalModel
       if exposedLabel != "" {
           display = exposedLabel
       }
       if mappedModel == display {
           return display
       }
       return fmt.Sprintf("%s -> %s", display, mappedModel)
   }
   ```
4. **确认通过。** `go test ./internal/proxy/ -run TestFormatModelLog` → 5/5 子测试通过。
5. **接入 handler。** 在 `internal/proxy/handler.go` 把内联块（约 L144-147）：
   ```go
   // 改前
   modelStr := metadata.OriginalModel
   if mappedModel != metadata.OriginalModel {
       modelStr = fmt.Sprintf("%s -> %s", metadata.OriginalModel, mappedModel)
   }
   ```
   替换为：
   ```go
   modelStr := formatModelLog(metadata.OriginalModel, mappedModel, route.ExposedLabel)
   ```
   （入口日志约 L159、出口日志约 L318 已消费 `modelStr`，两者自动更新。`handler.go` 的 `fmt` 可能变为未使用——若如此，从 import 块移除；构建会报错提示。）
6. **回归。** `go test ./internal/proxy/`，再 `go test ./...` 与 `go vet ./...`。
7. **提交。** `git add internal/proxy/helpers.go internal/proxy/helpers_test.go internal/proxy/handler.go && git commit -m "feat(proxy): log shows ExposedModel.Label instead of em- id"`。

#### 验证

- [x] `go test ./internal/proxy/ -run TestFormatModelLog` — 5/5 子测试通过。
- [x] `go test ./...` — 1684 项通过（0 失败）；`go vet ./...` 干净。
- [x] 人工验证（2026-07-19）：ExposedModel Label=`kimi-k3`、BackendModel=`k3`，入口 `>>>` 与出口 `<<<` 日志均显示 `model=kimi-k3 -> k3`（em- ID 已被 Label 替换）。
