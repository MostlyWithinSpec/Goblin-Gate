<#
.SYNOPSIS
  End-to-end test of the Linux applier (internal/apply TestE2E) on a live Debian 12
  system: boots a throwaway privileged container with systemd as PID 1, udev,
  systemd-networkd, nftables, kea-dhcp4-server and unbound, adds a dummy eth1, and
  lets the real applier write /etc, load the ruleset, create VLANs and restart the
  services - including a VLAN id change, a failed activation with rollback and a
  rejected ruleset. Only the container is touched. Needs Docker Desktop.
  Not part of verify.ps1 (Docker is optional).

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/linux-e2e.ps1
#>
[CmdletBinding()]
param(
    [string]$Image = 'goblin-e2e:debian12',
    [switch]$Keep   # leave the container running for inspection (docker exec -it goblin-e2e bash)
)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
$Name = 'goblin-e2e'

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) { Write-Host 'docker not found' -ForegroundColor Red; exit 2 }
docker version --format '{{.Server.Version}}' *> $null
if ($LASTEXITCODE -ne 0) { Write-Host 'Docker engine is not running - start Docker Desktop.' -ForegroundColor Red; exit 2 }

# procps is deliberately not installed: the applier must not need sysctl or /bin/kill.
$dockerfile = @'
FROM debian:12
RUN apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
      systemd systemd-sysv udev iproute2 nftables kea-dhcp4-server unbound wireguard-tools >/dev/null \
 && apt-get clean && systemctl enable systemd-networkd
COPY wait-online.conf /etc/systemd/system/systemd-networkd-wait-online.service.d/goblin.conf
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
'@
$work = Join-Path ([IO.Path]::GetTempPath()) "goblin-e2e-$PID"
New-Item -ItemType Directory -Force $work | Out-Null
try {
    [IO.File]::WriteAllText((Join-Path $work 'Dockerfile'), ($dockerfile -replace "`r", ''), (New-Object System.Text.UTF8Encoding $false))
    # The packaged drop-in: without it every kea/unbound restart waits 2 min for the (absent) WAN.
    $dropin = Get-Content -Raw (Join-Path $Root 'packaging/systemd/systemd-networkd-wait-online.service.d/goblin.conf')
    [IO.File]::WriteAllText((Join-Path $work 'wait-online.conf'), ($dropin -replace "`r", ''), (New-Object System.Text.UTF8Encoding $false))
    docker build -q -t $Image $work | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'docker build failed' }

    $bin = Join-Path $work 'apply.test'
    $env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
    try { go test -c -o $bin ./internal/apply } finally { Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED }
    if ($LASTEXITCODE -ne 0) { throw 'building the test binary failed' }

    docker rm -f $Name *> $null
    docker run -d --name $Name --privileged --cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw $Image | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'docker run failed' }
    $ErrorActionPreference = 'Continue'  # PS 5.1 turns native stderr into terminating errors
    for ($i = 0; $i -lt 30; $i++) {
        $state = (docker exec $Name systemctl is-system-running 2>$null) -join ''
        if ($state -match 'running|degraded') { break }
        Start-Sleep 1
    }
    docker cp $bin "${Name}:/apply.test" | Out-Null
    docker exec $Name sh -c 'ip link add eth1 type dummy && ip link set eth1 up && cd / && GOBLIN_APPLY_E2E=1 ./apply.test -test.run TestE2E -test.v 2>&1'
    $code = $LASTEXITCODE
} finally {
    if (-not $Keep) { docker rm -f $Name *> $null }
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}
if ($code -ne 0) { Write-Host 'LINUX E2E FAILED' -ForegroundColor Red; exit 1 }
Write-Host 'LINUX E2E PASSED' -ForegroundColor Green
