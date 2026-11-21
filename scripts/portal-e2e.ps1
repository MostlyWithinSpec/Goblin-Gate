<#
.SYNOPSIS
  Captive portal end to end: a client on a portal network is redirected to the portal, gets out
  after entering a voucher, and is shut out again when the voucher's time is up.

.DESCRIPTION
  Builds the current tree as a .deb and installs it in a privileged Debian 12 container with
  systemd as PID 1, on a Docker network that stands in for the internet (198.51.101.0/24) with a
  web server on it. The gate's LAN (eth1) has a client in a network namespace. The script sets the
  gate up through the API, turns on a voucher portal on the LAN and commits, then checks: DHCP
  hands out the portal API (option 114), HTTP is redirected to the portal, HTTPS is refused at
  once, ping and the admin UI are blocked, the RFC 8908 API says captive; a voucher lets the client
  out (web page, ping, API says not captive, listed under clients); the session survives a goblind
  restart; removing a guest shuts it out at once, open connections too; a 1-minute voucher
  expires. Only the containers are touched. Needs Docker Desktop and internet (apt, the nginx
  image). Not part of verify.ps1.

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/portal-e2e.ps1
#>
[CmdletBinding()]
param(
    [string]$Version = '0.3.99~portal',
    [switch]$Keep   # leave the containers running (docker exec -it gg-portal bash)
)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
$work = Join-Path ([IO.Path]::GetTempPath()) 'goblin-portal-e2e'
$net = 'gg-portal-wan'
$g = 'gg-portal'
$web = 'gg-portal-web'
$script:fails = 0

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) { Write-Host 'docker not found' -ForegroundColor Red; exit 2 }
docker version --format '{{.Server.Version}}' *> $null
if ($LASTEXITCODE -ne 0) { Write-Host 'Docker engine is not running - start Docker Desktop.' -ForegroundColor Red; exit 2 }

function Invoke-Docker([string[]]$DockerArgs) {
    $ErrorActionPreference = 'Continue'
    $out = & docker @DockerArgs 2>&1 | ForEach-Object { "$_" }
    $script:lastCode = $LASTEXITCODE
    return ($out -join "`n")
}
function Check([string]$Name, [bool]$Ok, [string]$Detail = '') {
    if ($Ok) { Write-Host "PASS $Name" -ForegroundColor Green } else { Write-Host "FAIL $Name $Detail" -ForegroundColor Red; $script:fails++ }
}
# Api <method> <path> [body object]: curl inside the gate (the API listens on loopback too)
function Api([string]$Method, [string]$Path, $Body = $null) {
    $ca = @('exec', $g, 'curl', '-sk', '-m', '300', '-X', $Method, '-H', 'Content-Type: application/json')
    if ($script:token) { $ca += @('-H', "Authorization: Bearer $($script:token)") }
    if ($null -ne $Body) {
        [IO.File]::WriteAllText((Join-Path $work 'body.json'), ($Body | ConvertTo-Json -Depth 20 -Compress), (New-Object System.Text.UTF8Encoding $false))
        $ca += @('-d', '@/w/body.json')
    }
    $ca += "https://127.0.0.1$Path"
    $out = Invoke-Docker $ca
    try { return $out | ConvertFrom-Json } catch { return [pscustomobject]@{ raw = $out } }
}
function Patch([string]$Path, $Value) {
    $r = Api PATCH /api/v1/config/candidate @{ path = $Path; value = $Value }
    if ($r.validation_errors) { Write-Host "  patch $Path had validation errors: $($r.validation_errors | ConvertTo-Json -Compress)" -ForegroundColor Yellow }
    if ($r.error) { Write-Host "  patch $Path failed: $($r.error.message)" -ForegroundColor Yellow }
}
function Exec([string]$Cmd) { return Invoke-Docker @('exec', $g, 'bash', '-c', $Cmd) }
function Client([string]$Cmd) { return Exec "ip netns exec client $Cmd" }
# Captive: the client's plain HTTP is answered by the portal's redirect
function Captive {
    $h = Client "curl -s -m5 -o /dev/null -w '%{http_code} %{redirect_url}' http://198.51.101.2/"
    return ($h -match '^302 http://192.168.1.1:8880/')
}
function Online { return ((Client 'curl -s -m5 http://198.51.101.2/') -match 'nginx') }
# Use-Voucher: what a guest does on the page; returns the HTTP status of the form post
function Use-Voucher([string]$Code) {
    $page = Client 'curl -s -m5 http://192.168.1.1:8880/'
    if ($page -notmatch 'name="token" value="([0-9a-f]+)"') { return "no form: $page" }
    $tk = $Matches[1]
    return (Client "curl -s -m5 -o /dev/null -w %{http_code} --data-urlencode voucher=$Code --data-urlencode accept=on --data-urlencode token=$tk http://192.168.1.1:8880/login").Trim()
}

