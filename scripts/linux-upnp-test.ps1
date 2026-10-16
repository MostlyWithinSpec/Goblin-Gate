<#
.SYNOPSIS
  Tests UPnP / NAT-PMP (task 059) with the real miniupnpd from Debian 12
  (miniupnpd-nftables) and miniupnpc's upnpc: the rendered ruleset and
  miniupnpd.conf in a network namespace, a LAN client opening a port, the mapping
  surviving a ruleset reload and restart, and disabling UPnP removing it
  (internal/render/upnp TestUPnPE2E). Runs in a throwaway privileged Debian 12
  container; needs Docker Desktop and internet. Not part of verify.ps1.

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/linux-upnp-test.ps1
#>
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) { Write-Host 'docker not found' -ForegroundColor Red; exit 2 }
docker version --format '{{.Server.Version}}' *> $null
if ($LASTEXITCODE -ne 0) { Write-Host 'Docker engine is not running - start Docker Desktop.' -ForegroundColor Red; exit 2 }

$work = Join-Path ([IO.Path]::GetTempPath()) "goblin-upnp-$PID"
New-Item -ItemType Directory -Force $work | Out-Null
try {
    Push-Location $Root
    $env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
    try { go test -c -o (Join-Path $work 'upnp.test') ./internal/render/upnp } finally { Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED; Pop-Location }
    if ($LASTEXITCODE -ne 0) { throw 'building the test binary failed' }

    $script = @'
set -eo pipefail
apt-get update -qq >/dev/null
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq nftables iproute2 miniupnpd-nftables miniupnpc >/dev/null 2>&1
echo "$(miniupnpd --version 2>&1 | head -1); $(nft --version)"
cd /w && GOBLIN_UPNP_E2E=1 ./upnp.test -test.run TestUPnPE2E -test.v 2>&1 | grep -v "^=== RUN"
'@
    [IO.File]::WriteAllText((Join-Path $work 'run.sh'), ($script -replace "`r", ''), (New-Object System.Text.UTF8Encoding $false))
    $ErrorActionPreference = 'Continue'
    docker run --rm --privileged -v "${work}:/w" debian:12 bash /w/run.sh
    $code = $LASTEXITCODE
    $ErrorActionPreference = 'Stop'
} finally {
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}
if ($code -ne 0) { Write-Host 'UPNP E2E FAILED' -ForegroundColor Red; exit 1 }
Write-Host 'UPNP E2E PASSED' -ForegroundColor Green
