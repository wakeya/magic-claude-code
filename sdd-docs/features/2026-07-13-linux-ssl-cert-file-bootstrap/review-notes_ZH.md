# Linux 系统信任与 SSL_CERT_FILE 自动引导审查记录

日期：2026-07-13  
审查者：Codex

## 范围

审查相对 `v0.15.3` 的完整工作区改动，包括 Linux 系统 bundle 验证、`SSL_CERT_FILE` profile 持久化与 marker、bootstrap 状态/提示，以及 `server.crt` 证书链修复。

## 关键发现与处理要求

1. **Medium：已知 shell 没有扫描所有可能生效的启动 profile。**
   - `resolveShellProfiles` 对 Bash 只返回 `.bashrc`，对 zsh 只返回 `.zshrc`。用户后来写入 `.profile`、`.bash_profile`、`.bash_login`、`.zprofile` 等实际启动文件的冲突值会绕过检测。临时审查测试已复现 Bash `.profile` 场景：`writePOSIXProfileSSLCertFile` 返回 `nil`，而不是 `ErrUserCustomValue`。
   - 必须修复：把“冲突扫描候选”与“首选写入目标”分开；接受或写入 MCC block 前，扫描该 shell 所有相关且存在的启动 profile。
2. **Medium：每个 profile 只检查第一个非 MCC 管理的赋值。**
   - `profileSSLCertFileOutsideMCCBlockValue` 找到第一条赋值就立即返回。如果前一条等于 MCC 系统 bundle，后一条指向用户自定义 bundle，持久化仍返回成功，但 shell 最终生效的是后面的冲突值。临时审查测试已用 `.bashrc` 中两条 export 稳定复现。
   - 必须修复：完整扫描 profile；任意不同值都必须返回 `ErrUserCustomValue`。只有找到的所有非 MCC 赋值都等于目标 bundle 时，才能按“同值”处理。
3. **Low：规格要求的运维文档与验证回填尚未完成。**
   - `CLAUDE.md` 仍把 `SSL_CERT_FILE` 描述成极少数 fallback；README 未加入任务 5 要求的 Linux 二进制/Docker 指引；feature spec 仍显示 `0 / 7 已规划`，验证项也未勾选。
   - 必须处理：发版前让 FAQ/README 与当前 Linux 行为一致，并回填已完成的验证证据。

## 最终审查结论

暂不批准合并。证书链轮换与 leaf-only 修复逻辑一致，未发现新的可直接利用安全漏洞；但两个已复现的 profile 扫描缺陷仍可能在实际 shell 使用冲突 bundle 时误报 `SSL_CERT_FILE` 已就绪，使原始 `unknown_ca` 问题复发。

## 残余说明

- 静态 symlink marker 攻击已被拒绝，但 marker/profile 写入仍采用“先检查、后按路径写入”，当高权限进程向不可信可写目录写文件时，仍保留此前已记录的本地 TOCTOU 窗口。
- 服务端发送根证书不能替代客户端信任锚配置；正确设置 `SSL_CERT_FILE` 并确保系统 bundle 包含 MCC CA，仍是解决 `unknown_ca` 的决定性条件。
- 验证通过：`go test ./... -count=1`、`go test -race ./internal/bootstrap ./internal/cert -count=1`、`go vet ./...`，以及 Windows amd64/macOS arm64 bootstrap 测试二进制交叉编译。

## 后续复审 — 2026-07-13

### 已确认解决

- 冲突扫描候选和首选写入目标已分离；修改前会扫描 Bash 的 `.profile`、`.bash_profile`、`.bash_login`，以及 zsh 的 `.zprofile`。
- 解析器返回的基础 POSIX/fish 赋值现在会全部检查，后面的冲突 export 不再被前面的同值 export 掩盖。
- 即使同一写入 profile 中存在同值用户配置，旧 MCC 管理块也会被修复。
- `CLAUDE.md`、README、README.en 和 feature 检查清单已统一说明 Linux 二进制与 Docker 行为。

### 尚存问题

