<#
.SYNOPSIS
  Tests the Home Assistant plugin's MQTT side against Debian 12's mosquitto:
  discovery and states retained, a switch command reaching the policy switch,
  Home Assistant's birth message, the last will, a clean stop
  (internal/homeassistant TestMosquittoE2E). Runs in a throwaway Debian 12
  container; needs Docker Desktop and internet. Not part of verify.ps1.

  -HomeAssistant also runs a real Home Assistant (container image) with the MQTT
  integration against the bridge and a fake goblind: the entities must appear
  with the right states, the policy switch must reach goblind, and Home
  Assistant's log must have no MQTT errors or warnings.

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/linux-ha-test.ps1 -HomeAssistant
#>
[CmdletBinding()]
param(
    [switch]$HomeAssistant,
    [string]$HAImage = 'ghcr.io/home-assistant/home-assistant:2025.10.0',
    [string]$BrokerImage = 'eclipse-mosquitto:2.0.18'
)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot

if (-not (Get-Command docker -ErrorAction SilentlyContinue)) { Write-Host 'docker not found' -ForegroundColor Red; exit 2 }
docker version --format '{{.Server.Version}}' *> $null
if ($LASTEXITCODE -ne 0) { Write-Host 'Docker engine is not running - start Docker Desktop.' -ForegroundColor Red; exit 2 }

function Test-HomeAssistant([string]$work) {
    $net = "gg-ha-$PID"
    $ErrorActionPreference = 'Continue'
    try {
        docker network create $net | Out-Null
        docker run -d --name "$net-broker" --network $net --network-alias broker $BrokerImage mosquitto -c /mosquitto-no-auth.conf | Out-Null
        docker run -d --name "$net-bridge" --network $net -v "${work}:/w" -e GOBLIN_HA_SERVE=broker:1883 debian:12 /w/ha.test '-test.run=TestServeForHomeAssistant' '-test.v' | Out-Null
        docker run -d --name "$net-ha" --network $net -p 18123:8123 $HAImage | Out-Null
        $ErrorActionPreference = 'Stop'
        $ha = 'http://localhost:18123'
        $client = "$ha/"
        Write-Host "waiting for Home Assistant ($HAImage)..."
        $up = $false
        for ($i = 0; $i -lt 90 -and -not $up; $i++) {
            try { Invoke-RestMethod "$ha/api/onboarding" -TimeoutSec 5 | Out-Null; $up = $true } catch { Start-Sleep 2 }
        }
        if (-not $up) { throw 'Home Assistant did not come up' }
        $user = Invoke-RestMethod -Method Post "$ha/api/onboarding/users" -ContentType 'application/json' -Body (@{
                client_id = $client; name = 'Test'; username = 'test'; password = 'goblin-test-pass-1'; language = 'en'
            } | ConvertTo-Json)
        $tok = Invoke-RestMethod -Method Post "$ha/auth/token" -ContentType 'application/x-www-form-urlencoded' `
            -Body "grant_type=authorization_code&code=$($user.auth_code)&client_id=$([uri]::EscapeDataString($client))"
        $h = @{ Authorization = "Bearer $($tok.access_token)" }
        $flow = Invoke-RestMethod -Method Post "$ha/api/config/config_entries/flow" -Headers $h -ContentType 'application/json' -Body '{"handler":"mqtt","show_advanced_options":false}'
        $res = Invoke-RestMethod -Method Post "$ha/api/config/config_entries/flow/$($flow.flow_id)" -Headers $h -ContentType 'application/json' -Body '{"broker":"broker","port":1883}'
        if ($res.type -ne 'create_entry') { throw "MQTT setup: $($res | ConvertTo-Json -Depth 5)" }

        $want = [ordered]@{
            'Goblin Gate WAN wan1'              = 'on'
            'Goblin Gate WAN wan1 IPv4 address' = '24.101.68.119'
            'Goblin Gate Gateway fiber'         = 'on'
            'Goblin Gate Gateway fiber latency' = '4'
            'Goblin Gate Gateway lte'           = 'unknown'
            'Goblin Gate CPU'                   = '3.3'
            'Goblin Gate Memory used'           = '25'
            'Goblin Gate Sessions'              = '321'
            'Goblin Gate Policy guest-internet' = 'on'
            "Sam's phone"                       = 'home'
            'aa:bb:cc:dd:ee:02'                 = 'not_home'
        }
        $byName = @{}
        for ($i = 0; $i -lt 45; $i++) {
            $byName = @{}
            # assigned first: Windows PowerShell hands a JSON array on as one object
            $states = Invoke-RestMethod "$ha/api/states" -Headers $h
            foreach ($st in $states) { $byName[[string]$st.attributes.friendly_name] = $st }
            $missing = @($want.Keys | Where-Object { -not $byName.ContainsKey($_) -or $byName[$_].state -ne $want[$_] })
            if ($missing.Count -eq 0) { break }
            Start-Sleep 2
        }
        $bad = 0
        foreach ($k in $want.Keys) {
            $ok = $byName.ContainsKey($k) -and $byName[$k].state -eq $want[$k]
            $got = '(missing)'
            if ($byName.ContainsKey($k)) { $got = "$($byName[$k].entity_id) = $($byName[$k].state)" }
            if (-not $ok) { $bad++ }
            $mark = 'BAD'
            if ($ok) { $mark = 'ok' }
            Write-Host ("  {0,-4} {1,-36} {2}" -f $mark, $k, $got)
        }
        if ($bad -gt 0) {
            Write-Host "  Home Assistant has $($byName.Count) entities: $(($byName.Keys | Sort-Object) -join ', ')"
            $ErrorActionPreference = 'Continue'
            Write-Host '  broker log:'; docker logs --tail 10 "$net-broker" 2>&1 | ForEach-Object { Write-Host "    $_" }
            Write-Host '  bridge log:'; docker logs --tail 10 "$net-bridge" 2>&1 | ForEach-Object { Write-Host "    $_" }
            $ErrorActionPreference = 'Stop'
        }
        $boot = $byName['Goblin Gate Boot time']
        if (-not $boot -or $boot.attributes.device_class -ne 'timestamp' -or $boot.state -notmatch '^\d{4}-') {
            Write-Host "  BAD  boot time: $($boot | ConvertTo-Json -Compress)"; $bad++
        }

        $sw = $byName['Goblin Gate Policy guest-internet'].entity_id
        Invoke-RestMethod -Method Post "$ha/api/services/switch/turn_off" -Headers $h -ContentType 'application/json' -Body (@{ entity_id = $sw } | ConvertTo-Json) | Out-Null
        $off = $false
        for ($i = 0; $i -lt 15 -and -not $off; $i++) {
            Start-Sleep 1
            $off = (Invoke-RestMethod "$ha/api/states/$sw" -Headers $h).state -eq 'off'
        }
        $ErrorActionPreference = 'Continue'
        $bridgeLog = docker logs "$net-bridge" 2>&1 | Out-String
        $haLog = docker logs "$net-ha" 2>&1 | Out-String
        $ErrorActionPreference = 'Stop'
        if ($off -and $bridgeLog -match 'switched guest-internet false') { Write-Host '  ok   switch turned off in Home Assistant reached goblind' }
        else { Write-Host "  BAD  switch: off=$off; bridge log:`n$bridgeLog"; $bad++ }
        $mqttErrors = @($haLog -split "`n" | Where-Object { $_ -match '(ERROR|WARNING).*mqtt' })
        if ($mqttErrors.Count -gt 0) {
            Write-Host '  BAD  Home Assistant logged MQTT problems:'; $mqttErrors | ForEach-Object { Write-Host "       $_" }; $bad++
        } else { Write-Host '  ok   no MQTT errors or warnings in the Home Assistant log' }
        if ($bad -gt 0) { return 1 }
        return 0
    } catch {
        Write-Host "Home Assistant test: $_" -ForegroundColor Red
        $ErrorActionPreference = 'Continue'
        docker logs --tail 40 "$net-ha" 2>&1 | Out-String | Write-Host
        return 1
    } finally {
        $ErrorActionPreference = 'Continue'
        docker rm -f "$net-ha" "$net-bridge" "$net-broker" 2>&1 | Out-Null
        docker network rm $net 2>&1 | Out-Null
    }
}

