# 反应式供应商兼容性错误恢复 — 状态

**功能：** 反应式供应商兼容性错误恢复
**当前状态：** shipped
**创建：** 2026-05-15
**最后更新：** 2026-05-21
**负责人：** 本地项目维护者

## 生命周期

```text
draft -> approved -> planned -> implementing -> implemented -> validating -> validated -> shipped
```

当前位置：

```text
implemented
```

## 概要

通用反应式错误恢复机制。当供应商返回 HTTP 400 且匹配可识别的错误模式（tool 校验或 thinking/签名）时，代理清理请求体并重试一次。不对任何供应商做主动请求修改。普通 `invalid_request_error` 不单独触发自动恢复。

## 设计参考

参考了 cc-switch（farion1231 开发的 Rust 代理）：
- cc-switch 对兼容 Anthropic 格式的供应商（GLM、MiniMax、Kimi、DeepSeek、Qwen）做透明透传。
- cc-switch 使用反应式 "thinking rectifier" 从签名/thinking 错误中恢复。
- cc-switch **不**为特定供应商主动修改请求体。

我们的方案在此基础上增加了 tool 定义清理。

## 转换检查清单

转为 `approved` 需要：

1. 错误模式目录经过审阅和接受。
2. 清理策略经过审阅和接受。
3. 单次重试限制经过接受。

转为 `planned` 需要：

1. `validation.md` 中的验收标准已确定。
2. 实现任务顺序已确认。

转为 `implementing` 需要：

1. 用户要求开始实现。

## 重新评估触发条件

出现以下情况时重新评估：

1. 新供应商返回 400 但错误模式不在目录中。
2. 清理策略导致 tool 调用准确度下降。
3. 重试延迟变得不可接受（目标：额外 <2s）。
4. 某供应商持续在首次请求失败，需要主动清理。
