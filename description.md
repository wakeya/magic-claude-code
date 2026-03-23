# 问题

日志中存在该信息。 具体日志如下：

  2026-03-08T08:42:51.621Z [DEBUG] [ToolSearch:optimistic] disabled:
  ANTHROPIC_BASE_URL=<https://coding.dashscope.aliyuncs.com/apps/anthropic>
  is not a first-party Anthropic host

  ---
  影响说明

  原因

  您配置了 ANTHROPIC_BASE_URL 指向第三方 API
  端点（maas-api.ai-yuanjing.com），而不是 Anthropic 官方 API。Claude Code
  检测到这不是官方端点，因此禁用了 ToolSearch:optimistic 功能。

  什么是 ToolSearch:optimistic？

  这是 Claude Code 的一个优化功能，用于智能工具搜索：

- 在 standard 模式下，每次需要调用工具时都会等待确认
- 在 optimistic 模式下，可以预测性地搜索和调用工具，提高响应效率

  实际影响

  ┌──────────────┬──────────────────────────────────────────────────┐
  │     方面     │                       影响                       │
  ├──────────────┼──────────────────────────────────────────────────┤
  │ 核心功能     │ ✅ 不影响 - 所有工具调用仍正常工作               │
  ├──────────────┼──────────────────────────────────────────────────┤
  │ 工具搜索效率 │ ⚠️  轻微降低 - 可能需要更多轮次来确定使用哪些工具 │
  ├──────────────┼──────────────────────────────────────────────────┤
  │ 用户体验     │ ⚠️  轻微影响 - 首次调用工具时可能稍有延迟         │
  ├──────────────┼──────────────────────────────────────────────────┤
  │ 代理模式     │ ✅ 不影响 - Agentic 模式仍可正常使用             │
  └──────────────┴──────────────────────────────────────────────────┘

  结论

  这是一个安全设计决策，不是错误。当使用第三方 API 端点时，Claude Code
  会禁用某些优化功能以确保兼容性和稳定性。您的使用不会受到明显影响，只是少了一个
  性能优化特性。

ANTHROPIC_BASE_URL 官方内容你可以从 claude-code源码文件本地地址：/home/www/workspace/github/claude-code 获取，要实现的透明代理地址是可以配置的，提供前端配置页面，可通过浏览器直接打开进行透明代理的ANTHROPIC_BASE_URL设置，举例：ANTHROPIC_BASE_URL=<https://coding.dashscope.aliyuncs.com/apps/anthropic>

## 方案

根本解法：透明代理，让客户端以为它在和官方通信，要从根本上解决这个问题，最干净的方式是不设 ANTHROPIC_BASE_URL，而是在网络层做透明劫持。核心思路：
/etc/hosts: api.anthropic.com → 127.0.0.1
iptables:   127.0.0.1:443    → 127.0.0.1:8443
本地 nginx: TLS 终结（持有 api.anthropic.com 自签证书）→ 你的实际后端这样 Claude Code 二进制始终认为自己在和 api.anthropic.com 通信，JB() 自然返回 true，所有功能正常开启。TLS 这边需要额外做一步：生成一个本地 CA，签发 api.anthropic.com 的证书，然后通过 NODE_EXTRA_CA_CERTS 环境变量（写入 /etc/environment 即可一劳永逸）告诉 Node.js 信任这个 CA。这个方案的优点是对更新免疫——claude update 再怎么升级二进制，网络层的配置完全不受影响，不需要重新操作任何东西。

该方案使用go + gin + nginx + 本地CA自动签名，到期自动签名 + docker实现透明代理，docker容器尽量小，节约资源。
docker映射的代码路径为当前文件所在的目录，本机docker已安装。

## 受影响的端点

除了上面说的功能开关，二进制里还有约 50 处 api.anthropic.com 的引用。其中部分端点完全忽视ANTHROPIC_BASE_URL，永远硬编码打到官方：

| 端点 | 用途 |
|---|---|
|/api/claude_code/metricsOpenTelemetry  | 指标上报|
|/api/claude_code/organizations/metrics_enabled | 组织指标开关查询|
|/api/claude_cli_feedback| 用户反馈提交|
|/api/claude_code_shared_session_transcripts| 会话记录共享|
|/api/web/domain_info?domain=...W| ebFetch 爬取权限校验|

所以即使设置了 ANTHROPIC_BASE_URL，这几个接口的请求还是照常发往 Anthropic 官方服务器。

## 参考资料

claude-code源码文件本地地址：/home/www/workspace/github/claude-code
claude-code源码文件在线地址：<https://github.com/anthropics/claude-code>
互联网CA签名：<https://github.com/acmesh-official/acme.sh>