1. **Medium：非写入 profile 中的同值可能阻止首选 profile 持久化。**
   - `sameValueFound` 在所有扫描 profile 之间全局共享。对 Bash 而言，如果只有 `.profile` 含正确值，`.bashrc` 写入循环会跳过创建 MCC block 并返回成功。未继承也不读取 `.profile` 的非 login 交互 Bash 仍可能在没有 `SSL_CERT_FILE` 的情况下启动 Claude Code。
   - 临时审查测试已复现：`.profile` 含正确值，调用返回成功，但 `.bashrc` 没有创建。
   - 必须修复：按 profile 跟踪同值。除非能保证启动环境继承，否则其他启动文件中的同值不能阻止确保首选写入 profile。
2. **Medium：zsh 启动 profile 覆盖仍不完整。**
   - 扫描列表包含 `.zshrc` 和 `.zprofile`，但缺少每次 zsh 都会读取的 `.zshenv`，以及 login zsh 在 `.zshrc` 后读取的 `.zlogin`。其中的冲突值仍可绕过检测，并覆盖 MCC block 或被其覆盖。
   - 临时审查测试已复现 `.zshenv` 场景：持久化返回成功，而不是 `ErrUserCustomValue`。
   - 必须修复：加入 `.zshenv`、`.zlogin`；若声明支持 zsh profile 重定位，还需处理 `ZDOTDIR`。
3. **Medium：常见的导出赋值语法未识别。**
   - POSIX 解析器只识别可选 `export ` 后直接出现的 `SSL_CERT_FILE=`。zsh `typeset -x`、Bash `declare -x`、fish `set -Ux` 等常见形式会被忽略；MCC 随后可能误报成功并追加 block，改变用户自定义值的实际效果。
   - 临时审查测试已复现 `typeset -x SSL_CERT_FILE=/custom/...`：持久化返回成功，而不是 `ErrUserCustomValue`。
   - 必须修复：识别常见导出声明形式，或对包含目标 key 的未识别活动赋值采取保守拒绝策略。

### 后续结论

仍不批准合并。上一轮两个复现场景和文档缺口已经解决，但上述三个 profile 边界/解析缺陷仍可能造成错误就绪，或覆盖实际生效的用户 CA 配置。未发现新的可直接利用安全漏洞。

再次验证通过：SSL profile 聚焦测试、`go test ./... -count=1`、`go test -race ./internal/bootstrap ./internal/cert -count=1`、`go vet ./...`，以及 Windows amd64/macOS arm64 bootstrap 测试二进制交叉编译。feature spec 中 Linux 长对话原生验证仍待完成。

## 第三轮复审 — 2026-07-13

### 已确认解决

- 非 MCC 同值现在按 profile 跟踪；`.profile` 中的同值不再阻止首选 Bash `.bashrc` 写入。
- zsh 冲突扫描覆盖 `.zshenv`、`.zprofile`、`.zshrc`、`.zlogin`，写入目标仍保持 `.zshrc`；`ZDOTDIR` 已明确为非目标。
- POSIX 解析器已识别带导出短/长选项的 `typeset`/`declare`，fish 解析器已识别组合或分离的 scope + export flags；`echo "$SSL_CERT_FILE"` 等只读引用仍会忽略。
- 对应聚焦回归测试已补齐，双语 spec 与 fix plan 已同步。

### 尚存问题

1. **Medium：不完整的 MCC 管理块标记会隐藏其后的真实赋值。**
   - `profileSSLCertFileOutsideMCCBlockValues` 遇到 begin 标记后进入块内状态，在 end 标记出现前忽略所有后续行。如果块因写入中断或人工编辑而缺失 end，后面的用户自定义赋值会被忽略；`replaceMarkedBlock` 随后又追加新 MCC block，并返回持久化成功。
   - 临时审查测试已复现：只有 begin、没有 end，后面跟 `export SSL_CERT_FILE=/custom/...`，调用返回 `nil`，而不是 `ErrUserCustomValue`。
   - 必须修复：只有完整配对的标记才能隐藏块内内容。未配对或嵌套标记应 fail-closed，或在不隐藏真实 shell 行的前提下安全修复。
