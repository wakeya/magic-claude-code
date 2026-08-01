# usage 查询性能优化：功能逻辑与安全审查

审查范围：`032aa80..HEAD`，重点覆盖 `internal/usage` 的 scoped SQL、候选去重、迁移和 Coverage/Requests/Trends 读取路径，以及 Dashboard 路由、待跳转状态和 Sessions 懒加载。审查依据为 `spec_ZH.md`、R1/R4 设计说明、`review-6A-backend.md` 和 R5 验证记录；未修改代码、测试或提交。

## 总体结论

当前不建议以“完全兼容”名义直接合并。已发现两个在支持 WAL 并发或历史脏库场景可实际改变统计结果的中危问题，另有一个需要明确数据库边界策略的整数溢出问题；前端存在低危的跨鉴权导航状态污染和父子组件状态竞态。现有后端单元/差分测试与前端测试均通过，但没有覆盖这些关键时序和数据边界，因此通过测试不能证明兼容性不变量成立。

审查中未发现用户筛选条件拼接进 SQL、历史 URL 脱敏被窄投影绕过、或仅凭 legacy 深链直接绕过鉴权的证据；legacy 路由仍由 Dashboard guard 和后续 API 鉴权保护。R1/R4/R5 以及已有 6A 记录中的低危 Trends/Requests 双查询问题未重复列为新问题，除非本审查发现了额外的影响面。

## 中危（MEDIUM）

### M-1 增量去重用原始 TEXT 时间范围，历史时区偏移记录会漏配候选

- **位置**：`internal/usage/dedupe.go:402-429,432-435`；相关时间解析为 `internal/usage/store.go:839-841`。
- **问题/不变量**：增量候选查询使用 `r.started_at >= ? AND r.started_at < ?`，参数是 canonical UTC 文本。历史库中的 `started_at` 可由 RFC3339Nano 解析，且旧数据可能保留 `2026-07-30T12:00:00-07:00` 这类带偏移文本；SQLite 对 TEXT 做字典序比较，不会把它转换为同一 instant。这样会违反“指纹时间差为含边界 10 分钟窗口”和“完全兼容旧 Go 去重”的不变量。迁移回填本身用 Go `time.Parse`，所以同一历史数据在回填时可能正确、在迁移后新增对端候选时却错误。
- **复现路径**：保留一个历史 session 行 `started_at='2026-07-30T12:00:00-07:00'`，其 instant 为 `19:00Z`；迁移后通过正常 `Record` 写入 `started_at='2026-07-30T19:00:00Z'` 的 provider 行，四类 token 和模型均相同。增量查询的 lower bound 约为 `2026-07-30T18:49:59Z`，历史值以 `2026-07-30T12...` 开头，直接被 TEXT lower bound 排除；旧算法解析后会在窗口内配对，导致 effective 统计少去重一行，raw/session-log 的标记也不同。反向插入顺序同样可能漏配。
- **现有测试**：`dedupe_test.go` 覆盖了非法时间、canonical 小数秒、窗口边界、插入顺序和并发写；`legacy_oracle_test.go`/`sql_aggregate_test.go` 覆盖了常见时区/DST 值，但未覆盖“数据库中保存带非 UTC 偏移的 TEXT、迁移后走增量候选”的组合。没有该回归测试。
- **修复方向**：增量范围应按 SQLite 规范化 epoch/fraction 表达式过滤，或先取足够宽的候选窄行再用 Go 解析并执行同一 `Before/After` 窗口判断；禁止对原始时间 TEXT 直接做 canonical 字符串范围比较。

### M-2 Coverage 的 summary/status 两次独立快照可产生分子分母及状态分布不一致

- **位置**：`internal/usage/scoped_query.go:342-403` 生成两条查询；`internal/usage/store.go:494-590` 先执行 summary，再独立执行 status。两次 `db.Query` 没有共享只读事务或物化 scoped snapshot。
- **问题/不变量**：WAL 下每条查询可以获得不同的 reader snapshot。Coverage 的 summary 负责总数、失败数、有/无 usage 等分母，status 负责 `usage_parse_status` 分布；写入恰在两次查询之间时，返回结构不再对应同一个筛选结果，违反统计口径和分子分母一致性。已有 6A 低危双查询说明只覆盖了 Trends/L2 Requests，没有覆盖 Coverage 这条新影响面。
- **复现路径**：summary query 先读到旧快照；随后 writer 提交一条与已有分组相同但 `usage_parse_status='error'`、无 usage 的请求；status query 读到新快照。返回的 `Total/WithoutUsage` 仍是旧分母，但该组的 parse status 计数已包含新行。反向时序下 summary 已包含新分组，而 status 查询旧快照没有该分组，`store.go` 的 `if group, ok := groups[key]; ok` 会静默丢弃该状态，并可能让新组的 `TopUsageParseStatus` 为空。
- **现有测试**：静态 SQL 聚合和 legacy oracle 测试覆盖分子/分母、分组、空集和脱敏后的分组键；`requests_pagination_test.go` 只测静态 count/page。没有 WAL writer 与 Coverage reader 交错、也没有验证两次结果使用同一 snapshot 的测试。
- **修复方向**：在同一只读事务中执行两条查询（并在结束时回滚），或一次物化 scoped 结果后从同一物化快照派生 summary/status；同时增加可控并发回归测试。

