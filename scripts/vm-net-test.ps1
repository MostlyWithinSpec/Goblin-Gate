<#
.SYNOPSIS
  End-to-end network test on a real installed firewall VM (task 039).

.DESCRIPTION
  Runs scripts/iso/net-test.sh in a privileged Debian 12 container with KVM: installs
  the newest unattended ISO from dist/ (scripts/build-iso.ps1 -Unattended) into a VM
  once (cached until the ISO changes), boots a fresh copy with its WAN and LAN ports on
  two bridges, puts an "internet" (DHCP, DNS, web server, NAT to the real internet) and
  a LAN client in network namespaces on them, runs the setup wizard over the API and
  checks DHCP, DNS, NAT, port forward, deny policy + log, DNS filter, threat feed and an
  IPS alert. The IPS check downloads ET Open, so it needs internet access.

  Artifacts (serial log, API dumps, dnsmasq log, per-check output) are copied to
  dist/net-test-artifacts.

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/vm-net-test.ps1
#>
[CmdletBinding()]
param([switch]$Reinstall)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
$iso = Get-ChildItem (Join-Path $Root 'dist') -Filter '*-unattended.iso' | Sort-Object LastWriteTime | Select-Object -Last 1
if (-not $iso) { Write-Host 'no unattended ISO: run scripts/build-iso.ps1 -Unattended' -ForegroundColor Red; exit 2 }
$work = Join-Path ([IO.Path]::GetTempPath()) 'goblin-net-test'
New-Item -ItemType Directory -Force (Join-Path $work 'net') | Out-Null
if ($Reinstall) { Get-ChildItem (Join-Path $work 'net') -Filter 'base-*.qcow2' | Remove-Item -Force }
Remove-Item -Recurse -Force (Join-Path $work 'net\artifacts') -ErrorAction SilentlyContinue

$ErrorActionPreference = 'Continue'
docker run --rm --privileged -v /lib/modules:/lib/modules:ro -v "$(Join-Path $Root 'dist'):/dist:ro" `
    -v "$(Join-Path $Root 'scripts\iso'):/s:ro" -v "${work}:/work" debian:12 bash /s/net-test.sh "/dist/$($iso.Name)" 2>&1 |
    ForEach-Object { "$_" }
$code = $LASTEXITCODE
$ErrorActionPreference = 'Stop'

$out = Join-Path $Root 'dist\net-test-artifacts'
Remove-Item -Recurse -Force $out -ErrorAction SilentlyContinue
Copy-Item -Recurse (Join-Path $work 'net\artifacts') $out -ErrorAction SilentlyContinue
if ($code -ne 0) { Write-Host "VM NETWORK TEST FAILED (artifacts in $out)" -ForegroundColor Red; exit 1 }
Write-Host 'VM NETWORK TEST PASSED' -ForegroundColor Green
