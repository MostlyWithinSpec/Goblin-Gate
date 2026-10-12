<#
.SYNOPSIS
  End-to-end test of the IPS (task 031) with Suricata 7 and the real ET Open rules:
  renders two IPS profiles, builds their rules, checks both configs with suricata -T,
  then queues live HTTP traffic through NFQUEUE to each profile's Suricata - the
  detect profile must alert and let the page through, the prevent profile must
  alert and block it (internal/render/suricata TestSuricataE2E).
  Runs in a throwaway privileged Debian 12 container (Suricata from
  bookworm-backports, NFQUEUE modules from the Docker VM). Downloads ET Open
  (about 40 MB), so it needs internet and takes a few minutes. Needs Docker Desktop.
  Not part of verify.ps1.

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/linux-ips-test.ps1
#>
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) { Write-Host 'docker not found' -ForegroundColor Red; exit 2 }
docker version --format '{{.Server.Version}}' *> $null
if ($LASTEXITCODE -ne 0) { Write-Host 'Docker engine is not running - start Docker Desktop.' -ForegroundColor Red; exit 2 }

$work = Join-Path ([IO.Path]::GetTempPath()) "goblin-ips-$PID"
New-Item -ItemType Directory -Force $work | Out-Null
try {
    Push-Location $Root
    $env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
    try { go test -c -o (Join-Path $work 'suricata.test') ./internal/render/suricata } finally { Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED; Pop-Location }
    if ($LASTEXITCODE -ne 0) { throw 'building the test binary failed' }

    $script = @'
set -eo pipefail
echo "deb http://deb.debian.org/debian bookworm-backports main" > /etc/apt/sources.list.d/bp.list
apt-get update -qq >/dev/null
apt-get install -y -qq -t bookworm-backports suricata suricata-update >/dev/null 2>&1
apt-get install -y -qq nftables kmod curl ca-certificates >/dev/null 2>&1
modprobe nfnetlink_queue && modprobe nft_queue
suricata -V
suricata-update --no-test --no-reload -o /var/lib/suricata/rules >/tmp/su.log 2>&1 || { tail -5 /tmp/su.log; exit 1; }
echo "ET Open rules: $(grep -c -E '^(alert|drop)' /var/lib/suricata/rules/suricata.rules)"
mkdir -p /var/log/suricata
cd /w && GOBLIN_IPS_E2E=1 ./suricata.test -test.run TestSuricataE2E -test.v -test.timeout 15m 2>&1 | grep -v "^=== RUN"
'@
    [IO.File]::WriteAllText((Join-Path $work 'run.sh'), ($script -replace "`r", ''), (New-Object System.Text.UTF8Encoding $false))
    $ErrorActionPreference = 'Continue'
    docker run --rm --privileged -v /lib/modules:/lib/modules:ro -v "${work}:/w" debian:12 bash /w/run.sh
    $code = $LASTEXITCODE
    $ErrorActionPreference = 'Stop'
} finally {
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}
if ($code -ne 0) { Write-Host 'IPS E2E FAILED' -ForegroundColor Red; exit 1 }
Write-Host 'IPS E2E PASSED' -ForegroundColor Green
