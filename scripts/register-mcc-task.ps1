<#
.SYNOPSIS
Register mcc as a portable per-user logon scheduled task.

.DESCRIPTION
Registers a scheduled task named mcc-autostart in the directory containing this
script. The task runs start-mcc.ps1 for the current user at logon with Limited
run level. Run this script from the same directory as mcc.exe.

.PARAMETER Password
Optional fixed admin password. If provided, the password is stored in the
scheduled task arguments and can be read by local administrators. Omit this
parameter to let mcc generate a random password and print it to stdout logs.

.PARAMETER Force
Replace an existing task with the same name.
#>
param(
    [string]$Password = "",
    [switch]$Force
)

$ErrorActionPreference = "Stop"

$MccDir = $PSScriptRoot
if (-not $MccDir) { $MccDir = Split-Path -Parent $MyInvocation.MyCommand.Path }
if (-not $MccDir) { throw "Unable to locate script directory. Run this file as a .ps1 script." }

$StartScript = Join-Path $MccDir "start-mcc.ps1"
$MccExe = Join-Path $MccDir "mcc.exe"

$Pwsh = (Get-Command pwsh -ErrorAction SilentlyContinue).Source
if (-not $Pwsh -or -not (Test-Path $Pwsh)) {
    $Pwsh = (Get-Command powershell.exe -ErrorAction SilentlyContinue).Source
}
if (-not $Pwsh -or -not (Test-Path $Pwsh)) {
    throw "PowerShell executable not found."
}

if (-not (Test-Path $StartScript)) { throw "start-mcc.ps1 not found: $StartScript" }
if (-not (Test-Path $MccExe)) { throw "mcc.exe not found: $MccExe" }

$WinIdentity = [Security.Principal.WindowsIdentity]::GetCurrent()
$IsAdmin = (New-Object Security.Principal.WindowsPrincipal($WinIdentity)).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
if (-not $IsAdmin) {
    Write-Host "Administrator privileges are required. Restarting with elevation..." -ForegroundColor Yellow
    $ArgList = @("-NoProfile", "-ExecutionPolicy", "Bypass", "-File", "`"$PSCommandPath`"")
    if (-not [string]::IsNullOrWhiteSpace($Password)) {
        $ArgList += "-Password"
        $ArgList += "`"$Password`""
    }
    if ($Force) { $ArgList += "-Force" }
    Start-Process $Pwsh -Verb RunAs -Wait -ArgumentList $ArgList
    exit
}

if (-not [string]::IsNullOrWhiteSpace($Password)) {
    Write-Host "Warning: password is stored in the scheduled task arguments and may be readable by local administrators." -ForegroundColor Yellow
}

$UserId = "$env:USERDOMAIN\$env:USERNAME"
$TaskName = "mcc-autostart"
$TaskArgs = "-NoProfile -ExecutionPolicy Bypass -File `"$StartScript`""
if (-not [string]::IsNullOrWhiteSpace($Password)) {
    $TaskArgs += " -Password `"$Password`""
}

$Action = New-ScheduledTaskAction -Execute $Pwsh -Argument $TaskArgs -WorkingDirectory $MccDir
$Trigger = New-ScheduledTaskTrigger -AtLogOn -User $UserId
$Settings = New-ScheduledTaskSettingsSet -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries -ExecutionTimeLimit ([TimeSpan]::Zero)
$Principal = New-ScheduledTaskPrincipal -UserId $UserId -LogonType Interactive -RunLevel Limited

$Existing = Get-ScheduledTask -TaskName $TaskName -ErrorAction SilentlyContinue
if ($Existing -and -not $Force) {
    Write-Host "Task '$TaskName' already exists. Use -Force to replace it." -ForegroundColor Yellow
    Write-Host ("  UserId  : " + $Existing.Principal.UserId)
    Write-Host ("  Execute : " + $Existing.Actions[0].Execute)
    Write-Host ("  Argument: " + $Existing.Actions[0].Arguments)
    return
}
if ($Existing) {
    Unregister-ScheduledTask -TaskName $TaskName -Confirm:$false
}

Register-ScheduledTask -TaskName $TaskName -Action $Action -Trigger $Trigger `
    -Settings $Settings -Principal $Principal `
    -Description "mcc proxy autostart (portable)" | Out-Null

$Task = Get-ScheduledTask -TaskName $TaskName
Write-Host ""
Write-Host "mcc autostart task registered." -ForegroundColor Green
Write-Host ("TaskName : " + $Task.TaskName)
Write-Host ("State    : " + $Task.State)
Write-Host ("UserId   : " + $Task.Principal.UserId)
Write-Host ("RunLevel : " + $Task.Principal.RunLevel)
Write-Host ("Execute  : " + $Task.Actions[0].Execute)
Write-Host ("Argument : " + $Task.Actions[0].Arguments)
Write-Host ("WorkDir  : " + $Task.Actions[0].WorkingDirectory)
Write-Host "Rollback: schtasks /Delete /TN mcc-autostart /F"