2. **Medium：管理块后的环境变更命令可使实际值失效，但不会被检测。**
   - 扫描器能识别赋值，却不识别 `unset SSL_CERT_FILE`、`export -n`、`typeset +x`、fish erase/unexport 等活动变更。如果这些命令位于未变化的 MCC block 后，持久化仍返回成功，但最终 shell 已不再导出已验证 bundle。
   - 临时审查测试已复现 MCC block 后跟 `unset SSL_CERT_FILE`：调用返回 `nil`，没有报告用户自定义/冲突状态。
   - 必须修复：识别针对该 key 的环境变更命令并按执行顺序纳入最终状态判断；或保守地将其视为用户管理冲突，同时继续忽略只读引用。

### 第三轮结论

此前三条 Medium 已修复，但由于上述两个新复现的 fail-open 状态仍可造成错误就绪，本分支暂不批准提交。未发现新的可直接利用安全漏洞。

独立验证通过：SSL/fish 聚焦测试、`go test ./... -count=1`、`go test -race ./internal/bootstrap ./internal/cert -count=1`、`go vet ./...`、Windows amd64 与 macOS arm64 server/test 交叉编译，以及 `git diff --check`。按 spec，Linux 重启 shell/长对话原生验证仍待完成。

## 第四轮修复复审 — 2026-07-13

### 已确认解决

- profile 扫描器现在只接受精确、完整且非嵌套的 SSL MCC marker 对。孤立、嵌套或嵌入活动命令的 marker 会在修改 profile 之前返回 `ErrUserCustomValue`。
- 在完整 SSL 管理块之外，精确针对目标 key 的 POSIX `unset`、`export -n`、`typeset +x`、`declare +x`，以及 fish erase/unexport 形式均按用户管理冲突处理。测试覆盖带引号的 POSIX key 和组合 fish 短选项；其他变量的变更及只读引用仍允许。
- 自审发现 POSIX Node CA 与 SSL 块共用通用结束 marker。扫描器现已单独跟踪既有 Node CA 块：合法 Node CA 块保持兼容，同时其块体仍会扫描，不能借此隐藏 `SSL_CERT_FILE` 赋值或变更命令。
- 回归测试要求所有冲突路径都保持原 profile 字节不变。

### 验证

- `go test ./... -count=1`
- `go test -race ./internal/bootstrap ./internal/cert -count=1`
- `go vet ./...`
- Windows amd64 与 macOS arm64 server 构建及 bootstrap 测试二进制交叉编译
- `git diff --check`

### 第四轮结论

两条已报告的 Medium fail-open 状态均已解决；在本轮审查的 profile 扫描路径中，没有发现仍可复现的逻辑缺陷或新的可直接利用安全缺陷。当前可交由独立复审；Linux 重启 shell/长对话原生验证仍待完成，未声明为已完成。

### 残余说明

- 此前记录的“检查后写入”TOCTOU 窗口仍存在于高权限进程向不可信可写目录写文件的场景。
- 通过 `ZDOTDIR` 重定位的 zsh profile 仍明确不在本功能范围内。

## 第五轮独立复审 — 2026-07-13

### 已确认解决

- 异常、嵌套、孤立及嵌入活动命令的 MCC marker 已能在 SSL profile 写入前 fail-closed。
- spec 覆盖的 POSIX 与 fish 精确 key 删除/取消导出形式，已能在 SSL 管理块外被检测。
- SSL 扫描器可接受合法 Node CA 块，且不会隐藏该 Node CA 块体内的 SSL 变更。

### 尚存问题

