# Research Backlog（研究积压）

> 记录探索性研究的结果和想法。不是承诺，而是备忘。
> 可在重规划阶段评估是否纳入路线图。

## 研究条目格式

```markdown
### [研究主题]
- **日期**: YYYY-MM-DD
- **状态**: 探索中 / 已完成 / 已纳入路线图 / 已废弃
- **背景**: 为什么做这个研究
- **结论**: 研究结果摘要
- **推荐**: 建议的下一步行动
```

---

## 研究列表

### 第三方供应商非标准 Content Block 兼容性研究

- **日期**: 2026-05-19
- **状态**: 已纳入路线图
- **背景**: 使用 kimi-k2.6 作为 Claude Code 后端时，当启用了 deferred tool（如 WebSearch），请求返回 400 Invalid request Error。GLM-5.1 等宽容型 API 则正常工作。
- **结论**: 见 [rectifier-pattern3-generic-bad-request.md](rectifier-pattern3-generic-bad-request.md)
- **推荐**: 后续考虑主动式清洗（在 transformRequest 阶段就剥离），避免第一次 400 的往返延迟
