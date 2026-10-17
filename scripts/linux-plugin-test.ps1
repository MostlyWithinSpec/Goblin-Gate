<#
.SYNOPSIS
  End-to-end test of the plugin framework (internal/api TestPluginE2E) on a live
  Debian 12 system: boots a throwaway privileged container with systemd as PID 1,
  installs goblin-plugin@.service and the example hello plugin's .deb, and lets
  the real plugin manager start it sandboxed (DynamicUser, identified by its
  cgroup), change its capabilities and stop it. Then installs Goblin Sentry's
  .deb as a release builds it (version-stamped) and runs it end to end
  (internal/api TestSentryE2E). Needs Docker Desktop. Not part of verify.ps1.

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/linux-plugin-test.ps1
#>
[CmdletBinding()]
param(
    [string]$Image = 'goblin-plugin-e2e:debian12',
    [switch]$Keep   # leave the container running (docker exec -it goblin-plugin-e2e bash)
)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
$Name = 'goblin-plugin-e2e'

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) { Write-Host 'docker not found' -ForegroundColor Red; exit 2 }
docker version --format '{{.Server.Version}}' *> $null
if ($LASTEXITCODE -ne 0) { Write-Host 'Docker engine is not running - start Docker Desktop.' -ForegroundColor Red; exit 2 }

$dockerfile = @'
FROM debian:12
RUN apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq systemd systemd-sysv >/dev/null \
 && apt-get clean
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
'@
$work = Join-Path ([IO.Path]::GetTempPath()) "goblin-plugin-e2e-$PID"
New-Item -ItemType Directory -Force $work | Out-Null
try {
    [IO.File]::WriteAllText((Join-Path $work 'Dockerfile'), ($dockerfile -replace "`r", ''), (New-Object System.Text.UTF8Encoding $false))
    docker build -q -t $Image $work | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'docker build failed' }

    Push-Location $Root
    $env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
    try {
        go test -c -o (Join-Path $work 'api.test') ./internal/api
        if ($LASTEXITCODE -ne 0) { throw 'building the test binary failed' }
        go build -trimpath -o (Join-Path $work 'hello-run') ./examples/plugins/hello
        if ($LASTEXITCODE -ne 0) { throw 'building the hello plugin failed' }
        go build -trimpath -o (Join-Path $work 'sentry-run') ./plugins/sentry
        if ($LASTEXITCODE -ne 0) { throw 'building the sentry plugin failed' }
    } finally { Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED; Pop-Location }
    Copy-Item -Recurse (Join-Path $Root 'examples/plugins/hello') (Join-Path $work 'hello')
    New-Item -ItemType Directory -Force (Join-Path $work 'sentry') | Out-Null
    Copy-Item (Join-Path $Root 'plugins/sentry/build-deb.sh'), (Join-Path $Root 'plugins/sentry/plugin.json') (Join-Path $work 'sentry')
    Copy-Item (Join-Path $Root 'packaging/systemd/goblin-plugin@.service') $work

    # The plugin's .deb is built by its own script, with the binary built above.
    $script = @'
set -eo pipefail
install -m644 /w/goblin-plugin@.service /lib/systemd/system/
systemctl daemon-reload
deb=$(RUN_BINARY=/w/hello-run bash /w/hello/build-deb.sh /w/dist)
dpkg --force-depends -i "$deb" >/dev/null
echo "installed $(basename "$deb")"
cd /w && GOBLIN_PLUGIN_E2E=1 ./api.test -test.run TestPluginE2E -test.v 2>&1
# Sentry as a nightly build packages it
deb=$(VERSION=0.1.0-alpha.3-12-gabc1234 RUN_BINARY=/w/sentry-run bash /w/sentry/build-deb.sh /w/dist)
dep=$(dpkg-deb -f "$deb" Depends)
[ "$dep" = "goblin-gate (>= 0.1.0~alpha.3+12.gabc1234)" ] || { echo "Depends: $dep"; exit 1; }
dpkg --force-depends -i "$deb" >/dev/null
echo "installed $(basename "$deb")"
cd /w && GOBLIN_SENTRY_E2E=1 GOBLIN_VERSION=0.1.0~alpha.3+12.gabc1234 ./api.test -test.run TestSentryE2E -test.v 2>&1
'@
    [IO.File]::WriteAllText((Join-Path $work 'run.sh'), ($script -replace "`r", ''), (New-Object System.Text.UTF8Encoding $false))

    docker rm -f $Name *> $null
    docker run -d --name $Name --privileged --cgroupns=host -v /sys/fs/cgroup:/sys/fs/cgroup:rw -v "${work}:/w" $Image | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'docker run failed' }
    $ErrorActionPreference = 'Continue'  # PS 5.1 turns native stderr into terminating errors
    for ($i = 0; $i -lt 30; $i++) {
        $state = (docker exec $Name systemctl is-system-running 2>$null) -join ''
        if ($state -match 'running|degraded') { break }
        Start-Sleep 1
    }
    docker exec $Name bash /w/run.sh
    $code = $LASTEXITCODE
} finally {
    if (-not $Keep) { docker rm -f $Name *> $null }
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}
if ($code -ne 0) { Write-Host 'PLUGIN E2E FAILED' -ForegroundColor Red; exit 1 }
Write-Host 'PLUGIN E2E PASSED' -ForegroundColor Green
