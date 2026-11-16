# Flow export against a real collector: GoFlow2 in Docker decodes what the
# exporter sends (IPFIX and NetFlow v9), and this script checks every record.
#   powershell -ExecutionPolicy Bypass -File scripts/flow-export-test.ps1
param(
    [string]$GoImage = 'golang:1.26-bookworm',
    [string]$GoFlowImage = 'netsampler/goflow2:v2.2.1'
)
$ErrorActionPreference = 'Continue'  # docker writes progress to stderr (PS 5.1 would stop on it)
$repo = Split-Path -Parent $PSScriptRoot
$net = 'gg-flowexport'
$failed = $false

function Run-Docker { & docker @args 2>&1 | Out-String }

docker network create $net 2>$null | Out-Null
try {
    foreach ($proto in 'ipfix', 'netflow9') {
        docker rm -f gg-goflow 2>$null | Out-Null
        docker run -d --name gg-goflow --network $net $GoFlowImage -listen 'netflow://:2055' -format json | Out-Null
        Start-Sleep -Seconds 2
        $out = Run-Docker run --rm --network $net -v "${repo}:/src" -w /src -v gomod:/go/pkg/mod `
            -e FLOWEXPORT_COLLECTOR=gg-goflow:2055 -e "FLOWEXPORT_PROTOCOL=$proto" $GoImage `
            go test ./internal/flowexport '-run=TestCollectorE2E' -count=1 -v
        if ($out -notmatch '--- PASS: TestCollectorE2E') { Write-Host $out; Write-Host "FAIL: $proto exporter test"; $failed = $true; continue }
        $logs = @(docker logs gg-goflow 2>&1 | ForEach-Object { "$_" })
        $records = @($logs | Where-Object { $_.StartsWith('{') } | ForEach-Object { $_ | ConvertFrom-Json })
        $want = @(
            @{ src = '192.168.1.10'; dst = '93.184.216.34'; sport = 51234; dport = 443; proto = 'TCP'; bytes = 900; packets = 25 },
            @{ src = '93.184.216.34'; dst = '192.168.1.10'; sport = 443; dport = 51234; proto = 'TCP'; bytes = 12000; packets = 16 },
            @{ src = '2001:db8::10'; dst = '2001:db8::53'; sport = 40000; dport = 53; proto = 'UDP'; bytes = 80; packets = 1 },
            @{ src = '2001:db8::53'; dst = '2001:db8::10'; sport = 53; dport = 40000; proto = 'UDP'; bytes = 200; packets = 1 }
        )
        foreach ($w in $want) {
            $hit = @($records | Where-Object { $_.src_addr -eq $w.src -and $_.dst_addr -eq $w.dst -and $_.src_port -eq $w.sport -and
                    $_.dst_port -eq $w.dport -and $_.proto -eq $w.proto -and $_.bytes -eq $w.bytes -and $_.packets -eq $w.packets })
            if ($hit.Count -ne 1) { Write-Host "FAIL: $proto record $($w.src):$($w.sport) -> $($w.dst):$($w.dport) not decoded"; $failed = $true }
        }
        $errors = @($logs | Where-Object { $_ -match 'level=(WARN|ERROR)' })
        if ($errors.Count -gt 0) { $errors | ForEach-Object { Write-Host $_ }; Write-Host "FAIL: $proto collector warnings"; $failed = $true }
        if (-not $failed) { Write-Host "ok: $proto ($($records.Count) records decoded by GoFlow2)" }
    }
} finally {
    docker rm -f gg-goflow 2>$null | Out-Null
    docker network rm $net 2>$null | Out-Null
}
if ($failed) { exit 1 }
Write-Host 'flow export test passed'