$work = Join-Path ([IO.Path]::GetTempPath()) "goblin-ha-$PID"
New-Item -ItemType Directory -Force $work | Out-Null
try {
    Push-Location $Root
    $env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
    try { go test -c -o (Join-Path $work 'ha.test') ./internal/homeassistant } finally { Remove-Item Env:GOOS, Env:GOARCH, Env:CGO_ENABLED; Pop-Location }
    if ($LASTEXITCODE -ne 0) { throw 'building the test binary failed' }

    $script = @'
set -eo pipefail
apt-get update -qq >/dev/null
DEBIAN_FRONTEND=noninteractive apt-get install -y -qq mosquitto >/dev/null 2>&1
echo "$(mosquitto -h | head -1)"
cd /w && GOBLIN_MQTT_E2E=1 ./ha.test -test.run '^TestMosquittoE2E$' -test.v 2>&1 | grep -v "^=== RUN"
'@
    [IO.File]::WriteAllText((Join-Path $work 'run.sh'), ($script -replace "`r", ''), (New-Object System.Text.UTF8Encoding $false))
    $ErrorActionPreference = 'Continue'
    docker run --rm -v "${work}:/w" debian:12 bash /w/run.sh
    $code = $LASTEXITCODE
    $ErrorActionPreference = 'Stop'
    if ($code -eq 0 -and $HomeAssistant) { $code = Test-HomeAssistant $work }
} finally {
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}
if ($code -ne 0) { Write-Host 'HOME ASSISTANT E2E FAILED' -ForegroundColor Red; exit 1 }
Write-Host 'HOME ASSISTANT E2E PASSED' -ForegroundColor Green
