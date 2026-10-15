<#
.SYNOPSIS
  End-to-end update test on a real installed firewall VM (task 051).

.DESCRIPTION
  Builds three goblin-gate test packages (0.1.99~test.1-3) that trust a throwaway
  signing key, an unattended installer ISO with version 1 (cached; -Rebuild builds
  everything again), then runs scripts/iso/update-test.sh in a privileged Debian 12
  container with KVM: installs the ISO into a VM, serves a signed APT repository on
  its WAN side and, over the API, updates 1 -> 2 (must succeed) and 2 -> 3, whose
  goblind is broken (the updater must roll back to 2). Needs Docker Desktop, Node.js
  and internet. Artifacts are copied to dist/update-test-artifacts.

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/vm-update-test.ps1
#>
[CmdletBinding()]
param([switch]$Rebuild)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
$work = Join-Path ([IO.Path]::GetTempPath()) 'goblin-update-test'
$upd = Join-Path $work 'update'
$versions = '0.1.99~test.1', '0.1.99~test.2', '0.1.99~test.3'
$isoName = 'goblin-gate-0.1.99~test.1-amd64-unattended.iso'
New-Item -ItemType Directory -Force $upd | Out-Null
if ($Rebuild) {
    foreach ($d in 'debs', 'iso') { Remove-Item -Recurse -Force (Join-Path $upd $d) -ErrorAction SilentlyContinue }
    Get-ChildItem $upd -Filter 'base-*.qcow2' | Remove-Item -Force
}
Remove-Item -Recurse -Force (Join-Path $upd 'artifacts') -ErrorAction SilentlyContinue

function Invoke-Docker([string[]]$DockerArgs) {
    $ErrorActionPreference = 'Continue'
    & docker @DockerArgs 2>&1 | ForEach-Object { Write-Host "$_" }
    return $LASTEXITCODE
}

$missing = $versions | Where-Object { -not (Test-Path (Join-Path $upd "debs\goblin-gate_$($_)_amd64.deb")) }
if ($missing) {
    Write-Host 'building the test packages'
    Push-Location (Join-Path $Root 'web')
    try { npm run build --silent; if ($LASTEXITCODE -ne 0) { throw 'web build failed' } } finally { Pop-Location }
    $code = Invoke-Docker (@('run', '--rm', '-v', "${Root}:/src", '-v', "${work}:/work", '-e', 'GOFLAGS=-buildvcs=false',
        'golang:1.26-bookworm', 'bash', '/src/scripts/iso/update-test-build.sh') + $versions)
    if ($code -ne 0) { Write-Host 'TEST PACKAGE BUILD FAILED' -ForegroundColor Red; exit 1 }
}

if (-not (Test-Path (Join-Path $upd "iso\$isoName"))) {
    Write-Host 'building the test ISO with version 1'
    $one = Join-Path $upd 'iso-debs'
    Remove-Item -Recurse -Force $one -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force $one, (Join-Path $upd 'iso') | Out-Null
    Copy-Item (Join-Path $upd "debs\goblin-gate_$($versions[0])_amd64.deb") $one
    $code = Invoke-Docker @('run', '--rm', '--privileged', '-v', "${Root}:/src", '-v', "${work}:/work",
        '-e', "VERSION=$($versions[0])", '-e', 'UNATTENDED=1', '-e', 'DEB_DIR=/work/update/iso-debs', '-e', 'OUT_DIR=/work/update/iso',
        'debian:12', 'bash', '/src/scripts/iso/build.sh')
    if ($code -ne 0) { Write-Host 'TEST ISO BUILD FAILED' -ForegroundColor Red; exit 1 }
}

$code = Invoke-Docker (@('run', '--rm', '--privileged', '-v', '/lib/modules:/lib/modules:ro',
    '-v', "$(Join-Path $Root 'scripts\iso'):/s:ro", '-v', "$(Join-Path $Root 'scripts'):/scripts:ro",
    '-v', "$(Join-Path $upd 'debs'):/debs:ro", '-v', "$(Join-Path $upd 'key'):/key:ro", '-v', "$(Join-Path $upd 'iso'):/iso:ro",
    '-v', "${work}:/work", 'debian:12', 'bash', '/s/update-test.sh', "/iso/$isoName") + $versions)

$out = Join-Path $Root 'dist\update-test-artifacts'
Remove-Item -Recurse -Force $out -ErrorAction SilentlyContinue
Copy-Item -Recurse (Join-Path $upd 'artifacts') $out -ErrorAction SilentlyContinue
if ($code -ne 0) { Write-Host "VM UPDATE TEST FAILED (artifacts in $out)" -ForegroundColor Red; exit 1 }
Write-Host 'VM UPDATE TEST PASSED' -ForegroundColor Green