### M-3 SQL token/duration 聚合在合法 int64 边界上不保持旧 Go 算术语义（边界库；需确认产品数据约束）

- **位置**：`internal/usage/scoped_query.go:63-65,134-146,261-267,315-332` 使用 SQL 行内四 token 相加及 `SUM()`/`AVG()`；旧路径的逐行累加位于 `internal/usage/store.go:442-472` 附近的 Go 聚合逻辑。
- **问题/不变量**：旧实现对 `int64` 字段逐行做 Go 加法，现实现把加法交给 SQLite。SQLite 整数表达式溢出时会转为 REAL，`SUM()` 对整数累计也可能报 integer overflow；随后 modernc SQLite driver 可能无法把科学计数法 REAL 精确扫描成 `int64`，表现为错误响应，或在可转换时发生舍入。例：四 token 中写入 `input_tokens=9223372036854775807, output_tokens=1`，旧代码得到 int64 包络结果，而 SQL 路径可能得到 REAL/扫描失败。该差异违反响应数值类型与兼容聚合语义；普通 API parser 已拒绝超过 MaxInt64 的单值，但 schema/历史库并未由这些查询保证所有既有数据都在安全加法范围内。
- **复现路径**：在测试 DB 直接插入上述合法 SQLite INTEGER 边界值，再调用 Summary、Providers 或 Models；观察 SQL 表达式发生 REAL promotion、`Scan` 报错或结果失真。多行各自合法但总和超过 MaxInt64 也可触发 `SUM()` 溢出。
- **现有测试**：parser 测试覆盖超范围输入被忽略，oracle/aggregate 测试覆盖常规非负 token 和 duration；没有 `MaxInt64`、跨行总和溢出、REAL promotion、driver Scan 错误测试。因此如果产品明确规定数据库 token 必须远离边界，此项可降为低危；若“完全兼容”包含历史合法 int64 数据，则是中危。
- **修复方向**：明确并校验受支持的数据范围；若必须保持旧 Go int64 语义，使用可证明不溢出的 SQL 模块化/边界表达式或受控 fallback，并增加目标 driver 的边界测试，不要让 SQLite 隐式 REAL 转换决定公开响应。

## 低危（LOW）

### L-1 未认证 legacy 深链留下全局 provider 状态，跨登录/切换用户时会污染下一会话

- **位置**：`internal/frontend/src/router/legacyUsageRedirect.ts:9-19` 在 guard 之前调用 `stagePendingUsageProvider`；`internal/frontend/src/stores/pendingUsageProvider.ts:10-24` 使用模块级变量；`internal/frontend/src/views/DashboardView.vue:1155-1164` 消费；`DashboardView.vue:1819-1824` 的 logout 未清理该状态。
- **问题/安全影响**：未认证访问 `/providers/<任意ID>/usage` 会先把攻击者控制的 ID 写入全局状态，随后虽然 401 会跳登录，但该值可跨登录保留。用户登录另一个账号、或退出后未挂载 Dashboard 再登录时，Dashboard 仍会自动打开该 provider 的 usage modal。当前没有直接鉴权绕过：modal API 仍需当前会话，通常只会返回当前账号的 404/该账号同 ID 的数据；但这是跨鉴权边界的 confused-deputy/状态污染，若 provider ID 可预测且在两个账号中复用，会造成错误资源请求和潜在的 UI 信息暴露边界扩大。
- **复现路径**：无会话打开 `/providers/abc/usage` → guard 收到 401 跳 `/login` → 登录账号 B（或先 logout 再登录）→ Dashboard mount 消费 `abc` 并发起 B 的 provider usage 请求；此期间不需要用户再次访问该旧深链。
- **现有测试**：`legacyUsageRedirect.test.ts:77-99` 明确覆盖“401 后 pending ID 保留”，验证了单次登录后继续导航的设计；没有测试 logout、账号切换、登录失败后再次登录或 provider ID 与新账号不匹配。
- **修复方向**：将 pending 值绑定导航/认证 epoch 并在 401、logout、账号切换时清除，或仅把经过 guard 的 intended route 交给登录页并在成功登录后一次性消费和清理。

