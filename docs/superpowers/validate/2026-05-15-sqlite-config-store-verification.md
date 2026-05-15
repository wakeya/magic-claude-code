# SQLite 配置存储改造验证清单

**版本**: 0.1
**日期**: 2026-05-15
**Spec**: [2026-05-15-sqlite-config-store-design.md](../specs/2026-05-15-sqlite-config-store-design.md)
**Plan**: [2026-05-15-sqlite-config-store.md](../plans/2026-05-15-sqlite-config-store.md)

---

## 验证方法

实施完成后逐项检查，每项通过后标记 `[x]`。本验证只覆盖 SQLite 配置存储改造，不覆盖 usage 统计功能。

---

## 1. 代码结构

- [ ] 新增 `internal/config/sqlite_store.go`
- [ ] 新增 `internal/config/sqlite_store_test.go`
- [ ] `SQLiteStore` 实现 `config.ConfigStore`
- [ ] `NewSQLiteStore(dbPath, legacyJSONPath)` 存在
- [ ] `SQLiteStore.Close()` 存在，并在 `cmd/server/main.go` 中 defer 调用
- [ ] `internal/config/store.go` 的 JSON `Store` 保留
- [ ] `Dockerfile` 未因 SQLite 改造引入 CGO 依赖
- [ ] `go.mod` 使用 pure Go driver `modernc.org/sqlite`

## 2. SQLite 文件与 schema

- [ ] 启动后 SQLite 文件路径为 `<dataDir>/proxy.db`
- [ ] `settings` 表存在
- [ ] `providers` 表存在
- [ ] `provider_model_mappings` 表存在
- [ ] `schema_migrations` 表存在
- [ ] `schema_migrations` 包含版本 `1`
- [ ] `PRAGMA foreign_keys = ON` 在 SQLite store 初始化时执行
- [ ] `provider_model_mappings.provider_id` 删除 provider 时级联删除映射

## 3. 配置 Load 行为

- [ ] 空数据库 `Load()` 返回默认 `BackendURL=https://open.bigmodel.cn/api/anthropic`
- [ ] 空数据库 `Load()` 返回默认 `ProxyPort=443`
- [ ] 空数据库 `Load()` 返回默认 `AdminPort=8442`
- [ ] 空数据库 `Load()` 返回默认 `DataDir=./data`
- [ ] `Load()` 能读取 `settings.backend_url`
- [ ] `Load()` 能读取 `settings.proxy_port`
- [ ] `Load()` 能读取 `settings.admin_port`
- [ ] `Load()` 能读取 `settings.admin_password_hash`
- [ ] `Load()` 能读取 `settings.data_dir`
- [ ] `Load()` 能读取 `settings.active_provider_id`
- [ ] `Load()` 能读取 provider 的 `id/name/api_url/api_token/supports_thinking/enabled`
- [ ] `Load()` 能读取 provider 的 model mappings
- [ ] provider 列表读取顺序稳定，按 `created_at ASC, id ASC`

## 4. 配置 Save 行为

- [ ] `Save(cfg)` 使用事务
- [ ] `Save(cfg)` upsert 所有 settings
- [ ] `Save(cfg)` upsert 所有 providers
- [ ] `Save(cfg)` 删除不再存在于 `cfg.Providers` 的 provider
- [ ] `Save(cfg)` 删除并重建每个 provider 的 model mappings
- [ ] `Save(cfg)` 失败时回滚，不留下半成品配置
- [ ] `Save(cfg)` 不写回 `config.json`
- [ ] `Save(cfg)` 支持空 provider 列表
- [ ] `Save(cfg)` 支持 provider `ModelMappings=nil`

## 5. JSON 迁移

- [ ] `proxy.db` 不存在且 `config.json` 存在时执行一次迁移
- [ ] 迁移后 SQLite 中数据与原 `config.json` 一致
- [ ] 迁移后生成 `config.json.bak-YYYYMMDDHHMMSS`
- [ ] 迁移后保留原 `config.json`
- [ ] 迁移后再次启动不会重复生成备份
- [ ] `proxy.db` 已存在时不再读取 `config.json` 作为权威来源
- [ ] JSON 解析失败时启动返回明确错误，不删除原 JSON