New-Item -ItemType Directory -Force (Join-Path $work 'debs') | Out-Null
try {
    # --- the package from the current tree
    $deb = Join-Path $work "debs\goblin-gate_$($Version)_amd64.deb"
    Remove-Item (Join-Path $work 'debs\*') -ErrorAction SilentlyContinue
    Push-Location (Join-Path $Root 'web')
    try {
        if (-not (Test-Path 'node_modules')) { npm ci --no-audit --no-fund --silent; if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' } }
        npm run build --silent | Out-Null; if ($LASTEXITCODE -ne 0) { throw 'web build failed' }
    } finally { Pop-Location }
    $null = Invoke-Docker @('run', '--rm', '-v', "${Root}:/src", '-v', "${work}:/w", '-w', '/src', '-e', 'GOFLAGS=-buildvcs=false',
        '-e', "VERSION=$Version", '-e', 'OUT_DIR=/w/debs', 'golang:1.26-bookworm', 'bash', 'scripts/build-deb.sh')
    if (-not (Test-Path $deb)) { throw "package build failed" }

    # --- an image with systemd and the package's dependencies
    $dockerfile = @'
FROM debian:12
RUN apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq systemd systemd-sysv udev iproute2 curl jq netcat-openbsd procps >/dev/null \
 && systemctl enable systemd-networkd
COPY debs/ /debs/
RUN DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends /debs/*.deb >/dev/null && apt-get clean \
 && systemctl disable apt-daily.timer apt-daily-upgrade.timer unattended-upgrades 2>/dev/null; true
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
'@
    [IO.File]::WriteAllText((Join-Path $work 'Dockerfile'), ($dockerfile -replace "`r", ''), (New-Object System.Text.UTF8Encoding $false))
    Write-Host 'building the gate image'
    $null = Invoke-Docker @('build', '-q', '-t', 'goblin-portal-e2e', $work)
    if ($script:lastCode -ne 0) { throw 'image build failed' }

    # --- the "internet" with a web server, and the gate
    foreach ($c in @($g, $web)) { $null = Invoke-Docker @('rm', '-f', $c) }
    $null = Invoke-Docker @('network', 'rm', $net)
    $null = Invoke-Docker @('network', 'create', '--subnet', '198.51.101.0/24', '--gateway', '198.51.101.1', $net)
    $null = Invoke-Docker @('run', '-d', '--name', $web, '--network', $net, '--ip', '198.51.101.2', 'nginx:alpine')
    if ($script:lastCode -ne 0) { throw 'could not start the web server' }
    $null = Invoke-Docker @('run', '-d', '--privileged', '--cgroupns=host', '-v', '/sys/fs/cgroup:/sys/fs/cgroup:rw', '-v', "${work}:/w",
        '--name', $g, '--hostname', 'guestgate', '--network', $net, '--ip', '198.51.101.10', 'goblin-portal-e2e')
    if ($script:lastCode -ne 0) { throw "could not start $g" }
    Exec "ip netns add client && ip link add eth1 type veth peer name cl0 && ip link set cl0 netns client && ip link set eth1 up && ip netns exec client ip link set lo up && ip netns exec client ip link set cl0 up && ip netns exec client ip addr add 192.168.1.50/24 dev cl0 && ip netns exec client ip route add default via 192.168.1.1" | Out-Null
    $ok = $false
    foreach ($i in 1..60) { $s = Api GET /api/v1/setup/state; if ($s.needed -ne $null) { $ok = $true; break }; Start-Sleep 2 }
    if (-not $ok) { Write-Host (Exec 'journalctl -u goblind --no-pager | tail -40'); throw 'the API never came up' }
    $r = Api POST /api/v1/setup @{ admin_password = 'Goblin-Test-Passw0rd!'; hostname = 'guestgate'; security_level = 'basic'
        wan = @{ device = 'eth0'; mode = 'static'; address = '198.51.101.10/24'; gateway = '198.51.101.1' }
        lan = @{ device = 'eth1'; address = '192.168.1.1/24' } }
    if ($r.error) { throw "setup: $($r.error.message)" }
    # bodies go through files: Windows PowerShell mangles quotes in native arguments
    [IO.File]::WriteAllText((Join-Path $work 'login.json'), '{"username":"admin","password":"Goblin-Test-Passw0rd!"}')
    [IO.File]::WriteAllText((Join-Path $work 'token.json'), '{"name":"portal-e2e"}')
    $login = Exec "curl -sk -c /tmp/jar -H 'Content-Type: application/json' -d @/w/login.json https://127.0.0.1/api/v1/auth/login" | ConvertFrom-Json
    $tok = Exec "curl -sk -b /tmp/jar -H 'X-CSRF-Token: $($login.csrf_token)' -H 'Content-Type: application/json' -d @/w/token.json https://127.0.0.1/api/v1/auth/tokens" | ConvertFrom-Json
    if (-not $tok.token) { throw 'token' }
    $script:token = $tok.token

    $up = $false
    foreach ($i in 1..20) { if (Online) { $up = $true; break }; Start-Sleep 2 }
    Check 'online before the portal' $up

    # --- a voucher portal on the LAN
    Patch 'dhcp' @(@{ interface = 'lan'; range_start = '192.168.1.100'; range_end = '192.168.1.199' })
    Patch 'captive_portals' @(@{ interface = 'lan'; enabled = $true; mode = 'voucher'; title = 'Guest Wi-Fi'; text = 'Be nice.' })
    $c = Api POST /api/v1/commit @{ comment = 'portal e2e' }
    Check 'commit' ($c.status -eq 'ok' -or $c.revision) ($c | ConvertTo-Json -Depth 6 -Compress)
    Start-Sleep 2

    Check 'dhcp option 114' ((Exec 'grep -c v4-captive-portal /etc/kea/kea-dhcp4.conf') -match '^[1-9]')
    Check 'http redirected to the portal' (Captive) (Client 'curl -s -m5 -i http://198.51.101.2/ | head -5')
    $probe = Client "curl -s -m5 -o /dev/null -w '%{http_code} %{redirect_url}' -H 'Host: captive.apple.com' http://198.51.101.2/hotspot-detect.html"
    Check 'os probe redirected' ($probe -match '^302 http://192.168.1.1:8880/') $probe
    $ms = Client "sh -c 'a=`$(date +%s%N); curl -s -m5 https://198.51.101.2/ >/dev/null 2>&1; b=`$(date +%s%N); echo `$(( (b-a)/1000000 ))'"
    Check 'https refused at once' ([int]($ms.Trim().Split("`n")[-1]) -lt 1500) "took $ms ms"
    Check 'ping blocked' (-not ((Client 'ping -c1 -W2 198.51.101.2 >/dev/null && echo ok') -match 'ok'))
    Check 'admin ui blocked' (-not ((Client 'curl -sk -m3 -o /dev/null -w %{http_code} https://192.168.1.1/') -match '^[1-5]'))
    $papi = Client 'curl -s -m5 http://192.168.1.1:8880/captive-portal/api'
    Check 'api says captive' ($papi -match '"captive":true') $papi

    $b = Api POST /api/v1/portal/vouchers @{ count = 2; minutes = 60; uses = 1 }
    Check 'vouchers made' ($b.codes.Count -eq 2) ($b | ConvertTo-Json -Compress)
    $bad = Use-Voucher 'XXXX-XXXX'
    Check 'unknown voucher refused' ($bad -eq '403') $bad
    $res = Use-Voucher $b.codes[0].code
    Check 'voucher accepted' ($res -eq '303') $res
    Start-Sleep 1
    Check 'online after the voucher' (Online) (Exec 'nft list set inet goblin portal_ok')
    Check 'ping after the voucher' ((Client 'ping -c1 -W2 198.51.101.2 >/dev/null && echo ok') -match 'ok')
    $papi = Client 'curl -s -m5 http://192.168.1.1:8880/captive-portal/api'
    Check 'api says not captive' ($papi -match '"captive":false') $papi
    $cl = Api GET /api/v1/portal/clients
    Check 'listed as a guest' ($cl.clients.Count -eq 1 -and $cl.clients[0].ip -eq '192.168.1.50' -and $cl.clients[0].voucher -eq $b.codes[0].code) ($cl | ConvertTo-Json -Compress)

    # sessions survive a restart (the sets are refilled after the ruleset load)
    Exec 'systemctl restart goblind' | Out-Null
    foreach ($i in 1..30) { Start-Sleep 2; if ((Api GET /api/v1/portal/clients).clients) { break } }
    Start-Sleep 3
    Check 'online after a restart' (Online) (Exec 'nft list set inet goblin portal_ok')

    # removing the guest shuts it out at once, its open "connection" too
    Client "sh -c 'nohup ping -i 0.2 198.51.101.2 > /tmp/ping.log 2>&1 &'" | Out-Null
    Start-Sleep 1
    $k = Api DELETE "/api/v1/portal/clients/$($cl.clients[0].mac)"
    Check 'guest removed' ($k.status -eq 'ok') ($k | ConvertTo-Json -Compress)
    Start-Sleep 1
    Check 'shut out after removal' (Captive)
    $again = Use-Voucher $b.codes[0].code
    Check 'used voucher refused' ($again -eq '403') $again
    $before = (Client 'grep -c "bytes from" /tmp/ping.log').Trim()
    Start-Sleep 2
    $after = (Client 'grep -c "bytes from" /tmp/ping.log').Trim()
    Client 'pkill ping' | Out-Null
    Check 'open connection cut' ($before -eq $after) "$before -> $after"

    # a 1-minute voucher expires
    $b1 = Api POST /api/v1/portal/vouchers @{ count = 1; minutes = 1 }
    $res = Use-Voucher $b1.codes[0].code
    Check 'short voucher accepted' ($res -eq '303') $res
    Start-Sleep 1
    Check 'online with the short voucher' (Online)
    Start-Sleep 65
    Check 'voucher expired' (Captive) (Exec 'nft list set inet goblin portal_ok')
    $cl = Api GET /api/v1/portal/clients
    Check 'expired guest gone from the list' ($cl.clients.Count -eq 0) ($cl | ConvertTo-Json -Compress)
} finally {
    if (-not $Keep) {
        foreach ($c in @($g, $web)) { $null = Invoke-Docker @('rm', '-f', $c) }
        $null = Invoke-Docker @('network', 'rm', $net)
    }
}
if ($script:fails -gt 0) { Write-Host "PORTAL E2E FAILED ($script:fails)" -ForegroundColor Red; exit 1 }
Write-Host 'PORTAL E2E PASSED' -ForegroundColor Green
