<#
.SYNOPSIS
  Installs the unattended Goblin Gate ISO (scripts/build-iso.ps1 -Unattended) in
  QEMU/KVM inside a privileged Docker container and checks the first boot: UEFI with
  Secure Boot on (OVMF with Microsoft keys, i.e. Debian's signed shim) and legacy
  BIOS. The installed system reports through a self-test on the serial port: goblind
  and the data-plane services running, setup pending, firewall loaded. Uses KVM
  from the Docker Desktop VM when it is there (falls back to slow emulation).

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/vm-install-test.ps1
#>
[CmdletBinding()]
param([ValidateSet('uefi', 'bios', 'both')][string]$Mode = 'both')

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
$iso = Get-ChildItem (Join-Path $Root 'dist') -Filter '*-unattended.iso' | Sort-Object LastWriteTime | Select-Object -Last 1
if (-not $iso) { Write-Host 'no unattended ISO: run scripts/build-iso.ps1 -Unattended' -ForegroundColor Red; exit 2 }
$work = Join-Path ([IO.Path]::GetTempPath()) "goblin-vmtest-$PID"
New-Item -ItemType Directory -Force $work | Out-Null
$modes = if ($Mode -eq 'both') { 'uefi', 'bios' } else { @($Mode) }
$failed = @()
try {
    foreach ($m in $modes) {
        $ErrorActionPreference = 'Continue'
        $out = docker run --rm --privileged -v /lib/modules:/lib/modules:ro -v "$(Join-Path $Root 'dist'):/dist:ro" `
            -v "$(Join-Path $Root 'scripts\iso'):/s:ro" -v "${work}:/work" debian:12 bash /s/qemu-test.sh "/dist/$($iso.Name)" $m 2>&1
        $ErrorActionPreference = 'Stop'
        $out | ForEach-Object { "$_" }
        $text = $out -join "`n"
        $ok = $text -match "\[$m\] PASS" -and $text -match 'unit goblind active' -and $text -match '"needed":true' -and $text -notmatch 'nft-chains 0'
        if ($m -eq 'uefi' -and $text -notmatch 'SecureBoot enabled') { $ok = $false; Write-Host 'UEFI boot without Secure Boot' -ForegroundColor Red }
        if (-not $ok) { $failed += $m }
    }
} finally {
    Remove-Item -Recurse -Force $work -ErrorAction SilentlyContinue
}
if ($failed) { Write-Host "VM INSTALL TEST FAILED: $($failed -join ', ')" -ForegroundColor Red; exit 1 }
Write-Host 'VM INSTALL TEST PASSED' -ForegroundColor Green