## 6. Provider 切换语义

- [ ] 前端 `/api/providers/{id}/activate` 仍能切换 active provider
- [ ] 切换 active provider 后新请求使用新 provider 的 API URL
- [ ] 切换 active provider 后新请求使用新 provider 的 API token
- [ ] 同 URL 不同 API key 的 provider 不会错误复用旧账号
- [ ] `ActiveProviderID` 指向启用 provider 时，`GetActiveProvider()` 返回该 provider
- [ ] `ActiveProviderID` 指向不存在 provider 时，`GetActiveProvider()` fallback 到第一个 enabled provider
- [ ] `ActiveProviderID` 指向 disabled provider 时，`GetActiveProvider()` fallback 到第一个 enabled provider
- [ ] 没有 enabled provider 时，proxy 返回 `No active provider`

## 7. Admin API 兼容性

- [ ] `GET /api/providers` 响应格式不变
- [ ] `POST /api/providers` 响应格式不变
- [ ] `PUT /api/providers/{id}` 响应格式不变
- [ ] `DELETE /api/providers/{id}` 行为不变
- [ ] `POST /api/providers/{id}/activate` 行为不变
- [ ] `POST /api/providers/{id}/toggle` 行为不变
- [ ] `POST /api/providers/{id}/duplicate` 行为不变
- [ ] `POST /api/providers/{id}/reveal-token` 行为不变
- [ ] 前端 Provider 列表、编辑、删除、设为当前功能正常

## 8. 自动化测试

- [ ] `env GOCACHE=/tmp/go-build go test ./internal/config -count=1` 通过
- [ ] `env GOCACHE=/tmp/go-build go test ./internal/proxy -count=1` 通过
- [ ] `env GOCACHE=/tmp/go-build go test ./internal/admin -count=1` 通过
- [ ] `env GOCACHE=/tmp/go-build go test ./... -count=1` 通过
- [ ] 新增测试覆盖 SQLite Save/Load
- [ ] 新增测试覆盖默认配置
- [ ] 新增测试覆盖 JSON 迁移和备份
- [ ] 新增测试覆盖 provider active fallback 语义
- [ ] 新增测试覆盖事务失败回滚

## 9. 容器与运行验证

- [ ] `docker compose up -d --build` 成功
- [ ] 容器启动日志无 SQLite 初始化错误
- [ ] 宿主机生成 `data/proxy.db`
- [ ] 首次迁移时宿主机保留 `data/config.json`
- [ ] 首次迁移时宿主机生成 `data/config.json.bak-*`
- [ ] provider 切换后 `data/config.json` hash 不变
- [ ] provider 切换后 `data/proxy.db` 修改时间更新
- [ ] 发送 Claude Code 请求成功返回
- [ ] 日志中 `Model mapping` provider 名称与当前 active provider 一致

## 10. 回滚确认

- [ ] 原 `config.json` 未删除
- [ ] 至少存在一个 `config.json.bak-*` 备份
- [ ] 代码回滚到 JSON store 版本后仍可使用保留的 JSON 配置
- [ ] 文档明确迁移后 SQLite 是唯一权威写入目标
- [ ] 文档明确 SQLite 到 JSON 导出不在本次范围

## 11. 范围控制

- [ ] 本阶段没有实现 usage statistics schema
- [ ] 本阶段没有实现 token 统计采集
- [ ] 本阶段没有修改前端使用统计页面
- [ ] 本阶段没有改变 provider API 响应格式
- [ ] 本阶段没有引入 API token 加密存储
- [ ] 本阶段没有改变硬编码端点拦截逻辑

## 12. 最终确认

- [ ] `git status --short` 中没有意外文件
- [ ] `git diff --stat` 符合 plan 的文件范围
- [ ] 所有新增 Go 文件已 `gofmt`
- [ ] 所有新增文档链接有效
- [ ] SQLite spec、plan、validate 三者范围一致