### L-2 SessionBrowser 父 prop watcher 可覆盖子组件请求中的本地状态

- **位置**：`internal/frontend/src/components/SessionBrowser.vue:257-271` 无条件把 `projects/sessions/loading/error` props 写回本地 ref；子请求的 generation 保护在 `useSessionBrowserData.ts` 内，只能防止该 composable 自己的过期响应。
- **问题**：父 Dashboard 的懒加载/刷新完成后，prop watcher 不检查子组件当前 generation 或请求状态，可能覆盖用户刚选择的项目、正在加载的会话列表或错误状态。优化引入的父级懒加载与子级主动刷新并没有共享一个状态版本，因此“卸载/导航竞态修复”并未覆盖父子更新交错场景。
- **复现路径**：进入 Sessions 后父级 `loadSessionsList()` 尚未完成；用户在子组件触发项目切换或 reload；父级 promise 随后完成，watcher 将旧的全局列表和 loading=false 写入子组件，造成选中项目/列表短暂回退，或遮蔽子请求错误。子请求稍后成功时又可能由父级重新 apply，表现为非确定性闪烁。
- **现有测试**：`useLazySessionData.test.ts`、`useSessionBrowserData.test.ts` 覆盖 composable generation/invalidate；`SessionBrowserLayout.test.ts` 只覆盖布局，未挂载真实父子组件并交错 resolve 两类 promise。
- **修复方向**：统一父子数据源或给 props 增加版本/请求 epoch，在子请求活跃时忽略旧父级快照，并增加真实组件级交错时序测试。

### L-3 关键兼容边界的测试缺口

- **位置**：`internal/usage/dedupe_test.go`、`sql_aggregate_test.go`、`legacy_oracle_test.go`、`requests_pagination_test.go`；前端 `legacyUsageRedirect.test.ts`、`useSessionBrowserData.test.ts`。
- **问题**：当前 2830 个 usage 测试和 269 个前端测试通过，但差分 oracle 主要是单一静态数据库快照；缺少历史 offset 时间走增量维护、Coverage 两查询 WAL 交错、int64/跨行 SUM 边界、认证边界 pending 清理、SessionBrowser 父子组件交错，以及超大 page/page_size 的资源上限验证。测试缺口本身不能证明运行时一定失败，但使上述兼容和资源安全承诺没有回归保护。
- **修复方向**：补充可控 writer barrier 的 WAL 并发测试、带 `±HH:MM` 的历史 TEXT fixtures、driver 级整数边界测试、登录/退出/切换账号路由测试和组件级 promise 交错测试；同时明确分页参数的上限策略。

## 安全审查结论（未发现新增缺陷）

- `filterWhere` 的 provider/model/URL/status/time 条件均使用参数绑定；SQL 中拼接的列名、scope 分支和排序表达式来自固定内部常量，未发现用户输入形成 SQL 语句的路径。
- `scanRequestRow` 及 Coverage 的窄投影仍在读取时调用 URL 脱敏；历史脏库 URL 没有因 SQL 聚合而直接回传。应保留现有二次脱敏测试。
- legacy 深链只能影响待打开的 provider ID，Dashboard guard 和 usage API 仍执行认证；未发现可凭未认证深链读取统计数据的路径。
- 迁移阶段分别以独立事务完成候选表、索引和 rank 回填，每阶段有 marker/幂等检查；本次未发现“半途中断后同一阶段继续写坏数据”的确定性缺陷。但由于不是一个跨所有迁移阶段的总事务，发布/崩溃窗口仍应依靠逐阶段 marker 和重开测试验证，不能把它当作全局原子迁移。

## 残余风险

`review-6A-backend.md` 已记录的 Trends 范围查询和 Requests count/page 双快照问题仍然存在，只是本审查未将其重复计数；若业务要求每个响应严格同一快照，应统一纳入修复。R5 记录的性能目标也不是本审查的正确性通过条件，现有测量仍显示部分并发场景未达到目标值。修复 M-1/M-2、明确 M-3 的数据边界并补齐 L-3 回归后，才可重新宣称“完全兼容”；在此之前建议不合并。
