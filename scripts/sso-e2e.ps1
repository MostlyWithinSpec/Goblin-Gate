<#
.SYNOPSIS
  Single sign-on test against a real Keycloak.

.DESCRIPTION
  Starts a throwaway Keycloak in Docker on 127.0.0.1:8180, sets up a realm
  (scripts/sso-keycloak-setup.sh: client "gate", groups gg-admins and gg-help,
  users alice and sam), then runs TestSSOKeycloak in internal/api, which signs
  in through Keycloak's own login form. Removes the container afterwards.
  Needs Docker Desktop and Go.

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/sso-e2e.ps1
#>
$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
$name = 'gg-keycloak'

function Invoke-Docker([string[]]$DockerArgs) {
    $ErrorActionPreference = 'Continue'
    & docker @DockerArgs 2>&1 | ForEach-Object { Write-Host "$_" }
    return $LASTEXITCODE
}

Invoke-Docker @('rm', '-f', $name) | Out-Null
if ((Invoke-Docker @('run', '-d', '--name', $name, '-p', '127.0.0.1:8180:8080',
            '-e', 'KC_BOOTSTRAP_ADMIN_USERNAME=admin', '-e', 'KC_BOOTSTRAP_ADMIN_PASSWORD=admin',
            'quay.io/keycloak/keycloak:26.0.7', 'start-dev')) -ne 0) { throw 'could not start Keycloak' }
try {
    if ((Invoke-Docker @('cp', (Join-Path $PSScriptRoot 'sso-keycloak-setup.sh'), "${name}:/tmp/setup.sh")) -ne 0) { throw 'docker cp failed' }
    if ((Invoke-Docker @('exec', $name, 'bash', '/tmp/setup.sh')) -ne 0) { throw 'Keycloak setup failed' }
    $env:GG_SSO_KEYCLOAK = 'http://localhost:8180/realms/gg'
    Push-Location $Root
    try {
        & go test ./internal/api -run 'TestSSOKeycloak' -count=1 -v 2>&1 | Where-Object { $_ -notmatch ' (INFO|WARN) ' } | ForEach-Object { Write-Host "$_" }
        $code = $LASTEXITCODE
    } finally { Pop-Location }
    if ($code -ne 0) { Write-Host 'SSO E2E FAILED' -ForegroundColor Red; exit 1 }
    Write-Host 'SSO E2E PASSED' -ForegroundColor Green
} finally {
    Invoke-Docker @('rm', '-f', $name) | Out-Null
}
