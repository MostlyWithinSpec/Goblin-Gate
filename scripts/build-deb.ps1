<#
.SYNOPSIS
  Builds the goblin-gate Debian package (task 038) into dist/: builds the web UI
  with npm, then the Go binaries and the .deb in a golang:1.26-bookworm container.
  Needs Docker Desktop and Node.js.

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-deb.ps1
#>
[CmdletBinding()]
param([string]$Version)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
if (-not $Version) {
    $Version = (git -C $Root describe --tags --always 2>$null)
    if (-not $Version) { $Version = '0.0.0' }
    $Version = $Version -replace '^v', ''
    if ($Version -notmatch '^\d+\.\d') { $Version = "0.0.0+g$Version" }
}
Push-Location (Join-Path $Root 'web')
try {
    npm run build --silent
    if ($LASTEXITCODE -ne 0) { throw 'web build failed' }
} finally { Pop-Location }
docker run --rm -v "${Root}:/src" -w /src -e VERSION=$Version -e GOFLAGS=-buildvcs=false golang:1.26-bookworm bash scripts/build-deb.sh
if ($LASTEXITCODE -ne 0) { Write-Host 'DEB BUILD FAILED' -ForegroundColor Red; exit 1 }
Get-ChildItem (Join-Path $Root 'dist') -Filter *.deb | Sort-Object LastWriteTime | Select-Object -Last 1 | ForEach-Object { Write-Host "built $($_.FullName)" -ForegroundColor Green }