1. **Medium：共用结束 marker 仍会破坏管理块替换与幂等性。**
   - `replaceMarkedBlock` 查找的是整个文件中第一次出现的 `end`，而不是指定 begin 之后第一个配对的 end。Node CA 和 SSL 块都使用 `# <<< mcc <<<`，所以排在前面的另一类块会提供错误的结束位置。
   - 在正常的“Node CA 块在前、SSL 块在后”顺序中，既有 SSL 块永远不会被替换；每次调用 `writePOSIXProfileSSLCertFile` 都会再追加一个 SSL 块。Linux bootstrap 即使 SSL marker 命中也会调用持久化，因此重复启动可持续膨胀 profile。
   - 如果 Node CA 块前有同值的非管理 `SSL_CERT_FILE`，其后又有旧 SSL 管理块，同值提前返回分支同样会误用 Node CA 的结束 marker，并在未修复旧 SSL 块时返回成功。旧管理值位于文件后方，shell 启动时仍会覆盖正确值。
   - 反向顺序也能复现 Node CA 写入缺陷：SSL 块位于旧 Node CA 块之前时，`writePOSIXProfileNodeCA` 会追加重复 Node CA 块，而不是替换旧块。
   - 临时审查测试已复现以上三种场景，并在执行后删除。必须修复：从 `begin + len(begin)` 开始查找 end（或使用带块类型的解析器），同值短路和替换必须使用同一个配对范围；为两种块补充顺序、旧值及重复运行回归测试。

### 第五轮结论

暂不批准合并。最新扫描器修复有效，但写入器仍违反 spec 的幂等和旧块修复要求，并可能在实际 `SSL_CERT_FILE` 仍错误时返回成功。未发现新的可直接利用安全漏洞。

仓库现有验证均独立通过：`go test ./... -count=1`、`go test -race ./internal/bootstrap ./internal/cert -count=1`、`go vet ./...`、Windows amd64 与 macOS arm64 server/test 交叉编译，以及 `git diff --check`；现有测试尚未覆盖上述块顺序回归。Linux 重启 shell/长对话原生验证仍待完成。

## 第五轮修复结论 — 2026-07-13

### 已确认解决

- `findMarkedBlock` 现在先定位请求的 begin marker，并且只从 `begin + len(begin)` 之后查找共用 end marker；前置的另一类 MCC 块不再为目标块提供错误结束范围。
- `replaceMarkedBlock` 与 `sameValueProfiles` 短路逻辑现已复用同一个目标相对定位器。块外存在同值 `SSL_CERT_FILE` 时，也不会再跳过后置 stale SSL 管理块修复。
- TDD 回归测试覆盖 Node CA 在旧 SSL 前、SSL 在旧 Node CA 前、两种重复运行路径，以及“块外同值 + 中间 Node CA + 后置旧 SSL”顺序；测试要求旧路径消失、目标 begin marker 恰好一个、第二次调用后字节完全不变。
- 既有异常 marker、用户冲突、Node CA/SSL 共存、profile 字节不变、引用转义和 profile 安全测试继续通过。

### 验证

- `go test ./internal/bootstrap -count=1`
- `go test ./... -count=1`
- `go test -race ./internal/bootstrap ./internal/cert -count=1`
- `go vet ./...`
- Windows amd64 与 macOS arm64 server 构建及 bootstrap 测试二进制交叉编译
- `git diff --check`

### 第五轮结论

共用结束 marker 的 Medium 问题已在两类管理块排列、stale block 替换、同值路径与重复运行幂等性上解决；本次修复没有引入新的可直接利用安全缺陷。Linux 重启 shell/长对话原生验证仍待完成，未声明为已完成。

### 残余说明

- 既有“检查后写入”TOCTOU 窗口保持不变；本修复没有扩大写入目标，也没有放宽 symlink/非常规文件校验。
- 通过 `ZDOTDIR` 重定位的 zsh profile 仍明确不在本功能范围内。

## 第六轮独立复审 — 2026-07-13

### 已确认解决

- 第五轮指出的共用结束 marker 缺陷已解决。`findMarkedBlock` 只从目标 begin marker 之后查找对应 end marker，`replaceMarkedBlock` 和 SSL 同值短路逻辑都复用这个目标相对范围。
- 回归测试已覆盖 Node CA 在 SSL 前、SSL 在 Node CA 前、旧块替换、重复运行幂等性，以及“块外同值 SSL + 后置旧 SSL 管理块”顺序。
- 既有 SSL 异常 marker、用户自定义值检测、marker symlink 检查、证书链重签检查，以及失败路径 profile 字节不变测试继续通过。

