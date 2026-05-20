# SQLite 配置存储改造规格

**版本**: 0.1
**日期**: 2026-05-15
**状态**: 待审核

---

## 1. 背景

当前代理使用 `data/config.json` 保存全局配置和 provider 列表。运行时代理请求通过 `ConfigStore.Load()` 读取整份配置，再从 `ActiveProviderID` 找到当前 provider，最后将请求转发到 provider 的 `APIURL` 并使用 provider 的 `APIToken` 覆盖认证头。

该机制当前能工作，但 JSON 文件存储有几个局限：

1. provider 配置是结构化数据，后续扩展字段、迁移和查询不适合长期放在单个 JSON 文件中。
2. 整文件读写不利于未来做并发安全、审计、排序、按 provider 维度扩展能力。
3. 用户希望 provider 切换与配置状态更可靠、可检查，并避免对单个 JSON 文件的依赖。

本次改造采用 SQLite 作为持久化存储，但保持现有 `ConfigStore` 接口语义，降低改动面。

---

## 2. 决策

采用方案 A：保持 `ConfigStore` 接口不变，新增 SQLite 实现。

```go
type ConfigStore interface {
	Load() (*Config, error)
	Save(cfg *Config) error
}
```

### 2.1 核心原则

1. `Load()` 仍返回完整 `*Config`。
2. `Save(cfg)` 仍保存完整配置。
3. 代理请求路径无需改成细粒度 repository。
4. 启动后以 SQLite 为唯一写入目标。
5. 首次迁移时从 `config.json` 导入，并备份原 JSON。
6. 不做 SQLite 与 JSON 双写，避免数据源分裂。

---

## 3. 非目标

1. 不重写 admin provider API 为细粒度 repository。
2. 不在本配置迁移 spec 内实现 provider 排序、审计日志、使用量统计等新功能；使用量统计由独立 spec 设计，并复用同一个 SQLite 数据库。
3. 不改变前端 API 响应格式。
4. 不改变 provider 切换语义。
5. 不引入内存缓存，避免切换 provider 后状态不一致。

---

## 4. 文件与模块设计

### 4.1 新增文件

| 文件 | 责任 |
|------|------|
| `internal/config/sqlite_store.go` | SQLite 版 `ConfigStore` 实现 |
| `internal/config/sqlite_store_test.go` | SQLite 存储、迁移、读写测试 |

### 4.2 修改文件

| 文件 | 修改 |
|------|------|
| `cmd/server/main.go` | 从 `config.NewStore(configPath)` 切换为 SQLite store 初始化 |
| `internal/config/store.go` | 保留 JSON store，用作迁移来源和测试兼容 |
| `go.mod` / `go.sum` | 增加 SQLite driver 依赖 |
| `Dockerfile` | 不修改；本次固定使用 pure Go SQLite driver，保持 `CGO_ENABLED=0` 构建 |

---

## 5. SQLite driver 选择

推荐使用 pure Go SQLite driver：

```text
modernc.org/sqlite
```

原因：

1. 当前 Dockerfile 使用 `CGO_ENABLED=0` 构建静态 Go 二进制。
2. `github.com/mattn/go-sqlite3` 需要 CGO，会引入 Alpine 构建依赖和运行时兼容问题。
3. 本项目配置数据量很小，pure Go driver 性能足够。

本次不引入 CGO SQLite driver。如果 `modernc.org/sqlite` 在实现阶段无法构建，通过设计评审后再单独调整方案。

---

## 6. 数据库位置

SQLite 文件路径：

```text
<dataDir>/proxy.db
```

当前 Docker Compose 已挂载：

```yaml
volumes:
  - ./data:/app/data
```

因此容器内 `/app/data/proxy.db` 会持久化到宿主机 `./data/proxy.db`。

后续使用量统计也复用该数据库文件。配置表和统计表通过不同 schema migration 管理，避免维护多个 SQLite 文件。

---

## 7. Schema 设计

### 7.1 `settings`

保存全局配置，使用单行 key/value。

```sql
-- settings: 全局配置键值表，用于保存端口、旧版 fallback 后端地址、当前激活 provider 等配置
CREATE TABLE IF NOT EXISTS settings (
  key TEXT PRIMARY KEY,      -- 配置键，例如 backend_url、active_provider_id
  value TEXT NOT NULL        -- 配置值，统一以字符串保存，读取时按需要转换类型
);
```

初始 keys：

| key | value |
|-----|-------|
| `backend_url` | 旧版 fallback 后端地址 |
| `proxy_port` | 代理端口 |
| `admin_port` | 管理端口 |
| `admin_password_hash` | 管理密码 hash |
| `data_dir` | 数据目录 |
| `active_provider_id` | 当前激活 provider ID |

端口值以字符串存储，`Load()` 时转整数。这样 schema 简单，未来新增全局配置无需改表结构。

### 7.2 `providers`

