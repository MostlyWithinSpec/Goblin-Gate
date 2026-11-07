<#
.SYNOPSIS
  Tests the reverse proxy end to end with Debian 12's caddy and Pebble (Let's
  Encrypt's test CA): certificates issued through the WAN redirects, proxying,
  the HTTPS redirect, the allow-list and the LAN hairpin, in network namespaces
  (internal/reverseproxy TestProxyE2E). Runs in a throwaway privileged
  container; needs Docker Desktop and internet. Not part of verify.ps1.

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/linux-proxy-test.ps1
#>
[CmdletBinding()]
param(
    [string]$Image = 'golang:1.26-bookworm',
    [string]$Pebble = 'v2.6.0'
)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) { Write-Host 'docker not found' -ForegroundColor Red; exit 2 }
docker version --format '{{.Server.Version}}' *> $null
if ($LASTEXITCODE -ne 0) { Write-Host 'Docker engine is not running - start Docker Desktop.' -ForegroundColor Red; exit 2 }

$work = Join-Path ([IO.Path]::GetTempPath()) "goblin-proxy-$PID"
New-Item -ItemType Directory -Force $work | Out-Null
try {
    Push-Location $Root
    $env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
    try { go test -c -o (Join-Path $work 'proxy.test') ./internal/reverseproxy } finally { Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED; Pop-Location }
    if ($LASTEXITCODE -ne 0) { throw 'building the test binary failed' }

    $script = @'
set -eo pipefail
apt-get update -qq >/dev/null
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq nftables iproute2 caddy curl >/dev/null 2>&1
GOBIN=/usr/local/bin go install github.com/letsencrypt/pebble/v2/cmd/pebble@PEBBLE_VERSION 2>&1 | tail -3
echo "$(caddy version | cut -d' ' -f1); pebble PEBBLE_VERSION; $(nft --version)"
cd /w && GOBLIN_PROXY_E2E=1 ./proxy.test -test.run '^TestProxyE2E$' -test.v 2>&1 | grep -v "^=== RUN"
'@ -replace 'PEBBLE_VERSION', $Pebble
    [IO.File]::WriteAllText((Join-Path $work 'run.sh'), ($script -replace "`r", ''), (New-Object System.Text.UTF8Encoding $false))
    $ErrorActionPreference = 'Continue'
    docker run --rm --privileged -v "${work}:/w" $Image bash /w/run.sh
    $code = $LASTEXITCODE
    $ErrorActionPreference = 'Stop'
} finally {
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}
if ($code -ne 0) { Write-Host 'PROXY E2E FAILED' -ForegroundColor Red; exit 1 }
Write-Host 'PROXY E2E PASSED' -ForegroundColor Green