### 尚存问题

1. **Medium：Node CA profile 写入路径仍未对异常 MCC marker fail-closed。**
   - `internal/bootstrap/adapters.go:1066` 的 `profileHasNodeCAKeyOutsideMCCBlock` 仍用简单布尔值处理 `posixCABlockBegin` / `posixCABlockEnd`，只返回是否看到用户自定义赋值，不报告 marker 异常状态。
   - 当 profile 中存在未闭合的 Node CA begin marker，后面跟着 `export NODE_EXTRA_CA_CERTS=/custom/company-ca.crt` 时，`internal/bootstrap/adapters.go:1000` 的 `writePOSIXProfileNodeCA` 会返回成功并追加新的 MCC 块，而不是返回 `ErrUserCustomValue` 并保持文件不变。临时审查测试已复现成功返回和文件被修改。
   - 这违反了 SSL marker 现在已经具备的 fail-closed 约束，并可能在异常 profile 中静默忽略用户手写的 Node CA 值。它不是新的可直接利用提权漏洞，因为 profile 持久化的受影响路径以用户身份运行，高权限路径仍受保护；但它仍是信任引导正确性和安全边界问题。
   - 修复要求：把 Node CA 的布尔扫描器替换为可返回错误的扫描器，对未闭合 begin、孤立 end、嵌套 begin、嵌入活动命令的 marker 都返回失败，行为与 SSL 扫描器一致。补充对应回归测试，并验证失败路径 profile 字节不变。

### 验证

- `go test ./internal/bootstrap -count=1`
- `go test ./... -count=1`
- `go test -race ./internal/bootstrap ./internal/cert -count=1`
- `go vet ./...`
- Windows amd64 与 macOS arm64 server 交叉编译
- `git diff --check`

### 第六轮结论

暂不批准合并。第五轮共用结束 marker 的 Medium 已修复，但当前 profile 写入器仍存在可复现的 Node CA 异常 marker fail-open 路径。Linux 重启 shell/长对话原生验证仍待完成，未声明为已完成。

## 第六轮修复结论 — 2026-07-13

### 已确认解决

- `profileHasNodeCAKeyOutsideMCCBlock` 现在同时返回用户自定义值结果与扫描错误。POSIX Node CA profile 路径遇到异常 MCC marker 时会返回 `ErrUserCustomValue`，不会再隐藏后续 `NODE_EXTRA_CA_CERTS` 赋值。
- 扫描器现在跟踪当前管理块类型。只有 Node CA 管理块内部会抑制 `NODE_EXTRA_CA_CERTS` 检测；未闭合 begin、孤立 end、嵌套 begin、嵌入命令的 marker 都会失败关闭。
- `scanPOSIXProfilesForCustomValue` 与 `writePOSIXProfileNodeCA` 会在任何环境更新或 profile 写入前传播扫描错误。
- 新增回归覆盖写入路径的字节保持失败场景、扫描器级别的未闭合 begin、孤立 end、嵌套 begin、嵌入 marker 场景，以及 Darwin 预检查路径遇到 POSIX Node CA 异常 marker 时必须跳过 `launchctl`。

### 验证

- `go test ./internal/bootstrap -count=1`
- `go test ./... -count=1`
- `go test -race ./internal/bootstrap ./internal/cert -count=1`
- `go vet ./...`
- `GOOS=windows GOARCH=amd64 go build -o /tmp/mcc-windows-amd64.exe ./cmd/server`
- `GOOS=windows GOARCH=amd64 go test -c -o /tmp/bootstrap-windows-amd64.test.exe ./internal/bootstrap`
- `GOOS=darwin GOARCH=arm64 go build -o /tmp/mcc-darwin-arm64 ./cmd/server`
- `GOOS=darwin GOARCH=arm64 go test -c -o /tmp/bootstrap-darwin-arm64.test ./internal/bootstrap`
- `git diff --check`

### 第六轮结论

第六轮 Node CA 异常 marker 的 Medium 问题已在代码和回归测试中解决；自动化验证套件已通过，本次自审未发现新的可直接利用安全缺陷。Linux 重启 shell/长对话原生验证仍待完成，未声明为已完成。

