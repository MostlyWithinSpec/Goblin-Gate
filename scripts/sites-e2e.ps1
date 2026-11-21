<#
.SYNOPSIS
  Site-to-site VPN end to end (task 155): two Goblin Gates pair with an invitation code and talk
  across the tunnel.

.DESCRIPTION
  Builds the current tree as a .deb and installs it in two privileged Debian 12 containers with
  systemd as PID 1 (gate-a "home", gate-b "cabin"), joined by a Docker network that stands in for
  the internet (198.51.100.0/24). Each gate has a LAN (eth1) with a client in a network namespace.
  The script sets both gates up through the API, pairs them (invite on home, join on cabin, accept
  on home), adds policies and commits, then checks: the WireGuard handshake, the status API (both
  sites reachable), a ping between the two LAN clients, SSH allowed from the cabin to home's client
  while HTTP is blocked by policy, and that the cabin's LAN reaches home's LAN only through the
  tunnel. Only the containers are touched. Needs Docker Desktop and internet (apt).
  Not part of verify.ps1.

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/sites-e2e.ps1
#>
[CmdletBinding()]
param(
    [string]$Version = '0.3.99~sites',
    [switch]$Keep   # leave the containers running (docker exec -it gg-site-a bash)
)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
$work = Join-Path ([IO.Path]::GetTempPath()) 'goblin-sites-e2e'
$net = 'gg-sites-wan'
$gates = [ordered]@{
    'gg-site-a' = @{ Host = 'home'; Wan = '198.51.100.10'; Lan = '192.168.1.1/24'; Client = '192.168.1.50' }
    'gg-site-b' = @{ Host = 'cabin'; Wan = '198.51.100.20'; Lan = '192.168.50.1/24'; Client = '192.168.50.50' }
}
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
# Api <gate> <method> <path> [body object]: curl inside the gate (the API listens on loopback too)
function Api([string]$Gate, [string]$Method, [string]$Path, $Body = $null) {
    $ca = @('exec', $Gate, 'curl', '-sk', '-m', '300', '-X', $Method, '-H', 'Content-Type: application/json')
    if ($script:tokens -and $script:tokens[$Gate]) { $ca += @('-H', "Authorization: Bearer $($script:tokens[$Gate])") }
    if ($null -ne $Body) {
        $file = "body-$Gate.json"
        [IO.File]::WriteAllText((Join-Path $work $file), ($Body | ConvertTo-Json -Depth 20 -Compress), (New-Object System.Text.UTF8Encoding $false))
        $ca += @('-d', "@/w/$file")
    }
    $ca += "https://127.0.0.1$Path"
    $out = Invoke-Docker $ca
    try { return $out | ConvertFrom-Json } catch { return [pscustomobject]@{ raw = $out } }
}
function Patch([string]$Gate, [string]$Path, $Value) {
    $r = Api $Gate PATCH /api/v1/config/candidate @{ path = $Path; value = $Value }
    if ($r.validation_errors) { Write-Host "  ${Gate}: patch $Path had validation errors: $($r.validation_errors | ConvertTo-Json -Compress)" -ForegroundColor Yellow }
    if ($r.error) { Write-Host "  ${Gate}: patch $Path failed: $($r.error.message)" -ForegroundColor Yellow }
}
function Exec([string]$Gate, [string]$Cmd) { $o = Invoke-Docker @('exec', $Gate, 'bash', '-c', $Cmd); return $o }

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
RUN apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq systemd systemd-sysv udev iproute2 curl jq netcat-openbsd >/dev/null \
 && systemctl enable systemd-networkd
