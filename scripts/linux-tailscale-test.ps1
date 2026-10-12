<#
.SYNOPSIS
  Tests goblind's Tailscale control (task 041) against a real tailscaled from
  Tailscale's Debian repository: status, the preferences goblind sets (netfilter
  off, routes, exit node), an interactive login URL and turning it off
  (internal/tailscale TestLocalAPIE2E). No tailnet login is needed. Runs in a
  throwaway privileged Debian 12 container; needs internet and Docker Desktop.
  Not part of verify.ps1.

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/linux-tailscale-test.ps1
#>
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) { Write-Host 'docker not found' -ForegroundColor Red; exit 2 }
docker version --format '{{.Server.Version}}' *> $null
if ($LASTEXITCODE -ne 0) { Write-Host 'Docker engine is not running - start Docker Desktop.' -ForegroundColor Red; exit 2 }

$work = Join-Path ([IO.Path]::GetTempPath()) "goblin-ts-$PID"
New-Item -ItemType Directory -Force $work | Out-Null
try {
    Push-Location $Root
    $env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
    try { go test -c -o (Join-Path $work 'tailscale.test') ./internal/tailscale } finally { Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED; Pop-Location }
    if ($LASTEXITCODE -ne 0) { throw 'building the test binary failed' }

    $script = @'
set -eo pipefail
apt-get update -qq >/dev/null
apt-get install -y -qq curl gnupg ca-certificates nftables >/dev/null 2>&1
curl -fsSL https://pkgs.tailscale.com/stable/debian/bookworm.noarmor.gpg -o /usr/share/keyrings/tailscale-archive-keyring.gpg
curl -fsSL https://pkgs.tailscale.com/stable/debian/bookworm.tailscale-keyring.list -o /etc/apt/sources.list.d/tailscale.list
apt-get update -qq >/dev/null
apt-get install -y -qq tailscale >/dev/null 2>&1
echo "tailscale $(tailscale version | head -1)"
mkdir -p /var/run/tailscale /var/lib/tailscale
(tailscaled --state=/var/lib/tailscale/tailscaled.state --socket=/var/run/tailscale/tailscaled.sock > /tmp/tsd.log 2>&1 &)
for i in $(seq 1 30); do [ -S /var/run/tailscale/tailscaled.sock ] && break; sleep 0.5; done
cd /w && GOBLIN_TS_E2E=1 ./tailscale.test -test.run TestLocalAPIE2E -test.v 2>&1 | grep -v "^=== RUN"
n=$(nft list ruleset | grep -c "ts-" || true)
echo "firewall rules made by tailscaled: $n"
[ "$n" = "0" ]
'@
    [IO.File]::WriteAllText((Join-Path $work 'run.sh'), ($script -replace "`r", ''), (New-Object System.Text.UTF8Encoding $false))
    $ErrorActionPreference = 'Continue'
    docker run --rm --privileged --device /dev/net/tun -v "${work}:/w" debian:12 bash /w/run.sh
    $code = $LASTEXITCODE
    $ErrorActionPreference = 'Stop'
} finally {
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}
if ($code -ne 0) { Write-Host 'TAILSCALE E2E FAILED' -ForegroundColor Red; exit 1 }
Write-Host 'TAILSCALE E2E PASSED' -ForegroundColor Green