```sql
-- providers: provider 主表，用于保存每个后端供应商的连接信息、能力开关和启用状态
CREATE TABLE IF NOT EXISTS providers (
  id TEXT PRIMARY KEY,                         -- provider 唯一 ID，沿用当前 provider-xxxx 格式
  name TEXT NOT NULL,                          -- provider 展示名称
  api_url TEXT NOT NULL,                       -- 后端 Anthropic-compatible API 基础地址
  api_token TEXT NOT NULL DEFAULT '',          -- provider API token，转发请求时用于覆盖 Authorization
  supports_thinking INTEGER NOT NULL DEFAULT 0,-- 是否支持 thinking 字段，0=false，1=true
  enabled INTEGER NOT NULL DEFAULT 1,          -- 是否启用，0=false，1=true
  created_at TEXT NOT NULL,                    -- 创建时间，RFC3339Nano 字符串
  updated_at TEXT NOT NULL                     -- 更新时间，RFC3339Nano 字符串
);
```

时间字段用 RFC3339Nano 字符串保存，避免 SQLite timezone 语义差异。

### 7.3 `provider_model_mappings`

```sql
-- provider_model_mappings: provider 模型映射表，用于把 Claude Code 请求模型映射到后端实际模型
CREATE TABLE IF NOT EXISTS provider_model_mappings (
  provider_id TEXT NOT NULL,                   -- 所属 provider ID
  source_model TEXT NOT NULL,                  -- Claude Code 发来的模型名，例如 claude-opus-4-6
  target_model TEXT NOT NULL,                  -- 后端实际模型名，例如 glm-5.1、MiniMax-M2.7-highspeed
  PRIMARY KEY (provider_id, source_model),     -- 同一 provider 下每个源模型只能有一个映射
  FOREIGN KEY (provider_id) REFERENCES providers(id) ON DELETE CASCADE -- 删除 provider 时自动删除映射
);
```

模型映射独立成表，避免把 JSON 字符串塞入 provider 主表，也方便后续按模型查询或校验。

### 7.4 `schema_migrations`

```sql
-- schema_migrations: 数据库 schema 迁移记录表，用于判断当前数据库结构版本
CREATE TABLE IF NOT EXISTS schema_migrations (
  version INTEGER PRIMARY KEY, -- 已应用的 schema 版本号
  applied_at TEXT NOT NULL     -- 应用时间，RFC3339Nano 字符串
);
```

当前版本：

```text
1
```

---

## 8. Store 行为

### 8.1 初始化

新增构造函数：

```go
func NewSQLiteStore(dbPath string, legacyJSONPath string) (*SQLiteStore, error)
```

初始化步骤：

1. 确保 `dbPath` 所在目录存在。
2. 打开 SQLite。
3. 执行 `PRAGMA foreign_keys = ON`。
4. 执行 schema migration。
5. 如果数据库是新建且 `legacyJSONPath` 存在，则迁移 JSON。

### 8.2 `Load()`

`Load()` 从 SQLite 聚合出完整 `Config`：

1. 从 `settings` 读取全局配置。
2. 从 `providers` 读取 provider 列表。
3. 从 `provider_model_mappings` 读取每个 provider 的映射。
4. 填充默认值：
   - `ProxyPort=443`
   - `AdminPort=8442`
   - `DataDir="./data"`
   - `BackendURL="https://open.bigmodel.cn/api/anthropic"`，仅在 settings 中没有值时使用
5. 返回 `*Config`。

### 8.3 `Save(cfg)`

`Save(cfg)` 使用事务覆盖当前配置：

1. `BEGIN`
2. upsert `settings`
3. 删除不存在于 `cfg.Providers` 的 provider
4. upsert 每个 provider
5. 删除并重建每个 provider 的 model mappings
6. `COMMIT`

必须保证 `Save()` 要么完整成功，要么不改变旧配置。

### 8.4 并发

SQLite store 内部使用 `sync.Mutex` 包住 `Load()` 和 `Save()`。

原因：

1. 当前 admin 和 proxy 会并发访问 store。
2. 配置量很小，串行化读写成本低。
3. 简化 SQLite connection 与事务并发语义。

---

## 9. JSON 迁移策略

### 9.1 迁移触发条件

仅在以下条件全部满足时迁移：

1. `proxy.db` 不存在或没有初始化 schema。
2. `config.json` 存在。
3. `config.json` 能成功解析为 `Config`。

### 9.2 迁移流程

1. 使用现有 JSON `Store` 读取 `config.json`。
2. 调用 SQLite `Save(cfg)` 写入数据库。
3. 将原文件复制为：

```text
config.json.bak-YYYYMMDDHHMMSS
```

4. 保留原 `config.json` 不删除。

### 9.3 为什么不删除原 JSON

保留原 JSON 方便人工核对和回滚。服务启动后不再写 JSON，因此 JSON 会逐渐变成旧快照。

