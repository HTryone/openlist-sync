$ErrorActionPreference = 'Stop'

$repoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$sidecarDir = Join-Path $repoRoot 'src-tauri/sidecar/openlistsync'
$binaryDir = Join-Path $repoRoot 'src-tauri/binary'
$targetArg = $args | Where-Object { $_ -like '--target=*' } | Select-Object -First 1
$target = if ($targetArg) { $targetArg.Substring('--target='.Length) } else { 'x86_64-pc-windows-msvc' }

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
  throw 'Go toolchain not found. Install Go 1.27 or newer.'
}

New-Item -ItemType Directory -Force -Path $binaryDir | Out-Null

$env:CGO_ENABLED = '0'
$outputName = if ($IsWindows -or $env:OS -eq 'Windows_NT') {
  "openlistsync-$target.exe"
} else {
  "openlistsync-$target"
}
$output = Join-Path $binaryDir $outputName

# Embed the engine logo (shared icon from engine/) into the sidecar exe
$icon = Join-Path $repoRoot 'engine/icon.ico'
if (Test-Path $icon -PathType Leaf) {
  Copy-Item $icon (Join-Path $sidecarDir 'icon.ico') -Force
  if (Get-Command rsrc -ErrorAction SilentlyContinue) {
    rsrc -ico icon.ico -o rsrc_windows_amd64.syso | Out-Null
  }
}
Push-Location $sidecarDir
try {
  go build -trimpath -ldflags '-s -w -H windowsgui' -o $output .
  Remove-Item (Join-Path $sidecarDir 'rsrc_windows_amd64.syso') -Force -ErrorAction SilentlyContinue
  Remove-Item (Join-Path $sidecarDir 'icon.ico') -Force -ErrorAction SilentlyContinue
} finally {
  Pop-Location
}

Write-Host "Built $output"