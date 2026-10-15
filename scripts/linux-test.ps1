<#
.SYNOPSIS
  Runs the Go tests on Linux in a throwaway Docker container (Debian 12 + nftables),
  including the Linux-only checks that cannot run on Windows:
    - TestNftSyntax: `nft -c -f` on the nft renderer's golden files
    - TestRealChecks: the applier's checks (nft -c, kea-dhcp4 -t, unbound-checkconf) on rendered configs
    - a real `nft -f` load (twice) of the full golden ruleset inside the container's
      own network namespace - the host firewall is never touched.
  Needs Docker Desktop running. Not part of verify.ps1 (Docker is optional).

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/linux-test.ps1
#>
[CmdletBinding()]
param(
    [string]$Image = 'golang:1.26-bookworm'
)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) { Write-Host 'docker not found' -ForegroundColor Red; exit 2 }
docker version --format '{{.Server.Version}}' *> $null
if ($LASTEXITCODE -ne 0) { Write-Host 'Docker engine is not running - start Docker Desktop.' -ForegroundColor Red; exit 2 }

$script = @'
set -eo pipefail
apt-get update -qq >/dev/null
apt-get install -y -qq nftables kea-dhcp4-server unbound dnsutils iproute2 >/dev/null 2>&1
# what unbound.service's start helper does: the DNSSEC anchor from dns-root-data
install -o unbound -m644 /usr/share/dns/root.key /var/lib/unbound/root.key
echo "nft: $(nft --version)  kea: $(kea-dhcp4 -v)  kernel: $(uname -r)"
go test -race -count=1 ./... 2>&1 | grep -v "no test files"
GOBLIN_NFT_CHECK=1 go test -count=1 -run TestNftSyntax -v ./internal/render/nft | grep -E "^(--- |ok|FAIL)"
GOBLIN_APPLY_CHECK=1 go test -count=1 -run TestRealChecks -v ./internal/apply | grep -E "^(\s*--- |ok|FAIL|\s+check_)"
if [ -f internal/render/nft/testdata/full.nft ]; then
  nft -f internal/render/nft/testdata/full.nft
  nft -f internal/render/nft/testdata/full.nft
  echo "nft load + reload of full.nft: ok"
  GOBLIN_NFT_LOAD=1 go test -count=1 -run 'TestFeedBatchLoad|TestScheduleLoad' -v ./internal/render/nft | grep -E "^(\s*--- |ok|FAIL|\s+(feeds|schedule)_linux)"
fi
# DNS filter in a real unbound (needs internet for the unfiltered answers)
GOBLIN_UNBOUND_E2E=1 go test -count=1 -run TestDNSFilterE2E -v ./internal/render/unbound | grep -E "^(\s*--- |ok|FAIL|\s+filter_linux)"
# web filter proxy in front of the same unbound
GOBLIN_WF_E2E=1 go test -count=1 -run TestWebFilterE2E -v ./internal/webfilter | grep -E "^(\s*--- |ok|FAIL|\s+e2e_linux)"
# QR codes (WireGuard client configs) must decode in a real reader
apt-get install -y -qq zbar-tools >/dev/null 2>&1
mkdir -p /tmp/qr && QR_PNG_DIR=/tmp/qr go test -count=1 -run TestWritePNGs ./internal/qr >/dev/null
for f in /tmp/qr/*.png; do
  zbarimg -q --raw --oneshot -Sbinary "$f" > "${f%.png}.out"
  cmp -s "${f%.png}.out" "${f%.png}.txt" || { echo "QR $(basename $f) does not decode"; exit 1; }
done
echo "QR codes decode: ok"
'@
# Hand the script over as a mounted file: Windows PowerShell mangles quotes in native
# arguments and adds a BOM and CRLF to piped stdin.
$tmp = Join-Path ([IO.Path]::GetTempPath()) "goblin-linux-test-$PID.sh"
[IO.File]::WriteAllText($tmp, ($script -replace "`r", ''), (New-Object System.Text.UTF8Encoding $false))
try {
    docker run --rm --cap-add NET_ADMIN -v "${Root}:/src:ro" -v "${tmp}:/gg-test.sh:ro" -w /src -e GOFLAGS=-buildvcs=false $Image bash /gg-test.sh
    $code = $LASTEXITCODE
} finally {
    Remove-Item $tmp -ErrorAction SilentlyContinue
}
if ($code -ne 0) { Write-Host 'LINUX TESTS FAILED' -ForegroundColor Red; exit 1 }
Write-Host 'LINUX TESTS PASSED' -ForegroundColor Green
