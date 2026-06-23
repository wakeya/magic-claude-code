# ================================================================
# MCC Proxy 宿主机一键配置脚本 (Windows) | Host Setup Script
# ================================================================
#
# 自动完成透明模式所需的宿主机配置：
#   1. 在 hosts 中添加 api.anthropic.com → 127.0.0.1
#   2. 将 MCC 生成的 CA 证书安装到 Windows 根证书存储
#
# 用法（以管理员身份运行 PowerShell）:
#   .\setup-host.ps1                          # 一键完成 hosts + CA
#   .\setup-host.ps1 -Action hosts            # 只改 hosts
#   .\setup-host.ps1 -Action trust            # 只装 CA
#   .\setup-host.ps1 -CertPath C:\path\ca.crt # 指定 CA 路径
#
# ================================================================

param(
    [ValidateSet('all', 'hosts', 'trust')]
    [string]$Action = 'all',

    [string]$CertPath = '',

    [string]$Domain = 'api.anthropic.com',

    [string]$IP = '127.0.0.1'
)

$ErrorActionPreference = 'Stop'

function Write-Info  { param([string]$Msg) Write-Host "[INFO] $Msg" -ForegroundColor Green }
function Write-Warn2 { param([string]$Msg) Write-Host "[WARN] $Msg" -ForegroundColor Yellow }
function Write-Err   { param([string]$Msg) Write-Host "[ERROR] $Msg" -ForegroundColor Red }

# ─── 配置标记（供 Docker 容器内的 helper 检测宿主机状态）───
# CA 标记额外写入证书 SHA256 fingerprint（小写），bootstrap 据此检测证书轮换。
function Write-Marker {
    param(
        [string]$Name,
        [string]$CertPath
    )
    $dataDir = Join-Path $PSScriptRoot "..\data"
    if (-not (Test-Path $dataDir)) { return }
    try {
        $obj = [ordered]@{ action = $Name }
        if ($Name -eq 'ca-trust-installed' -and $CertPath -and (Test-Path $CertPath)) {
            $hash = (Get-FileHash $CertPath -Algorithm SHA256).Hash.ToLower()
            $obj['fingerprint'] = $hash
        }
        $obj['os'] = 'windows'
        $obj['timestamp'] = (Get-Date -Format 'o')
        $obj | ConvertTo-Json -Compress |
            Set-Content -Path (Join-Path $dataDir ".$Name") -Encoding UTF8 -ErrorAction SilentlyContinue
    } catch { }
}

# ─── 管理员权限检查 ───
$principal = New-Object Security.Principal.WindowsPrincipal([Security.Principal.WindowsIdentity]::GetCurrent())
$isAdmin = $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)

if (-not $isAdmin) {
    Write-Warn2 "需要管理员权限，正在以管理员身份重新启动..."
    $argList = @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "`"$PSCommandPath`"")
    if ($Action -ne 'all') { $argList += "-Action"; $argList += $Action }
    if ($CertPath)         { $argList += "-CertPath"; $argList += "`"$CertPath`"" }
    if ($Domain -ne 'api.anthropic.com') { $argList += "-Domain"; $argList += $Domain }
    if ($IP -ne '127.0.0.1')             { $argList += "-IP"; $argList += $IP }
    Start-Process PowerShell -Verb RunAs -ArgumentList $argList
    exit
}

# ─── hosts 配置 ───
function Set-HostsEntry {
    $hostsPath = "$env:WINDIR\System32\drivers\etc\hosts"

    Write-Info "配置 hosts: $IP → $Domain"

    $content = ''
    if (Test-Path $hostsPath) {
        $content = Get-Content $hostsPath -Raw -ErrorAction SilentlyContinue
    }

    $pattern = "(?m)^\s*([^\s#]\S*)\s+$([regex]::Escape($Domain))\b"

    if ($content -match $pattern) {
        $existingIP = $Matches[1]
        if ($existingIP -eq $IP) {
            Write-Info "hosts 已包含正确映射，跳过。"
            Write-Marker "hosts-configured"
            return
        }
        Write-Warn2 "hosts 中已有 $Domain → $existingIP，更新为 $IP"
        $content = $content -replace $pattern + ".*`r?`n?", ""
    }

    $entry = "$IP $Domain"
    if (-not $content.EndsWith("`n") -and $content -ne '') {
        $content += "`n"
    }
    $content += "$entry`n"
    Set-Content -Path $hostsPath -Value $content -Encoding ASCII
    Write-Info "hosts 配置完成。"
    Write-Marker "hosts-configured"
}

# ─── CA 证书查找 ───
function Find-Cert {
    if ($CertPath -and (Test-Path $CertPath)) {
        return $CertPath
    }
    $candidates = @(
        "$PSScriptRoot\..\data\ca.crt"
        "$PSScriptRoot\data\ca.crt"
        ".\data\ca.crt"
        ".\ca.crt"
    )
    foreach ($c in $candidates) {
        if (Test-Path $c) { return $c }
    }
    return $null
}

# ─── CA 信任安装 ───
function Install-CA {
    $cert = Find-Cert
    if (-not $cert) {
        Write-Err "找不到 CA 证书文件。请用 -CertPath C:\path\to\ca.crt 指定路径。"
        exit 1
    }

    Write-Info "安装 CA 证书: $cert"

    if (-not (Test-Path $cert)) {
        Write-Err "证书文件不存在: $cert"
        exit 1
    }

    # certutil 是 Windows 自带工具，无需安装
    $result = & certutil -addstore -f ROOT $cert 2>&1
    if ($LASTEXITCODE -ne 0) {
        Write-Err "CA 安装失败: $result"
        exit 1
    }

    Write-Info "CA 已安装到 Windows 根证书存储。"
    Write-Marker "ca-trust-installed" -CertPath $cert
    Write-Info "提示: 如果使用 Node.js (Claude Code)，还需设置环境变量:"
    Write-Host "  setx NODE_EXTRA_CA_CERTS `"$cert`""
}

# ─── 主逻辑 ───
Write-Info "MCC Proxy 宿主机配置 | OS: Windows"
Write-Host ""

switch ($Action) {
    'hosts' { Set-HostsEntry }
    'trust' { Install-CA }
    'all'   {
        Set-HostsEntry
        Write-Host ""
        Install-CA
        Write-Host ""
        Write-Info "全部配置完成！现在可以启动 Claude Code 了。"
    }
}
