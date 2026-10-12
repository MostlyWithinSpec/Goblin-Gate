<#
.SYNOPSIS
  Builds goblind and goblin for linux/amd64 into bin\ with the git-derived version.

.DESCRIPTION
  The version comes from `git describe --tags --always` (fallback 0.0.0-dev) and
  is stamped into internal/version.Version via -ldflags. Pass -Version to override.

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build.ps1
#>
[CmdletBinding()]
param(
    [string]$Version = ''
)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $Root

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw "Go is not installed or not on PATH."
}

if (-not $Version) {
    $git = & git describe --tags --always 2>$null
    if ($git) { $Version = "$git".Trim() }
    if (-not $Version) { $Version = '0.0.0-dev' }
}

$binDir = Join-Path $Root 'bin'
New-Item -ItemType Directory -Force -Path $binDir | Out-Null

$ldflags = "-s -w -X github.com/networkgoblinlabs/goblin-gate/internal/version.Version=$Version"

$oldOS = $env:GOOS; $oldArch = $env:GOARCH; $oldCgo = $env:CGO_ENABLED
$env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
try {
    foreach ($tool in @('goblind', 'goblin')) {
        Write-Host "building $tool for linux/amd64 (version $Version)" -ForegroundColor Cyan
        & go build -trimpath -ldflags $ldflags -o (Join-Path $binDir $tool) "./cmd/$tool"
        if ($LASTEXITCODE -ne 0) { throw "go build failed for $tool (exit $LASTEXITCODE)" }
    }
} finally {
    $env:GOOS = $oldOS; $env:GOARCH = $oldArch; $env:CGO_ENABLED = $oldCgo
}

Write-Host "done: bin\goblind, bin\goblin (version $Version)" -ForegroundColor Green
