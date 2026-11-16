# The homelab plugin against a real dockerd over TLS: docker:dind with a
# client-certificate API, a bridge container and a macvlan container, read by
# internal/homelab TestDockerE2E.
#   powershell -ExecutionPolicy Bypass -File scripts/homelab-docker-test.ps1
param(
    [string]$GoImage = 'golang:1.26-bookworm',
    [string]$DindImage = 'docker:27-dind'
)
$ErrorActionPreference = 'Continue'  # docker writes progress to stderr (PS 5.1 would stop on it)
$repo = Split-Path -Parent $PSScriptRoot
$net = 'gg-homelab'
$failed = $false

function Run-Docker { & docker @args 2>&1 | Out-String }

docker rm -f gg-dind 2>$null | Out-Null
docker volume rm gg-dind-certs 2>$null | Out-Null
docker network create $net 2>$null | Out-Null
try {
    # dockerd makes its CA, a server certificate for "docker" and a client
    # certificate in /certs, and listens on 2376 with --tlsverify
    docker run -d --privileged --name gg-dind --network $net --network-alias docker `
        -e DOCKER_TLS_CERTDIR=/certs -v gg-dind-certs:/certs $DindImage | Out-Null
    $ready = $false
    foreach ($i in 1..60) {
        docker exec gg-dind sh -c 'docker -H unix:///var/run/docker.sock info >/dev/null 2>&1 && test -f /certs/client/key.pem' 2>$null | Out-Null
        if ($LASTEXITCODE -eq 0) { $ready = $true; break }
        Start-Sleep -Seconds 1
    }
    if (-not $ready) { Write-Host (Run-Docker logs gg-dind); throw 'dockerd in docker:dind did not start' }
    $setup = @'
set -e
d() { docker -H unix:///var/run/docker.sock "$@"; }
d network create -d macvlan --subnet 10.99.0.0/24 -o parent=eth0 lan99 >/dev/null
d run -d --name web --label gg-web= busybox sleep 1d >/dev/null
d run -d --name iot --network lan99 --ip 10.99.0.70 --label gg-iot=yes busybox sleep 1d >/dev/null
d run -d --name other busybox sleep 1d >/dev/null
d ps
'@
    $out = Run-Docker exec gg-dind sh -c ($setup -replace "`r", '')
    Write-Host $out
    if ($LASTEXITCODE -ne 0) { throw 'setting up the containers failed' }

    $out = Run-Docker run --rm --network $net -v "${repo}:/src" -w /src -v gomod:/go/pkg/mod -v gg-dind-certs:/certs:ro `
        -e HOMELAB_DOCKER_URL=tcp://docker:2376 -e HOMELAB_DOCKER_CERTS=/certs/client $GoImage `
        go test ./internal/homelab '-run=TestDockerE2E' -count=1 -v
    Write-Host $out
    if ($out -notmatch '--- PASS: TestDockerE2E') { Write-Host 'FAIL: TestDockerE2E'; $failed = $true }
} finally {
    docker rm -f gg-dind 2>$null | Out-Null
    docker volume rm gg-dind-certs 2>$null | Out-Null
    docker network rm $net 2>$null | Out-Null
}
if ($failed) { Write-Host 'HOMELAB DOCKER TEST FAILED'; exit 1 }
Write-Host 'HOMELAB DOCKER TEST PASSED'
