<#
.SYNOPSIS
  Dynamic routing test against real FRR.

.DESCRIPTION
  Builds an image with Debian 12's FRR (internal/frr/testdata/image), then runs
  TestFRRE2E in internal/frr: the gate's rendered frr.conf in one container and
  a second FRR as its neighbour, on a Docker network. Checks vtysh's config
  check, the BGP session over IPv4 (with a password) and IPv6, the OSPF
  adjacency, learned, filtered and announced routes, a reload that keeps the
  session, and the status reader. Removes its containers afterwards.
  -Record also saves FRR's JSON answers to internal/frr/testdata/recorded.
  Needs Docker Desktop and Go.

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/frr-e2e.ps1
#>
param([switch]$Record)
$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot

$ErrorActionPreference = 'Continue'
& docker build -q -t gg-frr-test (Join-Path $Root 'internal/frr/testdata/image') 2>&1 | ForEach-Object { Write-Host "$_" }
if ($LASTEXITCODE -ne 0) { Write-Host 'image build failed' -ForegroundColor Red; exit 1 }
$env:GG_FRR_E2E = '1'
if ($Record) { $env:GG_FRR_RECORD = '1' }
Push-Location $Root
try {
    & go test ./internal/frr -run 'TestFRRE2E' -count=1 -v 2>&1 | ForEach-Object { Write-Host "$_" }
    $code = $LASTEXITCODE
} finally { Pop-Location }
if ($code -ne 0) { Write-Host 'FRR E2E FAILED' -ForegroundColor Red; exit 1 }
Write-Host 'FRR E2E PASSED' -ForegroundColor Green
