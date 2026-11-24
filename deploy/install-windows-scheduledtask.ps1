# =============================================================================
# Windows 客户端：计划任务（每 30 分钟）—— 本机（Windows 11）即用此方案
# 以「当前用户」身份运行，无需密码交互；日志写 D:\work\logs。
# 前提：rclone 已装并加入 PATH（本机已装 v1.76，PATH 已写 HKCU）。
# =============================================================================
$ErrorActionPreference = 'Stop'

# ---------- 目录骨架 ----------
$WorkRoot   = 'D:\work'
$LocalDir   = Join-Path $WorkRoot 'workspace'
$TrashLocal = Join-Path $WorkRoot '_trash_local'
$StateDir   = Join-Path $WorkRoot '.bisync-state'
$LogDir     = Join-Path $WorkRoot 'logs'
foreach ($d in @($LocalDir,$TrashLocal,$StateDir,$LogDir)) { New-Item -ItemType Directory -Force -Path $d | Out-Null }

# OpenList 远端需先在 WebDAV/后台建好：
#   /backup/workspace   同步目标
#   /trash/workspace    垃圾桶（与 backup/workspace 同级，不在目标内）
# rclone.conf 放 %APPDATA%\rclone\rclone.conf（参考 deploy\rclone.conf.example）

# ---------- 同步封装脚本 ----------
$Runner = Join-Path $WorkRoot 'run-bisync.ps1'
@'
param([string]$Mode = 'sync')
$ErrorActionPreference = 'Stop'
$Local   = 'D:\work\workspace'
$Remote  = 'openlist:/backup/workspace'
$TrashL  = 'D:\work\_trash_local'
$TrashR  = 'openlist:/trash/workspace'
$State   = 'D:\work\.bisync-state'
$Log     = 'D:\work\logs\openlist-bisync.log'

$common = @(
  '--checksum',
  '--backup-dir1', $TrashL,
  '--backup-dir2', $TrashR,
  '--workdir', $State,
  '--recover',
  '--max-delete', '50',
  '--retries', '10', '--low-level-retries', '20',
  '--contimeout', '30s', '--timeout', '5m',
  '--transfers', '4', '--checkers', '8',
  '--log-file', $Log, '--log-level', 'INFO', '--stats', '5m'
)
$extra = switch ($Mode) {
  'resync'  { @('--resync') }
  'dry-run' { @('--dry-run') }
  default   { @() }
}
$p = Start-Process -FilePath 'rclone' `
  -ArgumentList (@('bisync', $Local, $Remote) + $common + $extra) `
  -NoNewWindow -PassThru -Wait
exit $p.ExitCode
'@ | Set-Content -Path $Runner -Encoding utf8

# ---------- 注册计划任务（每 30 分钟） ----------
$TaskName = 'OpenList-Bisync'
$action  = New-ScheduledTaskAction -Execute 'pwsh.exe' `
  -Argument "-NoProfile -ExecutionPolicy Bypass -File `"$Runner`" sync"
$trigger = New-ScheduledTaskTrigger -Once -At (Get-Date).Date.AddMinutes(5) `
  -RepetitionInterval (New-TimeSpan -Minutes 30)
$settings = New-ScheduledTaskSettingsSet -StartWhenAvailable -DontStopIfGoingOnBatteries `
  -AllowStartIfOnBatteries -MultipleInstances IgnoreNew -ExecutionTimeLimit (New-TimeSpan -Minutes 20)
$principal = New-ScheduledTaskPrincipal -UserId "$env:USERDOMAIN\$env:USERNAME" -LogonType S4U -RunLevel Limited

Register-ScheduledTask -TaskName $TaskName -Action $action -Trigger $trigger `
  -Settings $settings -Principal $principal -Force | Out-Null

Write-Host "已注册计划任务 $TaskName（每30分钟）。"
Write-Host "首次务必先手动预演/建基线："
Write-Host "  pwsh -File `"$Runner`" dry-run"
Write-Host "  pwsh -File `"$Runner`" resync"
Write-Host "立即手动跑一次验证： Start-ScheduledTask -TaskName $TaskName"
Write-Host "查看： Get-ScheduledTaskInfo -TaskName $TaskName ; Get-Content '$Log' -Tail 30"