## 第七轮独立复审 — 2026-07-13

### 已确认解决

- 第六轮指出的 Node CA 异常 marker 问题已解决。`profileHasNodeCAKeyOutsideMCCBlock` 现在返回 `(bool, error)`，`scanPOSIXProfilesForCustomValue` 与 `writePOSIXProfileNodeCA` 都会在 `launchctl` 或 profile 写入前传播该错误。
- 回归测试覆盖未闭合 Node CA begin、孤立 end、嵌套 begin、嵌入 marker 文本、写入路径字节不变，以及 Darwin 预检查阻止 `launchctl`。
- 第五轮共用结束 marker 修复在 Node CA 与 SSL 两类管理块排列中仍保持有效。

### 尚存问题

1. **Medium：Node CA POSIX 扫描仍漏检导出声明和后续 mutation/unset 形式。**
   - `internal/bootstrap/adapters.go:1074` 的 `profileHasNodeCAKeyOutsideMCCBlock` 对 POSIX shell 仍只识别 `export NODE_EXTRA_CA_CERTS=...`，没有复用 `SSL_CERT_FILE` 已经使用的更严格解析辅助函数。
   - 临时审查测试确认：`declare -x NODE_EXTRA_CA_CERTS=/custom/company-ca.crt`、`typeset -x NODE_EXTRA_CA_CERTS=/custom/company-ca.crt`、`unset NODE_EXTRA_CA_CERTS`、`export -n NODE_EXTRA_CA_CERTS` 都会返回 `(false, nil)`。
   - 完整写入路径的临时测试也确认：当 profile 中已有合法 MCC Node CA 块，后面跟着 `unset NODE_EXTRA_CA_CERTS` 时，写入函数返回成功并保留后续 unset。shell 启动时后续行会清除变量，导致 mcc 误报 Node CA 持久化就绪，但实际环境未就绪。
   - 修复要求：复用或泛化现有 `parsePOSIXExportedAssignment` 与 `posixMutatesEnvKey` 逻辑来处理 `NODE_EXTRA_CA_CERTS`，并补充 `declare -x`、`typeset -x`、`unset`、`export -n` 及失败路径 profile 字节不变测试。

### 验证

- `go test ./internal/bootstrap -count=1`
- `go test ./... -count=1`
- `go test -race ./internal/bootstrap ./internal/cert -count=1`
- `go vet ./...`
- `GOOS=windows GOARCH=amd64 go build -o /tmp/mcc-windows-amd64.exe ./cmd/server`
- `GOOS=windows GOARCH=amd64 go test -c -o /tmp/bootstrap-windows-amd64.test.exe ./internal/bootstrap`
- `GOOS=darwin GOARCH=arm64 go build -o /tmp/mcc-darwin-arm64 ./cmd/server`
- `GOOS=darwin GOARCH=arm64 go test -c -o /tmp/bootstrap-darwin-arm64.test ./internal/bootstrap`
- `git diff --check`

### 第七轮结论

暂不批准合并。第六轮异常 marker 问题已修复，但 Node CA profile 持久化仍存在可复现的 POSIX declaration 与 mutation 形式 fail-open 路径。Linux 重启 shell/长对话原生验证仍待完成，未声明为已完成。

## 第七轮修复结论 — 2026-07-13

### 已确认解决

- `profileHasNodeCAKeyOutsideMCCBlock` 现在为 `NODE_EXTRA_CA_CERTS` 复用既有 POSIX helper：`posixMutatesEnvKey` 检测精确 key 的 unset/unexport 形式，`parsePOSIXExportedAssignment` 检测裸赋值、`export` 赋值和声明导出赋值。
- 回归测试覆盖 `declare -x NODE_EXTRA_CA_CERTS=...`、`typeset -x NODE_EXTRA_CA_CERTS=...`、`unset NODE_EXTRA_CA_CERTS`、`export -n NODE_EXTRA_CA_CERTS`，以及合法 Node CA 管理块后跟 `unset NODE_EXTRA_CA_CERTS` 的写入路径。
- 写入路径回归要求返回 `ErrUserCustomValue` 且 profile 字节完全不变，因此后续 mutation 不会在持久化报告成功后继续生效。

