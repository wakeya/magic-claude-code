# 全站主题系统改造状态

**功能：** 管理端前端 Light/Dark 主题系统
**当前状态：** shipped
**创建日期：** 2026-05-21
**最后更新：** 2026-05-21
**负责人：** 本地项目维护者

## 生命周期

```text
draft -> approved -> planned -> implementing -> implemented -> validating -> validated -> shipped
```

当前位置：

```text
validated
```

第一阶段会话记录页试点已实现。前端自动化验证已通过，容器化构建已成功重启，浏览器冒烟验证确认主题开关、Dark 模式持久化、切换主题时保持当前会话，以及 Dark 模式下清理命令弹窗可读性均正常。

第二阶段已完成全栈推广并通过验证。最终主题开关位于 `AppHeader.vue`，管理员主题偏好通过后端配置存储持久化，`localStorage` 继续作为启动和失败兜底，语义化主题 token 已覆盖 Dashboard 外壳、登录页、供应商区域、弹窗、会话记录页、消息块和代码块。

## 转段条件

进入 `approved` 需满足：

1. Light 和 Dark 的参考方向已确认。
2. 会话记录页试点范围已确认。
3. 显式主题开关行为已确认。

进入 `planned` 需满足：

1. `plan.md` 和 `plan_ZH.md` 已审阅。
2. 第一阶段实现文件和验证命令已确认。

进入 `implementing` 需满足：

1. 用户确认可以开始实现。
2. 当前工作区状态已检查。

第二阶段进入 `planned` 需满足：

1. `requirements.md` / `requirements_ZH.md` 中的第二阶段范围已审核。
2. `decisions.md` / `decisions_ZH.md` 中的后端持久化和 Header 入口位置决策已确认。
3. `plan.md` / `plan_ZH.md` 中的全栈实现任务已确认。

## 阻塞项

无。

## 重新审视触发条件

后续扩展到其他公开页面、增加 `system` 模式，或引入多管理员并需要按用户保存偏好时重新审视。
