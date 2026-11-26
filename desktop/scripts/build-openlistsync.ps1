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

Push-Location $sidecarDir
try {
  # Embed the engine logo (shared icon from engine/) so the sidecar carries the right icon
  $icon = Join-Path $repoRoot 'engine/icon.ico'
  if (Test-Path $icon -PathType Leaf) {
    Copy-Item $icon (Join-Path $sidecarDir 'icon.ico') -Force
    $winres = Join-Path $env:USERPROFILE 'go/bin/go-winres.exe'
    if (Test-Path $winres) {
      & $winres simply --icon icon.ico | Out-Null
      Remove-Item (Join-Path $sidecarDir 'rsrc_windows_386.syso') -Force -ErrorAction SilentlyContinue
      Remove-Item (Join-Path $sidecarDir 'rsrc_windows_amd64.syso_windows_amd64.syso') -Force -ErrorAction SilentlyContinue
    } elseif (Get-Command rsrc -ErrorAction SilentlyContinue) {
      rsrc -ico icon.ico -o rsrc_windows_amd64.syso | Out-Null
    }
  }
  go build -trimpath -ldflags '-s -w -H windowsgui' -o $output .
  Remove-Item (Join-Path $sidecarDir 'rsrc_windows_amd64.syso') -Force -ErrorAction SilentlyContinue
  Remove-Item (Join-Path $sidecarDir 'icon.ico') -Force -ErrorAction SilentlyContinue
} finally {
  Pop-Location
}

Write-Host "Built $output"