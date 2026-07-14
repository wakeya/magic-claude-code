# Provider Quota SQLite BUSY Flaky 审查记录

日期：2026-07-14  
审查人：Codex and Claude Code

## 范围

审查提交 `d54dd74` 中 providerquota SQLite BUSY flaky 的修复：测试 SQLite DSN/连接池与生产对齐、scheduler goroutine 追踪、FK 测试数据修复，以及新增的并发读写回归测试。

## 关键发现与处理

1. 未发现阻塞性的逻辑缺陷或安全缺陷。
   - 处理：测试 DSN 已使用与生产一致的 SQLite pragma 和连接池上限；scheduler 测试会在 DB cleanup 前等待 `scanAndQuery` goroutine 收尾；FK fixture 补齐后，jitter 测试中的 snapshot 持久化路径可以真实执行。

2. 维护备注：`TestSnapshotStoreConcurrentReadWriteNoBusy` 只统计 `locked`/`busy` 错误，会忽略其他非预期 store 错误。
   - 处理：本次可接受，因为该测试目标是 BUSY 回归守护，正常持久化路径已有既有 store 测试覆盖。后续如果再修改该测试，建议记录首个非 BUSY 错误，并在 `wg.Wait()` 后失败，避免无关存储回归被静默放过。**【2026-07-14 已实施】** —— 测试现通过 mutex 保护的 `firstErr` 记录首个非 BUSY store 错误，并在 `wg.Wait()` 后若存在则失败；BUSY 计数逻辑不变。已重新验证 `-race -count=5` 通过（正常 WAL 路径下 firstErr 保持 nil）。

## 最终审查结论

GLM-5.2 的修复可以接受。根因修复与生产 SQLite 行为一致，scheduler 测试清理仅影响测试等待方式且不改变 `Stop()` 语义，本问题范围内没有剩余逻辑或安全缺陷。唯一一条后续维护项（回归测试捕获非 BUSY 错误）现已实施并验证。

## 遗留备注

- 已验证 `go test -race ./internal/providerquota -run 'TestSnapshotStoreConcurrentReadWriteNoBusy|TestScheduler(AppliesJitter|PeriodicScanNoJitter)' -count=5 -v`。
- 已验证 `go test -race ./internal/providerquota/... -count=20`。
- 已验证 `go vet ./...`。
- 已验证 `go test ./...`。
- 后续（2026-07-14）：为 `TestSnapshotStoreConcurrentReadWriteNoBusy` 增加非 BUSY 错误捕获；已重新验证 `go test -race ./internal/providerquota -run TestSnapshotStoreConcurrentReadWriteNoBusy -count=5 -v`。
