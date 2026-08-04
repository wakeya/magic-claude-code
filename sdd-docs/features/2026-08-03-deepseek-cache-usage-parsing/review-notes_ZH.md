# DeepSeek 缓存 Usage 解析审查记录

日期：2026-08-04
审查者：Codex 协调者与 pi 安全审查 worker

## 审查范围

- 第一轮审查分支 `fix/deepseek-cache-usage-parsing` 的 commit `05a507f`，覆盖共享 usage 归一化逻辑，以及 Chat Completions / Responses 的非流式和 SSE 调用路径。
- 第二轮（本文档修订）以只读方式在分支当前 HEAD `f896a53`（实现 `05a507f` + 负零修复 `be8ed50` + 文档 `f896a53`）上相对 `main` 复审，覆盖相同转换路径以及下游 `internal/usage` 的 int64/数据库边界。

## 关键发现与结论

### 第一轮（2026-08-04）

1. **F-01——已修复的低风险逻辑问题：负零绕过校验。** `usageNumber` 原先只判断 `number < 0`，但 IEEE-754 中 `-0.0 < 0` 为 false。commit `be8ed50` 已增加 `math.Signbit(number)` 校验和运行时负零回归测试，因此 `cached_tokens: -0.0` 不再阻止回退到 `prompt_cache_hit_tokens`。
2. **F-02——信息性语义风险：输入计算假设 total 已包含缓存 token。** 新逻辑从总输入中扣除缓存读取和缓存写入 token。对于缓存 token 已包含在总输入中的 OpenAI/DeepSeek 语义，这是正确的；若某个不规范供应商把缓存 token 作为总输入之外的附加值返回，则会出现输入统计偏低。结论：当前范围内保留；若接入此类供应商，应增加按供应商区分的语义配置。（精确表述见第二轮。）
3. **未发现高风险安全缺陷。** 未发现本次变更引入可达的 panic、无界分配、命令/路径/网络危险 sink、注入、授权绕过或数值溢出。上游 usage 数值本来就由后端决定，本 commit 没有扩大这一信任边界。

### 第二轮（2026-08-04，HEAD f896a53）

1. **未发现新缺陷。** 在 HEAD `f896a53` 上复审未发现新的高、中、低级功能逻辑缺陷，也未发现可利用的安全漏洞。已用针对真实代码的对抗性探针验证（JSON/字符串/json.Number/float32 负零、NaN/Inf/1e300、分数值、全部数值类型、hit/miss/write/total 矛盾组合、nil/垃圾字段、SSE 与非流式一致性）：全部收敛为钳制到零的非负输出，无 panic。
2. **F-01——确认由 `be8ed50` 完整关闭。** 所有可达的运行时负零形式均被拒绝：`encoding/json` 解码的 JSON `-0`/`-0.0`、经 `strconv.ParseFloat` 的字符串 `"-0"`/`"-0.0"`、`json.Number("-0")`/`"-0e0"`，以及运行时 float32 负零（经 float64 转换符号保留）。无误伤：显式 `+0`（JSON `0`、字符串 `"0"`/`"+0"`）作为显式零保留并正确阻止回退。注意：`math.Signbit` 对负零与负 NaN 均返回 true；负 NaN 已由相邻的 `math.IsNaN` 检查排除，因此不存在合法值被误判的可能。
3. **F-02——精确表述：适用于所有 endpoint。** `total - cache_read - cache_creation` 的算术假设总输入已包含缓存 token。若非标准供应商把缓存 token 报在 total 之外，Chat Completions 与 Responses 两条转换路径都可能低估未缓存 input：推导出的未缓存值变成 `total - hit - write` 而非真实的 miss 计数，且可能在真实未缓存输入非零时被钳制为零。DeepSeek 与 OpenAI 当前冻结的语义均为 total 包含缓存 token，因此该风险仍属信息性、不阻断本分支；若接入不符合该语义的后端，则需要按供应商区分配置。
4. **信息性健壮性观察——巨大有限值与分数 token。** transform 会把巨大有限数（如 `1e300`）和分数 token 值（如 `600.7`）透传到客户端 JSON。下游 int64 边界（`internal/usage` 的 `usageFieldInt64`）会拒绝超范围值并截断分数，因此这属于既有容忍策略——记录可能丢弃该字段或记录截断后的计数，但不会造成可利用的数据库溢出或负数回绕。本分支无需改动。
5. **低优先级测试缺口。** 现有测试未覆盖：Responses 的 `input_tokens_details.cached_tokens` 回退路径、Chat 顶层 `cache_read_input_tokens` 遗留路径，以及显式零缓存场景下 SSE 与非流式输出的一致性。属于可选后续项，不阻断。
6. **未运行 race 的原因。** 未执行 `go test -race`，因为本次变更不引入共享并发状态：归一化逻辑是纯按请求的 map 处理，两条 SSE 转换路径均为单 goroutine。

## 最终审查结论

该分支在声明的 DeepSeek/OpenAI usage 解析范围内逻辑正确，未发现可利用的安全漏洞。负零统计健壮性问题（F-01）已由 `be8ed50` 完整关闭；total 包含缓存 token 的假设（F-02）同时适用于 Chat 与 Responses，在 DeepSeek/OpenAI 当前语义下属信息性风险；其余健壮性观察与测试缺口为信息性或低优先级。

## 残余说明

- 第二轮验证台账（全部通过）：
  - `go test ./internal/proxy/transform -count=1` → ok
  - `go test ./...` → ok（15 个包，0 FAIL）
  - `go vet ./internal/proxy/transform` → 干净
  - 关键 usage/缓存用例 `go test ./internal/proxy/transform -run 'PromptCache|CachedTokens|CacheTokens|ExplicitZero|UncachedUsage' -count=20` → ok
  - `git diff main..HEAD --check` → 干净
- 协议转换导致的真实缓存下降仍不在本分支范围内，已由独立的 protocol-cache-prefix-stability spec 跟踪。