### 验证

- `go test ./internal/bootstrap -count=1`
- `go test ./... -count=1`
- `go test -race ./internal/bootstrap ./internal/cert -count=1`
- `go vet ./...`
- `GOOS=windows GOARCH=amd64 go build -o /tmp/mcc-windows-amd64.exe ./cmd/server`
- `GOOS=windows GOARCH=amd64 go test -c -o /tmp/bootstrap-windows-amd64.test.exe ./internal/bootstrap`
- `GOOS=darwin GOARCH=arm64 go build -o /tmp/mcc-darwin-arm64 ./cmd/server`
- `GOOS=darwin GOARCH=arm64 go test -c -o /tmp/bootstrap-darwin-arm64.test ./internal/bootstrap`
- `git diff --check`

### 第七轮结论

第七轮 Node CA POSIX declaration 与 mutation 的 Medium 问题已在代码和回归测试中解决；自动化验证套件已通过，本次自审未发现新的可直接利用安全缺陷。Linux 重启 shell/长对话原生验证仍待完成，未声明为已完成。

## 第八轮独立复审 — 2026-07-13

### 已确认解决

- 第七轮指出的 POSIX declaration 与 mutation 问题已解决。`profileHasNodeCAKeyOutsideMCCBlock` 现在为 `NODE_EXTRA_CA_CERTS` 复用 `posixMutatesEnvKey` 与 `parsePOSIXExportedAssignment`，因此 POSIX shell 下的 `declare -x`、`typeset -x`、`unset`、`export -n` 已覆盖。
- 合法 Node CA 管理块后跟 `unset NODE_EXTRA_CA_CERTS` 的写入路径回归现在会返回 `ErrUserCustomValue`，并保持 profile 字节不变。
- 前几轮的共用 marker/idempotency 与异常 marker 回归仍保持覆盖。

### 尚存问题

1. **Medium：Node CA fish 扫描仍漏检后续 erase/unexport mutation。**
   - `internal/bootstrap/adapters.go:1074` 的 `profileHasNodeCAKeyOutsideMCCBlock` 在 fish 分支仍只调用 `parseFishExportLine`。与 SSL scanner 不同，它没有调用 `fishMutatesEnvKey(trimmed, "NODE_EXTRA_CA_CERTS")`。
   - 临时写入路径审查测试已复现：fish profile 中有合法 MCC Node CA 块，后面跟着 `set -e NODE_EXTRA_CA_CERTS` 时，函数返回成功并保留后续 erase 命令。fish 启动时后续命令会清除变量，导致 mcc 误报 Node CA 持久化就绪，但实际环境未就绪。
   - 修复要求：对 `NODE_EXTRA_CA_CERTS` 的 fish 分支镜像 SSL fish 路径，在 `parseFishExportLine` 前先调用 `fishMutatesEnvKey`。补充 `set -e NODE_EXTRA_CA_CERTS`、`set --erase NODE_EXTRA_CA_CERTS` 的 scanner 回归，以及合法管理块后跟 fish erase/unexport 时失败且 profile 字节不变的写入路径测试。

### 验证

- `go test ./internal/bootstrap -count=1`
- `go test ./... -count=1`
- `go test -race ./internal/bootstrap ./internal/cert -count=1`
- `go vet ./...`
- `GOOS=windows GOARCH=amd64 go build -o /tmp/mcc-windows-amd64.exe ./cmd/server`
- `GOOS=windows GOARCH=amd64 go test -c -o /tmp/bootstrap-windows-amd64.test.exe ./internal/bootstrap`
- `GOOS=darwin GOARCH=arm64 go build -o /tmp/mcc-darwin-arm64 ./cmd/server`
- `GOOS=darwin GOARCH=arm64 go test -c -o /tmp/bootstrap-darwin-arm64.test ./internal/bootstrap`
- `git diff --check`

