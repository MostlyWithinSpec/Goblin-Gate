<#
.SYNOPSIS
  Builds the Goblin Gate installer ISO (task 038) into dist/: the .deb first
  (scripts/build-deb.ps1), then a customized Debian 12 installer with simple-cdd in
  a privileged Debian container. The installer asks only for the target disk; BIOS
  and UEFI (Secure Boot through Debian's signed shim, also Hyper-V Gen 2 with the
  Microsoft UEFI CA template) are supported. Needs Docker Desktop, Node.js and
  internet. -Unattended builds a test image that installs to /dev/sda without
  asking (used by scripts/hyperv-test.ps1).

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/build-iso.ps1
#>
[CmdletBinding()]
param([switch]$Unattended, [switch]$SkipDeb)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
$Version = (git -C $Root describe --tags --always 2>$null) -replace '^v', ''
if ($Version -notmatch '^\d+\.\d') { $Version = "0.0.0+g$Version" }
if (-not $SkipDeb) {
    Remove-Item (Join-Path $Root 'dist\goblin-gate_*.deb') -ErrorAction SilentlyContinue
    & powershell -NoProfile -ExecutionPolicy Bypass -File (Join-Path $PSScriptRoot 'build-deb.ps1') -Version $Version
    if ($LASTEXITCODE -ne 0) { exit 1 }
}
$u = if ($Unattended) { '1' } else { '0' }
$ErrorActionPreference = 'Continue'
docker run --rm --privileged -v "${Root}:/src" -e VERSION=$Version -e UNATTENDED=$u debian:12 bash /src/scripts/iso/build.sh
$code = $LASTEXITCODE
$ErrorActionPreference = 'Stop'
if ($code -ne 0) { Write-Host 'ISO BUILD FAILED' -ForegroundColor Red; exit 1 }
