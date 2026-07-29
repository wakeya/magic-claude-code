# SSRF DNS 重绑定修复规格

后端：`internal/providerquota/llm_client.go` / 最后更新：2026-07-28 / 状态：draft / 进度：0 / 2 planned

## 整体分析

复审发现 5c58e36 的 SSRF 修复有 TOCTOU 残余：`isInternalHost` 只在请求前对 `endpointURL.Hostname()` 检查一次，`client.Do` 用默认 Transport 再解析/拨号，存在 DNS 重绑定绕过（第一次解析公网 IP 通过检查，拨号时再解析到内网）。本规格把校验下沉到 `http.Transport.DialContext`，在每次拨号解析出的 IP 上拒绝 internal，并用已校验 IP 直连（消除二次解析）。顺手清复审发现的 trailing whitespace。

## 任务详情

### 任务 1：LLMClient DialContext 级 IP 校验

**文件：`internal/providerquota/llm_client.go`**

1. `NewLLMClient` 的 `http.Client` 增加自定义 `http.Transport`（或基于 `http.DefaultTransport.(*http.Transport)` clone），设置 `DialContext`：
   ```go
   base := http.DefaultTransport.(*http.Transport).Clone()
   base.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
       host, port, err := net.SplitHostPort(addr)
       if err != nil { return nil, err }
       ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
       if err != nil { return nil, err }
       if len(ips) == 0 { return nil, fmt.Errorf("no IP resolved for %s", host) }
       for _, ip := range ips {
           if isInternalIP(ip.IP) {
               return nil, fmt.Errorf("refusing to dial internal address %s", ip.IP)
           }
       }
       // 用已校验的 IP 直连，避免二次 DNS 解析（TOCTOU）
       dialer := &net.Dialer{Timeout: 30 * time.Second}
       return dialer.DialContext(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
   }
   ```
2. 保留现有请求前 `isInternalHost(endpointURL.Hostname())` 检查（快路径，明显内网直接拒，不等 DNS）——但**真正的安全边界是 DialContext**（防 DNS 重绑定）。
3. 若已有 `isInternalHost(host string)`（基于主机名/DNS 解析），新增或复用 `isInternalIP(ip net.IP) bool`（基于 IP：loopback / private 10.0.0.0/8、172.16.0.0/12、192.168.0.0/16 / link-local 169.254.0.0/16 含 metadata / unspecified 0.0.0.0、::1、fc00::/7、fe80::/10）。若 `token_plan.go` 或 `internal/proxy` 已有等价函数，复用并导出/共享。

**测试：`llm_client_test.go`**

4. `TestLLMClientRejectsDNSRebinding`：用自定义 `http.Transport` + mock resolver（或 httptest server 配合 `net.Resolver` 注入），让 DNS 第一次解析返回公网 IP、第二次（拨号）返回 127.0.0.1 → 断言 `Call` 返回 `invalid_config`（或网络错），**不拨号到 127.0.0.1**。
   - 若 mock resolver 注入复杂，可用更简单的回归测试：直接构造 endpoint host 解析到内网（如 `localhost` → 127.0.0.1，已被现有 RejectsLoopback 覆盖）；再加一个"DialContext 拒绝内网 IP"的单测：直接调用 transport 的 DialContext 传内网 addr → 拒绝。
5. 现有 `TestLLMClientRejectsLoopback` / `RejectsMetadata` / `RejectsRedirect` 必须仍过（回归）。

### 任务 2：清 trailing whitespace

**文件：`sdd-docs/features/2026-07-27-custom-script-ai-generate/spec_ZH.md:222`**

1. 去掉该行行尾空格（`git diff --check` 干净）。

## 验证

- [ ] `go test -v -race ./internal/providerquota/ -run LLMClient` 全绿（含新 DNS 重绑定测试 + 现有回归）。
- [ ] `go test ./...` + `go vet ./...` 全绿。
- [ ] `git diff --check main..HEAD` 干净（无 trailing whitespace）。
- [ ] commit: `fix(llm-client): SSRF DNS rebinding via DialContext IP check`。
