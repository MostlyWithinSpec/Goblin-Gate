<#
.SYNOPSIS
  RADIUS end to end: MAC authentication into the right VLAN, unknown devices to quarantine,
  802.1X with PEAP and EAP-TLS, accounting, and a Disconnect when a device moves.

.DESCRIPTION
  Builds the current tree as a .deb and installs it with FreeRADIUS in a privileged Debian 12
  container with systemd as PID 1. A network namespace on the gate's LAN (192.168.1.50) plays the
  switch: radclient sends MAB and accounting requests, eapol_test runs PEAP-MSCHAPv2 and EAP-TLS,
  and a small responder answers Disconnect-Requests on UDP 3799. The script sets the gate up
  through the API (VLANs iot 20, staff 40, quarantine 99, the internal CA, RADIUS) and checks:
  the stock freeradius.service stays masked, the AppArmor profile parses, a known MAC gets its
  VLAN, an unknown MAC gets quarantine, a wrong secret is not let in, a PEAP user gets the
  group's VLAN and a wrong password is refused with a reason in the log, an EAP-TLS certificate
  issued through the API is accepted and refused once revoked, accounting shows the switch port
  (sessions and device annotations), and moving a device sends a Disconnect the switch acks.
  Only the containers are touched. Needs Docker Desktop and internet (apt). Not part of
  verify.ps1.

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/radius-e2e.ps1
#>
[CmdletBinding()]
param(
    [string]$Version = '0.3.99~radius',
    [switch]$Keep   # leave the container running (docker exec -it gg-radius bash)
)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
$work = Join-Path ([IO.Path]::GetTempPath()) 'goblin-radius-e2e'
$net = 'gg-radius-wan'
$g = 'gg-radius'
$secret = 'testing123x'
$script:fails = 0
$utf8 = New-Object System.Text.UTF8Encoding $false

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
function Write-Work([string]$Name, [string]$Text) { [IO.File]::WriteAllText((Join-Path $work $Name), ($Text -replace "`r", ''), $utf8) }
function Api([string]$Method, [string]$Path, $Body = $null) {
    $ca = @('exec', $g, 'curl', '-sk', '-m', '300', '-X', $Method, '-H', 'Content-Type: application/json')
    if ($script:token) { $ca += @('-H', "Authorization: Bearer $($script:token)") }
    if ($null -ne $Body) {
        Write-Work 'body.json' ($Body | ConvertTo-Json -Depth 20 -Compress)
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
function Commit([string]$Comment) {
    $c = Api POST /api/v1/commit @{ comment = $Comment }
    Check "commit: $Comment" ($c.status -eq 'ok' -or $c.revision) ($c | ConvertTo-Json -Depth 6 -Compress)
}
function Exec([string]$Cmd) { return Invoke-Docker @('exec', $g, 'bash', '-c', $Cmd) }
function Sw([string]$Cmd) { return Exec "ip netns exec switch $Cmd" }
# Radclient <auth|acct> <attributes>: the "switch" asks the gate
function Radclient([string]$Kind, [string]$Attrs, [string]$Secret = $secret) {
    Write-Work 'rad.txt' $Attrs
    return Sw "radclient -x -r 1 -t 3 -f /w/rad.txt 192.168.1.1 $Kind $Secret"
}
function Eapol([string]$Conf, [string]$Mac) {
    return Sw "eapol_test -c /w/$Conf -a 192.168.1.1 -s $secret -M $Mac -t 10"
}
function Last-Event { return (Api GET '/api/v1/radius/log?limit=1').events[0] }

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

    # --- an image with systemd, the package, FreeRADIUS, Caddy (internal CA) and the test clients
    Write-Work 'Dockerfile' @'
FROM debian:12
RUN apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq systemd systemd-sysv udev iproute2 curl jq procps python3-minimal \
    freeradius-utils eapoltest >/dev/null \
 && systemctl enable systemd-networkd
COPY debs/ /debs/
RUN DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends /debs/*.deb caddy freeradius freeradius-rest >/dev/null && apt-get clean \
 && systemctl disable apt-daily.timer apt-daily-upgrade.timer unattended-upgrades 2>/dev/null; true
STOPSIGNAL SIGRTMIN+3
CMD ["/lib/systemd/systemd"]
'@
    Write-Host 'building the gate image'
    $null = Invoke-Docker @('build', '-q', '-t', 'goblin-radius-e2e', $work)
    if ($script:lastCode -ne 0) { throw 'image build failed' }

    $null = Invoke-Docker @('rm', '-f', $g)
    $null = Invoke-Docker @('network', 'rm', $net)
    $null = Invoke-Docker @('network', 'create', '--subnet', '198.51.102.0/24', '--gateway', '198.51.102.1', $net)
    $null = Invoke-Docker @('run', '-d', '--privileged', '--cgroupns=host', '-v', '/sys/fs/cgroup:/sys/fs/cgroup:rw', '-v', "${work}:/w",
        '--name', $g, '--hostname', 'radgate', '--network', $net, '--ip', '198.51.102.10', 'goblin-radius-e2e')
    if ($script:lastCode -ne 0) { throw "could not start $g" }
    Exec "ip netns add switch && ip link add eth1 type veth peer name sw0 && ip link set sw0 netns switch && ip link set eth1 up && ip netns exec switch ip link set lo up && ip netns exec switch ip link set sw0 up && ip netns exec switch ip addr add 192.168.1.50/24 dev sw0 && ip netns exec switch ip route add default via 192.168.1.1" | Out-Null
    $ok = $false
    foreach ($i in 1..60) { $s = Api GET /api/v1/setup/state; if ($s.needed -ne $null) { $ok = $true; break }; Start-Sleep 2 }
    if (-not $ok) { Write-Host (Exec 'journalctl -u goblind --no-pager | tail -40'); throw 'the API never came up' }
    $r = Api POST /api/v1/setup @{ admin_password = 'Goblin-Test-Passw0rd!'; hostname = 'radgate'; security_level = 'basic'
        wan = @{ device = 'eth0'; mode = 'static'; address = '198.51.102.10/24'; gateway = '198.51.102.1' }
        lan = @{ device = 'eth1'; address = '192.168.1.1/24' } }
    if ($r.error) { throw "setup: $($r.error.message)" }
    Write-Work 'login.json' '{"username":"admin","password":"Goblin-Test-Passw0rd!"}'
    Write-Work 'token.json' '{"name":"radius-e2e"}'
    $login = Exec "curl -sk -c /tmp/jar -H 'Content-Type: application/json' -d @/w/login.json https://127.0.0.1/api/v1/auth/login" | ConvertFrom-Json
    $tok = Exec "curl -sk -b /tmp/jar -H 'X-CSRF-Token: $($login.csrf_token)' -H 'Content-Type: application/json' -d @/w/token.json https://127.0.0.1/api/v1/auth/tokens" | ConvertFrom-Json
    if (-not $tok.token) { throw 'token' }
    $script:token = $tok.token

    Check 'stock freeradius.service masked' ((Exec 'systemctl is-enabled freeradius.service').Trim() -eq 'masked') (Exec 'systemctl is-enabled freeradius.service')
    $aa = Exec 'apparmor_parser -Q -K /etc/apparmor.d/goblin-gate.freeradius && echo parsed'
    Check 'AppArmor profile parses' ($aa -match 'parsed') $aa

    # --- VLANs and the internal CA first: 802.1X needs the CA's root
    Patch 'vlans' @(
        @{ name = 'iot'; parent = 'lan'; vlan_id = 20; mode = 'static'; address = '192.168.20.1/24'; enabled = $true },
        @{ name = 'staff'; parent = 'lan'; vlan_id = 40; mode = 'static'; address = '192.168.40.1/24'; enabled = $true },
        @{ name = 'quarantine'; parent = 'lan'; vlan_id = 99; mode = 'static'; address = '192.168.99.1/24'; enabled = $true })
    Patch 'internal_ca' @{ enabled = $true }
    Commit 'vlans and CA'
    $ca = $false
    foreach ($i in 1..30) { if ((Exec 'test -s /var/lib/goblin-caddy/pki/authorities/local/root.key && echo yes') -match 'yes') { $ca = $true; break }; Start-Sleep 2 }
    Check 'internal CA made its root' $ca (Exec 'journalctl -u goblin-caddy --no-pager | tail -20')

    Patch 'radius' @{ enabled = $true; mab = $true; dot1x = $true; unknown = 'quarantine'; quarantine_vlan = 'quarantine'
        clients = @(@{ name = 'switch'; address = '192.168.1.50'; secret = $secret })
        groups = @(@{ name = 'staff'; vlan = 'staff' })
        users = @(@{ name = 'alice'; enabled = $true; password = 'alice-password'; group = 'staff' }) }
    Commit 'radius'
    $up = $false
    foreach ($i in 1..15) { if ((Exec 'systemctl is-active goblin-radius.service').Trim() -eq 'active') { $up = $true; break }; Start-Sleep 1 }
    Check 'goblin-radius running' $up (Exec 'journalctl -u goblin-radius --no-pager | tail -40')
    $fw = Exec 'nft list ruleset | grep 1812'
    Check 'firewall opens RADIUS to the switch only' ($fw -match 'ip saddr (\{ )?192\.168\.1\.50(/32)?( \})? udp dport \{ 1812, 1813 \} accept' -and $fw -match 'iifname != "eth0"') $fw
    Check 'secrets root only' ((Exec 'stat -c %a /etc/goblingate/radius/radiusd.conf').Trim() -eq '600')

    # --- MAB
    $mv = Api PUT /api/v1/devices/aa:bb:cc:00:00:01/vlan @{ vlan = 'iot' }
    Check 'device given a VLAN' ($mv.device.vlan -eq 'iot') ($mv | ConvertTo-Json -Compress)
    $a = Radclient auth 'User-Name = "aabbcc000001", User-Password = "aabbcc000001", Calling-Station-Id = "AA-BB-CC-00-00-01", NAS-Port-Id = "Gi1/0/3"'
    Check 'known MAC accepted into its VLAN' ($a -match 'Access-Accept' -and $a -match 'Tunnel-Private-Group-Id(:0)? = "20"' -and $a -match 'Tunnel-Type(:0)? = VLAN') $a
    $a = Radclient auth 'User-Name = "aabbcc0000ff", User-Password = "aabbcc0000ff", Calling-Station-Id = "AA-BB-CC-00-00-FF"'
    Check 'unknown MAC to quarantine' ($a -match 'Access-Accept' -and $a -match 'Tunnel-Private-Group-Id(:0)? = "99"') $a
    $e = Last-Event
    Check 'quarantine logged' ($e.result -eq 'quarantine' -and $e.client -eq 'switch' -and $e.vlan -eq 'quarantine') ($e | ConvertTo-Json -Compress)
    $a = Radclient auth 'User-Name = "aabbcc000001", User-Password = "aabbcc000001"' 'wrong-secret'
    Check 'wrong secret not let in' (-not ($a -match 'Received Access-Accept')) $a
    $a = Radclient auth 'User-Name = "aabbcc000001", User-Password = "wrong"'
    Check 'MAB with a wrong password refused' ($a -match 'Access-Reject') $a
    $e = Last-Event
    Check 'wrong MAB password logged' ($e.result -eq 'reject' -and $e.method -eq 'mab' -and $e.reason -match 'wrong password') ($e | ConvertTo-Json -Compress)

    # --- PEAP-MSCHAPv2
    $null = Exec 'curl -sk -o /w/ca.pem https://127.0.0.1/api/v1/internal-ca/root.crt'
    Write-Work 'peap.conf' "network={`n key_mgmt=WPA-EAP`n eap=PEAP`n identity=`"alice`"`n anonymous_identity=`"anonymous`"`n password=`"alice-password`"`n phase2=`"auth=MSCHAPV2`"`n ca_cert=`"/w/ca.pem`"`n}`n"
    $p = Eapol 'peap.conf' '02:00:00:00:00:aa'
    Check 'PEAP user accepted' ($p -match '(?m)^SUCCESS') (($p -split "`n" | Select-Object -Last 15) -join "`n")
    $e = Last-Event
    Check 'PEAP user in the group VLAN' ($e.method -eq 'peap' -and $e.user -eq 'alice' -and $e.vlan -eq 'staff' -and $e.vlan_id -eq 40) ($e | ConvertTo-Json -Compress)
    Write-Work 'peap-bad.conf' "network={`n key_mgmt=WPA-EAP`n eap=PEAP`n identity=`"alice`"`n anonymous_identity=`"anonymous`"`n password=`"not-her-password`"`n phase2=`"auth=MSCHAPV2`"`n ca_cert=`"/w/ca.pem`"`n}`n"
    $p = Eapol 'peap-bad.conf' '02:00:00:00:00:ab'
    Check 'PEAP wrong password refused' ($p -match '(?m)^FAILURE') (($p -split "`n" | Select-Object -Last 5) -join "`n")
    $e = Last-Event
    Check 'refusal logged with the reason' ($e.result -eq 'reject' -and $e.method -eq 'peap' -and $e.user -eq 'alice' -and $e.reason -eq 'wrong password') ($e | ConvertTo-Json -Compress)

    # --- EAP-TLS with a certificate from the API
    $cert = Api POST /api/v1/radius/users/alice/certificate @{ days = 30 }
    Check 'client certificate issued' ($cert.certificate -match 'BEGIN CERTIFICATE' -and $cert.key -match 'PRIVATE KEY') ($cert | ConvertTo-Json -Compress)
    Write-Work 'alice.pem' $cert.certificate
    Write-Work 'alice.key' $cert.key
    Write-Work 'tls.conf' "network={`n key_mgmt=WPA-EAP`n eap=TLS`n identity=`"alice`"`n ca_cert=`"/w/ca.pem`"`n client_cert=`"/w/alice.pem`"`n private_key=`"/w/alice.key`"`n}`n"
    $p = Eapol 'tls.conf' '02:00:00:00:00:ac'
    Check 'EAP-TLS accepted' ($p -match '(?m)^SUCCESS') (($p -split "`n" | Select-Object -Last 15) -join "`n")
    $e = Last-Event
    Check 'EAP-TLS in the group VLAN' ($e.method -eq 'eap-tls' -and $e.user -eq 'alice' -and $e.vlan -eq 'staff') ($e | ConvertTo-Json -Compress)
    $rv = Api POST /api/v1/radius/users/alice/certificates/revoke @{}
    Check 'certificate revoked' ($rv.revoked -eq 1) ($rv | ConvertTo-Json -Compress)
    $p = Eapol 'tls.conf' '02:00:00:00:00:ac'
    Check 'revoked certificate refused' ($p -match '(?m)^FAILURE') (($p -split "`n" | Select-Object -Last 5) -join "`n")
    $e = Last-Event
    Check 'revocation is the reason' ($e.method -eq 'eap-tls' -and $e.reason -match 'revoked') ($e | ConvertTo-Json -Compress)

    # --- accounting, then a move with a Disconnect the "switch" acks
    $a = Radclient acct 'Acct-Status-Type = Start, User-Name = "aabbcc000001", Calling-Station-Id = "AA-BB-CC-00-00-01", Acct-Session-Id = "S1", NAS-Port-Id = "Gi1/0/3", NAS-Identifier = "sw-office"'
    Check 'accounting answered' ($a -match 'Accounting-Response') $a
    $ss = (Api GET /api/v1/radius/sessions).sessions | Where-Object { $_.mac -eq 'aabbcc000001' }
    Check 'session shows the switch port' ($ss.port -eq 'Gi1/0/3' -and $ss.client -eq 'switch' -and $ss.session_id -eq 'S1') ($ss | ConvertTo-Json -Compress)
    $dv = (Api GET /api/v1/devices).devices | Where-Object { $_.mac -eq 'aabbcc000001' }
    Check 'device annotated with its port' ($dv.annotations.'@radius'.port -eq 'Gi1/0/3' -and $dv.annotations.'@radius'.switch -eq 'sw-office') ($dv | ConvertTo-Json -Depth 5 -Compress)
    Write-Work 'coa.py' @"
import socket, hashlib, struct
s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
s.bind(('0.0.0.0', 3799))
sec = b'$secret'
while True:
    d, a = s.recvfrom(4096)
    with open('/tmp/coa.log', 'a') as f:
        f.write(d.hex() + '\n')
    hdr = bytes([41, d[1]]) + struct.pack('!H', 20)
    s.sendto(hdr + hashlib.md5(hdr + d[4:20] + sec).digest(), a)
"@
    Sw "sh -c 'nohup python3 /w/coa.py >/tmp/coa.out 2>&1 &'" | Out-Null
    Start-Sleep 1
    $mv = Api PUT /api/v1/devices/aa:bb:cc:00:00:01/vlan @{ vlan = 'staff' }
    Check 'move sends a Disconnect the switch acks' ($mv.disconnect.result -eq 'acked' -and $mv.device.vlan -eq 'staff') ($mv | ConvertTo-Json -Compress)
    $coa = Exec 'cat /tmp/coa.log'
    $csid = -join ('AA-BB-CC-00-00-01'.ToCharArray() | ForEach-Object { '{0:x2}' -f [int]$_ })
    Check 'Disconnect names the device and session' ($coa -match '^28' -and $coa -match $csid -and $coa -match '5331') $coa
    $a = Radclient auth 'User-Name = "aabbcc000001", User-Password = "aabbcc000001", Calling-Station-Id = "AA-BB-CC-00-00-01"'
    Check 'rejoins in the new VLAN' ($a -match 'Tunnel-Private-Group-Id(:0)? = "40"') $a
    $dc = Api POST /api/v1/radius/sessions/aa:bb:cc:00:00:01/disconnect
    Check 'disconnect from the API' ($dc.result -eq 'acked') ($dc | ConvertTo-Json -Compress)

    # --- off again: FreeRADIUS stops, the ports close
    Patch 'radius.enabled' $false
    Commit 'radius off'
    Start-Sleep 2
    Check 'goblin-radius stopped' ((Exec 'systemctl is-active goblin-radius.service').Trim() -ne 'active')
    Check 'ports closed' (-not ((Exec 'nft list ruleset') -match '1812'))
} finally {
    if (-not $Keep) {
        $null = Invoke-Docker @('rm', '-f', $g)
        $null = Invoke-Docker @('network', 'rm', $net)
    }
}
if ($script:fails -gt 0) { Write-Host "RADIUS E2E FAILED ($script:fails)" -ForegroundColor Red; exit 1 }
Write-Host 'RADIUS E2E PASSED' -ForegroundColor Green
