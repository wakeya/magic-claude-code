# 发布包脚本说明

正常情况下优先直接运行 `mcc` / `mcc.exe`，让内置 bootstrap 自动完成 hosts、系统 CA 信任和客户端环境变量配置。下面这些脚本用于自动配置失败、Docker 宿主机预配置、Windows 后台启动/登录自启，或需要单独配置 hosts/CA 的场景。

## Linux / macOS

发布包包含：

- `setup-host.sh`
- `docker-host-helper.sh`

一键配置 hosts 和 CA 信任：

```bash
sudo ./setup-host.sh
```

只配置 hosts：

```bash
sudo ./setup-host.sh hosts
```

只安装 CA：

```bash
sudo ./setup-host.sh trust
```

安装 CA 后，脚本会输出客户端环境变量建议。Linux 上 `SSL_CERT_FILE` 必须指向完整系统 CA bundle，例如 `/etc/ssl/certs/ca-certificates.crt`，不要指向单个 `data/ca.crt`。

`docker-host-helper.sh` 用于 Docker 场景。宿主机先运行 `setup-host.sh` 完成配置，然后把 helper 挂载进容器并通过 `MCC_HOST_HELPER` 指向它，让容器检测宿主机 hosts/CA 状态。

## Windows

发布包包含：

- `setup-host.ps1`
- `start-mcc.ps1`
- `stop-mcc.ps1`
- `register-mcc-task.ps1`

管理员 PowerShell 中配置 hosts 和 CA 信任：

```powershell
.\setup-host.ps1
```

只配置 hosts：

```powershell
.\setup-host.ps1 -Action hosts
```

只安装 CA：

```powershell
.\setup-host.ps1 -Action trust
```

后台启动 / 停止：

```powershell
.\start-mcc.ps1
.\stop-mcc.ps1
```

注册当前用户登录时自动启动：

```powershell
.\register-mcc-task.ps1 -Force
```

默认不固定后台管理密码，由 `mcc.exe` 随机生成并写入 stdout 日志。若显式传入 `-Password`，密码会出现在计划任务参数中，本机管理员可读取。