COPY debs/ /debs/
RUN DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends /debs/*.deb >/dev/null && apt-get clean \
 && systemctl disable apt-daily.timer apt-daily-upgrade.timer unattended-upgrades 2>/dev/null; true
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
'@
    [IO.File]::WriteAllText((Join-Path $work 'Dockerfile'), ($dockerfile -replace "`r", ''), (New-Object System.Text.UTF8Encoding $false))
    Write-Host 'building the gate image'
    $null = Invoke-Docker @('build', '-q', '-t', 'goblin-sites-e2e', $work)
    if ($script:lastCode -ne 0) { throw 'image build failed' }

    # --- the "internet" and the two gates
    foreach ($g in $gates.Keys) { $null = Invoke-Docker @('rm', '-f', $g) }
    $null = Invoke-Docker @('network', 'rm', $net)
    $null = Invoke-Docker @('network', 'create', '--subnet', '198.51.100.0/24', '--gateway', '198.51.100.1', $net)
    foreach ($g in $gates.Keys) {
        $c = $gates[$g]
        $null = Invoke-Docker @('run', '-d', '--privileged', '--cgroupns=host', '-v', '/sys/fs/cgroup:/sys/fs/cgroup:rw', '-v', "${work}:/w",
            '--name', $g, '--hostname', $c.Host, '--network', $net, '--ip', $c.Wan, 'goblin-sites-e2e')
        if ($script:lastCode -ne 0) { throw "could not start $g" }
    }
    foreach ($g in $gates.Keys) {
        $c = $gates[$g]
        $lanGw = $c.Lan.Split('/')[0]
        # the LAN: eth1 with a client in its own namespace behind it
        Exec $g "ip netns add client && ip link add eth1 type veth peer name cl0 && ip link set cl0 netns client && ip link set eth1 up && ip netns exec client ip link set lo up && ip netns exec client ip link set cl0 up && ip netns exec client ip addr add $($c.Client)/24 dev cl0 && ip netns exec client ip route add default via $lanGw" | Out-Null
        $ok = $false
        foreach ($i in 1..60) { $s = Api $g GET /api/v1/setup/state; if ($s.needed -ne $null) { $ok = $true; break }; Start-Sleep 2 }
        if (-not $ok) { Write-Host (Exec $g 'journalctl -u goblind --no-pager | tail -40'); throw "$g's API never came up" }
        $setup = @{ admin_password = 'Goblin-Test-Passw0rd!'; hostname = $c.Host; security_level = 'basic'
            wan = @{ device = 'eth0'; mode = 'static'; address = "$($c.Wan)/24"; gateway = '198.51.100.1' }
            lan = @{ device = 'eth1'; address = $c.Lan } }
        $r = Api $g POST /api/v1/setup $setup
        if ($r.error) { throw "$g setup: $($r.error.message)" }
    }
    $script:tokens = @{}
    foreach ($g in $gates.Keys) {
        # bodies go through files: Windows PowerShell mangles quotes in native arguments
        [IO.File]::WriteAllText((Join-Path $work 'login.json'), '{"username":"admin","password":"Goblin-Test-Passw0rd!"}')
        [IO.File]::WriteAllText((Join-Path $work 'token.json'), '{"name":"sites-e2e"}')
        $login = Exec $g "curl -sk -c /tmp/jar -H 'Content-Type: application/json' -d @/w/login.json https://127.0.0.1/api/v1/auth/login" | ConvertFrom-Json
        $tok = Exec $g "curl -sk -b /tmp/jar -H 'X-CSRF-Token: $($login.csrf_token)' -H 'Content-Type: application/json' -d @/w/token.json https://127.0.0.1/api/v1/auth/tokens" | ConvertFrom-Json
        if (-not $tok.token) { throw "$g token" }
        $script:tokens[$g] = $tok.token
    }

    # --- pair: home invites, cabin joins, home accepts
    Patch 'gg-site-a' 'vpn.sites' @{ enabled = $true; endpoint = $gates['gg-site-a'].Wan; networks = @('lan') }
    Patch 'gg-site-b' 'vpn.sites' @{ enabled = $true; networks = @('lan') }
    $inv = Api 'gg-site-a' POST /api/v1/vpn/sites/invitations @{ site = 'cabin' }
    Check 'invitation' ([bool]$inv.code) ($inv | ConvertTo-Json -Compress)
    $chk = Api 'gg-site-b' POST /api/v1/vpn/sites/join/check @{ code = $inv.code }
    Check 'join-check' ($chk.site -eq 'home' -and $chk.problems.Count -eq 0 -and $chk.networks -contains '192.168.1.0/24') ($chk | ConvertTo-Json -Compress)
    $join = Api 'gg-site-b' POST /api/v1/vpn/sites/join @{ code = $inv.code; site = 'home' }
    Check 'join' ([bool]$join.reply) ($join | ConvertTo-Json -Compress)
    $acc = Api 'gg-site-a' POST /api/v1/vpn/sites/accept @{ code = $join.reply }
    Check 'accept' ($acc.site.name -eq 'cabin') ($acc | ConvertTo-Json -Compress)

    # --- policies: home lets its LAN reach the cabin, and the cabin only SSH to home's LAN; the cabin allows all to home
    function Add-Policy([string]$Gate, $Policy) {
        $cand = Api $Gate GET /api/v1/config/candidate
        $list = @($cand.policies | Where-Object { $_ }) + @($Policy)
        Patch $Gate 'policies' $list
    }
    $svc = (Api 'gg-site-a' GET /api/v1/config/candidate).services
    if (-not ($svc | Where-Object { $_.name -eq 'ssh-e2e' })) { Patch 'gg-site-a' 'services' (@($svc | Where-Object { $_ }) + @(@{ name = 'ssh-e2e'; protocol = 'tcp'; ports = '22' })) }
    Add-Policy 'gg-site-a' @{ id = 100; name = 'lan-to-cabin'; enabled = $true; src_intf = @('lan'); dst_intf = @('cabin'); src_addr = @('all'); dst_addr = @('all'); service = @('all'); action = 'accept' }
    Add-Policy 'gg-site-a' @{ id = 101; name = 'cabin-ssh'; enabled = $true; src_intf = @('cabin'); dst_intf = @('lan'); src_addr = @('all'); dst_addr = @('all'); service = @('ssh-e2e'); action = 'accept' }
    Add-Policy 'gg-site-b' @{ id = 100; name = 'lan-to-home'; enabled = $true; src_intf = @('lan'); dst_intf = @('home'); src_addr = @('all'); dst_addr = @('all'); service = @('all'); action = 'accept' }
    Add-Policy 'gg-site-b' @{ id = 101; name = 'home-to-lan'; enabled = $true; src_intf = @('home'); dst_intf = @('lan'); src_addr = @('all'); dst_addr = @('all'); service = @('all'); action = 'accept' }
    foreach ($g in $gates.Keys) {
        $c = Api $g POST /api/v1/commit @{ comment = 'sites e2e' }
        Check "commit-$g" ($c.status -eq 'ok' -or $c.revision) ($c | ConvertTo-Json -Depth 6 -Compress)
    }

    # --- the tunnel
    $hs = $false
    foreach ($i in 1..30) {
        $d = Exec 'gg-site-b' 'wg show home latest-handshakes'
        if ($d -match '\s([1-9][0-9]+)\s*$') { $hs = $true; break }
        Start-Sleep 2
    }
    Check 'handshake' $hs (Exec 'gg-site-b' 'wg show; ip route; networkctl')
    foreach ($g in $gates.Keys) {
        $st = Api $g GET /api/v1/vpn/sites/status
        Check "status-$g" ($st.sites[0].reachable -eq $true -and $st.sites[0].handshake_age -ge 0) ($st | ConvertTo-Json -Depth 5 -Compress)
    }
    $a = $gates['gg-site-a'].Client; $b = $gates['gg-site-b'].Client
    Check 'ping home->cabin' ((Exec 'gg-site-a' "ip netns exec client ping -c2 -W2 $b >/dev/null && echo ok") -match 'ok')
    Check 'ping cabin->home blocked' (-not ((Exec 'gg-site-b' "ip netns exec client ping -c2 -W2 $a >/dev/null && echo ok") -match 'ok'))
    # listeners on home's client: SSH allowed by policy, HTTP not
    Exec 'gg-site-a' "ip netns exec client sh -c 'nohup nc -lk -p 22 >/dev/null 2>&1 & nohup nc -lk -p 80 >/dev/null 2>&1 &'" | Out-Null
    Start-Sleep 1
    Check 'cabin ssh to home lan' ((Exec 'gg-site-b' "ip netns exec client nc -z -w3 $a 22 && echo ok") -match 'ok')
    Check 'cabin http blocked' (-not ((Exec 'gg-site-b' "ip netns exec client nc -z -w3 $a 80 && echo ok") -match 'ok'))
    # the cabin's LAN never reaches home's LAN outside the tunnel (the WAN carries only WireGuard)
    $cap = Exec 'gg-site-a' "timeout 6 tcpdump -eni eth0 -c 20 'not udp port 51830 and net 192.168.0.0/16' 2>/dev/null & sleep 1; ip netns exec client ping -c3 -W1 $b >/dev/null; wait"
    Check 'only wireguard on the wan' (-not ($cap -match 'IPv4')) $cap
} finally {
    if (-not $Keep) {
        foreach ($g in $gates.Keys) { $null = Invoke-Docker @('rm', '-f', $g) }
        $null = Invoke-Docker @('network', 'rm', $net)
    }
}
if ($script:fails -gt 0) { Write-Host "SITES E2E FAILED ($script:fails)" -ForegroundColor Red; exit 1 }
Write-Host 'SITES E2E PASSED' -ForegroundColor Green