### 第八轮结论

暂不批准合并。第七轮 POSIX 问题已修复，但 Node CA profile 持久化仍存在可复现的 fish erase/unexport fail-open 路径。Linux 重启 shell/长对话原生验证仍待完成，未声明为已完成。

## 第八轮修复结论 — 2026-07-13

### 已确认解决

- `profileHasNodeCAKeyOutsideMCCBlock` 现在与 SSL fish scanner 对齐，在 `parseFishExportLine` 前先调用 `fishMutatesEnvKey(trimmed, "NODE_EXTRA_CA_CERTS")`。
- 回归测试覆盖 `set -e NODE_EXTRA_CA_CERTS`、`set --erase NODE_EXTRA_CA_CERTS`、`set --unexport NODE_EXTRA_CA_CERTS`，以及合法 fish Node CA 管理块后跟 `set -e NODE_EXTRA_CA_CERTS` 的写入路径。
- 写入路径回归要求返回 `ErrUserCustomValue` 且 profile 字节完全不变，因此后续 fish erase/unexport 不会在持久化报告成功后继续生效。

### 验证

- `go test ./internal/bootstrap -count=1`
- `go test ./... -count=1`
- `go test -race ./internal/bootstrap ./internal/cert -count=1`
- `go vet ./...`
- `GOOS=windows GOARCH=amd64 go build -o /tmp/mcc-windows-amd64.exe ./cmd/server`
- `GOOS=windows GOARCH=amd64 go test -c -o /tmp/bootstrap-windows-amd64.test.exe ./internal/bootstrap`
- `GOOS=darwin GOARCH=arm64 go build -o /tmp/mcc-darwin-arm64 ./cmd/server`
- `GOOS=darwin GOARCH=arm64 go test -c -o /tmp/bootstrap-darwin-arm64.test ./internal/bootstrap`
- `git diff --check`

### 第八轮结论

第八轮 Node CA fish erase/unexport 的 Medium 问题已在代码和回归测试中解决；自动化验证套件已通过，本次自审未发现新的可直接利用安全缺陷。Linux 重启 shell/长对话原生验证仍待完成，未声明为已完成。

## 第九轮独立复审 — 2026-07-13

### 已确认解决

- 第八轮指出的 fish erase/unexport 问题已解决。`profileHasNodeCAKeyOutsideMCCBlock` 现在会在解析 fish export 前先调用 `fishMutatesEnvKey(trimmed, "NODE_EXTRA_CA_CERTS")`。
- 临时审查测试确认：`set -e NODE_EXTRA_CA_CERTS`、`set --erase NODE_EXTRA_CA_CERTS`、`set --unexport NODE_EXTRA_CA_CERTS` 现在都会 fail-closed；合法 fish Node CA 管理块后接 `set -e NODE_EXTRA_CA_CERTS` 时会返回 `ErrUserCustomValue` 且不修改 profile。
- 之前的 POSIX declaration/mutation、异常 marker、共用 marker/idempotency、marker symlink 和证书链重签路径仍由自动化测试覆盖。

### 审查发现

本轮未发现新的可复现功能逻辑缺陷或可直接利用安全缺陷。

### 验证

- `go test ./internal/bootstrap -count=1`
- `go test ./... -count=1`
- `go test -race ./internal/bootstrap ./internal/cert -count=1`
- `go vet ./...`
- `GOOS=windows GOARCH=amd64 go build -o /tmp/mcc-windows-amd64.exe ./cmd/server`
- `GOOS=windows GOARCH=amd64 go test -c -o /tmp/bootstrap-windows-amd64.test.exe ./internal/bootstrap`
- `GOOS=darwin GOARCH=arm64 go build -o /tmp/mcc-darwin-arm64 ./cmd/server`
- `GOOS=darwin GOARCH=arm64 go test -c -o /tmp/bootstrap-darwin-arm64.test ./internal/bootstrap`
- `git diff --check`

### 第九轮结论

从本轮复审覆盖的功能逻辑与安全范围看，可以合并。Linux 重启 shell/长对话原生验证仍待完成，未声明为已完成。
