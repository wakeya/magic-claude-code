# Desktop Endpoint Patch 审查记录

日期：2026-06-20  
审查人：Codex 和 Claude Code

## 范围

本文记录 desktop endpoint patch 及其后续加固修改的最终审查结论。

## 关键发现与处理

1. `count_tokens` 之前读取请求体时没有大小限制。
   - 处理：`handleCountTokens()` 现在使用 `io.LimitReader(..., maxRequestBodySize+1)`，超限时返回 `413 Request Entity Too Large`。

2. TLS 监听器握手处理之前存在 listener starvation 和关闭边界问题。
   - 处理：握手改为异步执行，增加超时和并发上限，`Close()` 会等待在途握手结束后再 drain 已排队连接。

3. 测试日志之前依赖全局 logger，在未来并行化时容易产生竞态噪声。
   - 处理：测试改为注入局部 `log.Logger`，并使用线程安全 buffer 收集日志。

4. `newTLSListener()` 未来若误传 `nil logger`，会有 panic 风险。
   - 处理：`nil` 现在回退到 `log.Default()`。

5. `LoadServerCert()` 写入和读取的证书形态不一致。
   - 处理：已补充注释，明确它只返回 `server.crt` 的首个 PEM block；生产 TLS 启动使用 `tls.LoadX509KeyPair()`。

## 最终结论

未发现残留的逻辑缺陷或安全缺陷。

最终验证通过：

```bash
go test ./... -race
```

## 残余说明

- `LoadServerCert()` 设计上只返回 `server.crt` 中的叶子证书 DER。
- 这是已明确的语义，不影响当前 TLS 启动路径。

## 一致性检查

- `sdd-docs/features/README.md` 已补充说明 `review-notes.md` 和 `review-notes_ZH.md` 是 feature 级归档记录。
- 该 feature 下的 `review-notes.md` 和 `review-notes_ZH.md` 已成对存在。
- Review skills 索引已同步到 `~/.codex/skills/` 和 `~/.claude/skills/`。

## 最终格式检查

- `git diff --check` 通过，没有格式问题。