### 9.4 为什么不双写

双写会引入两个权威数据源：

1. SQLite 写成功但 JSON 写失败。
2. JSON 被手动改但 SQLite 仍是旧值。
3. 排查 provider 切换时不知道哪个文件才是准。

因此迁移后 SQLite 是唯一权威存储。

---

## 10. 启动路径变更

当前：

```go
configPath := filepath.Join(*dataDir, "config.json")
configStore := config.NewStore(configPath)
```

目标：

```go
configPath := filepath.Join(*dataDir, "config.json")
dbPath := filepath.Join(*dataDir, "proxy.db")
configStore, err := config.NewSQLiteStore(dbPath, configPath)
if err != nil {
	log.Fatalf("Failed to initialize config store: %v", err)
}
```

启动打印和状态页本次保持现状，不额外改前端展示。

---

## 11. 回滚策略

### 11.1 代码回滚

如果 SQLite 版本出现问题，可以回滚代码到 JSON store 版本。

### 11.2 数据回滚

因为首次迁移会保留：

```text
config.json
config.json.bak-YYYYMMDDHHMMSS
```

回滚代码后仍可继续使用 JSON。

### 11.3 注意

迁移后在 SQLite 中新增或修改的 provider 不会自动同步回 JSON。若需要带数据回滚，需要额外导出 SQLite 内容为 JSON。本次不实现导出功能。

---

## 12. 测试计划

### 12.1 SQLite store 基础读写

测试：

1. 新建临时目录。
2. 初始化 SQLite store。
3. 保存包含多个 provider、active provider、model mappings 的配置。
4. 重新 `Load()`。
5. 断言所有字段一致。

### 12.2 默认配置

测试空数据库时 `Load()` 返回默认配置：

1. `BackendURL=https://open.bigmodel.cn/api/anthropic`
2. `ProxyPort=443`
3. `AdminPort=8442`
4. `DataDir=./data`

### 12.3 JSON 迁移

测试：

1. 写入旧版 `config.json`。
2. 初始化 SQLite store。
3. 断言数据已导入 SQLite。
4. 断言存在 `config.json.bak-*`。
5. 断言后续 `Save()` 不再修改 `config.json`。

### 12.4 Provider 切换

测试：

1. 保存两个 provider，token 不同。
2. 修改 `ActiveProviderID` 并 `Save()`。
3. `Load().GetActiveProvider()` 返回新 provider。
4. proxy handler 使用新 provider 的 APIURL 和 token。

### 12.5 删除 active provider

沿用现有行为：

1. 删除当前 active provider。
2. 自动激活剩余列表中的第一个 provider。
3. 保存后重新 `Load()` 仍保持该行为。

### 12.6 事务完整性

通过构造非法数据或模拟错误，验证 `Save()` 失败时不会写入半成品配置。

---

## 13. 验证命令

实现后运行：

```bash
env GOCACHE=/tmp/go-build go test ./...
docker compose up -d --build
```

手动验证：

1. 启动后确认生成 `data/proxy.db`。
2. 确认保留 `data/config.json` 和生成 `data/config.json.bak-*`。
3. 在前端点击不同 provider 的“设为当前”。
4. 发送新的 Claude Code 请求。
5. 查看日志确认 `Model mapping` 中 provider 名称变化。
6. 对同 URL 不同 API key 的 provider，确认 429 不再错误复用旧账号。

---

## 14. 风险与缓解

| 风险 | 影响 | 缓解 |
|------|------|------|
| SQLite driver 构建失败 | 容器无法构建 | 使用 pure Go `modernc.org/sqlite`，不启用 CGO |
| 迁移失败 | 启动失败或配置丢失 | 迁移前不删除 JSON；失败时返回明确错误 |
| JSON 与 SQLite 数据不一致 | 用户误看旧 JSON | 文档说明迁移后 SQLite 为权威；本次不再读取 JSON |
| Save 覆盖写导致 provider 顺序变化 | 前端列表顺序变化 | providers 表可按 `created_at` 或插入顺序读取；建议按 `created_at ASC` |
| 并发读写 | 切换 provider 时读到中间状态 | `Save()` 事务 + store mutex |
| active provider 指向被禁用或不存在 provider | 请求 fallback 到第一个 enabled provider | 保持现有 `GetActiveProvider()` 行为 |

---

## 15. 兼容性说明

1. 前端 API 不变。
2. 代理请求路径不变。
3. provider 切换语义不变。
4. `data/config.json` 首次迁移后不再作为权威来源。
5. 旧 JSON `Store` 保留，便于迁移和测试。

---

## 16. 暂不处理项

1. provider 使用量统计的采集、查询和前端展示；该能力由 `2026-05-15-usage-statistics-design.md` 单独定义。
2. provider 排序字段。
3. provider 分组。
4. API token 加密存储。
5. SQLite 到 JSON 的导出命令。
6. 前端显示当前存储后端类型。
