$ErrorActionPreference = "Stop"
$Root = Split-Path -Parent $MyInvocation.MyCommand.Path
$PidPath = Join-Path $Root "mcc.pid"

if (-not (Test-Path $PidPath)) {
    Write-Host "mcc.pid does not exist. Check Task Manager for any remaining mcc.exe process."
    exit 0
}

$MccProcessId = (Get-Content $PidPath | Select-Object -First 1).Trim()
if ([string]::IsNullOrWhiteSpace($MccProcessId)) {
    Remove-Item $PidPath -Force
    Write-Host "mcc.pid was empty and has been removed."
    exit 0
}

$Process = Get-Process -Id ([int]$MccProcessId) -ErrorAction SilentlyContinue
if ($null -eq $Process) {
    Remove-Item $PidPath -Force
    Write-Host "Process $MccProcessId does not exist. Removed mcc.pid."
    exit 0
}

Stop-Process -Id $Process.Id -Force
Remove-Item $PidPath -Force
Write-Host "mcc stopped. PID: $($Process.Id)"
