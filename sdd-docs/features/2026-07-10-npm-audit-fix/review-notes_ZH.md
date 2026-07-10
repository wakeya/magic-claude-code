# NPM Audit 漏洞修复审查记录

日期：2026-07-10  
审查者：Codex 与 Claude Code

## 审查范围

审查 amend 后的 `fix/npm-audit` 分支 tip 相对 `main` 的变更，包括仅更新 lockfile 的依赖解析、双语 spec、被 Git 跟踪并嵌入 Go 二进制的前端产物、ECharts 集成、Vite/发布构建链路，以及反馈中的 updater 测试失败。

## 主要发现与处理

1. 最初的嵌入式前端不一致问题已解决。
   - 证据：`internal/frontend/embed.go` 嵌入 `dist/*`，而 `make build` 和 `make run` 直接执行 Go。amend 后的分支 tip 已删除旧 hash 资源并提交 Vite 8.1.4/ECharts 6.1.0 资源集。新鲜执行 `npm ci` 后再次运行 `npm run build` 没有产生 Git diff，证明提交产物可由 lockfile 确定性复现。
   - 处理：已解决。干净检出后的 `make build`/`make run` 现在会嵌入升级后的前端。

2. lockfile 升级本身已解决报告的告警，并且可复现。
   - 证据：官方告警范围分别为 ECharts `<6.1.0`、Vite `8.0.0` 至 `8.0.15`；lockfile 解析为 `echarts@6.1.0`、`zrender@6.1.0`、`vite@8.1.4`。新鲜执行 `npm ci` 和 `npm audit --json` 均报告 0 漏洞；`npm test` 158/158 通过；Vite 8.1.4 的 `npm run build` 成功；`go test ./...` 通过。
   - 处理：可接受。`package.json` 的原 caret 范围保持不变是有效做法，因为 `package-lock.json` 已固定安装版本，且 `npm ci` 成功复现。

3. spec 此前只完成了部分同步，仍保留互相矛盾的指引。
   - 证据：Checklist、musl binding 行和实际实现结果已经修正，但风险总结、非目标以及任务 1/2/4 仍写着不提交 `dist/`、且预期只处理 `package.json` 与 `package-lock.json`。这些内容直接违背了已修正的”被跟踪并嵌入的 `dist/` 必须提交”约束。
   - 处理：已解决并经复审确认。两份 spec 于 2026-07-10 全文重写，所有有效约束均声明 `dist/` 必须提交、`package.json` 未变；机械扫描未发现矛盾指令。

4. 图表运行时渲染仍存在明确的验证缺口。
   - 证据：单测只断言懒加载源码形态，生产构建也已成功，但尚未在浏览器检查升级后的七序列图表、tooltip/legend 交互和空状态 graphic。
   - 处理：发版前完成 spec 已记录的浏览器运行时检查。这是残余验证项，不是已复现的功能缺陷。

5. 反馈中的 updater 失败既是不可靠测试，也揭示了真实的既有 URL 脱敏缺陷，并非可以忽略的普通 flaky。
   - 证据：`main...fix/npm-audit` 在 `internal/updater` 下没有 diff。该测试会在校验 URL 前真实请求 `https://user:pass@example.com?token=secret`。正常执行 `go test ./... -count=1` 本次通过，但强制使用不可达 HTTPS 代理后，测试会稳定失败，因为 `http.Client.Do` 返回的错误包含 `?token=secret`。
   - 处理：该代码与 `main` 相同，因此不阻断本次纯依赖变更；但必须另行修复安全错误路径并把测试改为 hermetic。不能只把它记录成以后忽略的 flaky；返回或记录网络层错误前必须统一脱敏。

## 最终审查结论

依赖与嵌入产物实现正确：audit 已清零，前端测试和构建通过，干净重建可以逐字节复现已提交的 ECharts 6.1 资源。本次 npm-audit 实现中未发现新的功能或安全缺陷。两份 spec 在"`dist/` 必须提交、`package.json` 未变"上已自洽，先前的文档阻断已解决。该分支可以推送并创建 PR。浏览器运行时检查仍是发版门禁。updater URL 泄露不由本次 diff 引入，但必须作为独立安全缺陷跟踪。

## 残余说明

- 当前应用注册的是 `LineChart`，并非告警涉及的 `LinesChart`，因此现有图表配置无法触达该 ECharts XSS 路径；升级仍是正确的纵深防御。
- lockfile 将升级后的 Vite/Rolldown 子树下载源从 `registry.npmmirror.com` 改为 `registry.npmjs.org`。本次安装成功；这是分发/维护注意事项，不是安全发现。
- 不要在即将 amend 的提交内部记录分支 tip 的字面 commit hash，因为 amend 必然改变该 hash；应写成“当前分支 tip”。
