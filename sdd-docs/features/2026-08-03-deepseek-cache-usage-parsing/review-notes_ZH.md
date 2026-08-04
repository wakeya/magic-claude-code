# DeepSeek 缓存 Usage 解析审查记录

日期：2026-08-04  
审查者：Codex 协调者与 pi 安全审查 worker

## 审查范围

审查分支 `fix/deepseek-cache-usage-parsing` 的 commit `05a507f`，覆盖共享 usage 归一化逻辑，以及 Chat Completions / Responses 的非流式和 SSE 调用路径。

## 关键发现与结论

1. **低风险逻辑问题——负零绕过校验。** `usageNumber` 只判断 `number < 0`，但 IEEE-754 中 `-0.0 < 0` 为 false。若上游返回 `cached_tokens: -0.0`，该值会被视为显式存在的零，阻止回退到 `prompt_cache_hit_tokens`，导致缓存读取统计可能偏低。结论：建议增加 `math.Signbit(number)` 校验，并补充 `-0.0` 回归测试，再视为完全加固。
2. **信息性语义风险——Responses 输入计算是有意变更。** 新逻辑从 `input_tokens` 中扣除缓存读取和缓存写入 token。对于缓存 token 已包含在总输入中的 OpenAI Responses 语义，这是正确的；若未来某个不规范供应商把缓存 token 作为总输入之外的附加值返回，则会出现输入统计偏低。结论：当前范围内保留；若接入此类供应商，应增加按供应商区分的语义配置。
3. **未发现高风险安全缺陷。** 未发现本次变更引入可达的 panic、无界分配、命令/路径/网络危险 sink、注入、授权绕过或数值溢出。上游 usage 数值本来就由后端决定，本 commit 没有扩大这一信任边界。

## 最终审查结论

该分支在声明的 DeepSeek/OpenAI usage 解析范围内逻辑基本正确，未发现可利用的安全漏洞。如果要求缓存统计绝对精确，应先补充负零的一行加固；当前问题属于低风险统计健壮性问题，不构成安全阻断。

## 残余说明

- 已验证 `go test ./...`、`go vet ./internal/proxy/transform` 和 `git diff --check`。
- 协议转换导致的真实缓存下降仍不在本分支范围内，已由独立的 protocol-cache-prefix-stability spec 跟踪。
