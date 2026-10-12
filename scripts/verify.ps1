<#
.SYNOPSIS
  Goblin Gate checks: run before every commit and by CI.
  Exit code 0 = all checks passed.

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/verify.ps1 -SkipWeb
#>
[CmdletBinding()]
param(
    [switch]$SkipWeb,
    [switch]$SkipGo
)

$ErrorActionPreference = 'Continue'
$Root = Split-Path -Parent $PSScriptRoot
Set-Location -LiteralPath $Root

$script:Failed = New-Object System.Collections.Generic.List[string]
$script:Passed = New-Object System.Collections.Generic.List[string]

function Invoke-Step {
    param([string]$Name, [scriptblock]$Block)
    Write-Host ""
    Write-Host "=== $Name ===" -ForegroundColor Cyan
    $global:LASTEXITCODE = 0
    $output = & $Block 2>&1 | ForEach-Object { "$_" }
    $code = $LASTEXITCODE
    if ($output) { $output | ForEach-Object { Write-Host $_ } }
    if ($code -ne 0) {
        Write-Host "FAIL: $Name (exit $code)" -ForegroundColor Red
        $script:Failed.Add($Name)
    } else {
        Write-Host "ok: $Name" -ForegroundColor Green
        $script:Passed.Add($Name)
    }
}

function Test-Tool([string]$Name) {
    return [bool](Get-Command $Name -ErrorAction SilentlyContinue)
}

# ---------------------------------------------------------------- Go
if (-not $SkipGo) {
    if (-not (Test-Path (Join-Path $Root 'go.mod'))) {
        Write-Host "No go.mod yet - skipping Go checks." -ForegroundColor Yellow
    } elseif (-not (Test-Tool 'go')) {
        Write-Host "Go is not installed or not on PATH." -ForegroundColor Red
        $script:Failed.Add('go installed')
    } else {
        Invoke-Step 'gofmt' {
            $dirs = @('cmd', 'internal', 'packaging') | Where-Object { Test-Path $_ }
            $bad = @()
            if ($dirs) { $bad = @(& gofmt -l $dirs) }
            if ($bad) {
                "These files need gofmt (run: gofmt -w <file>):"
                $bad
                $global:LASTEXITCODE = 1
            } else { $global:LASTEXITCODE = 0 }
        }
        Invoke-Step 'go vet' { go vet ./... }
        Invoke-Step 'go build (windows)' { go build ./... }
        Invoke-Step 'go build (linux)' {
            $oldOS = $env:GOOS; $oldArch = $env:GOARCH; $oldCgo = $env:CGO_ENABLED
            $env:GOOS = 'linux'; $env:GOARCH = 'amd64'; $env:CGO_ENABLED = '0'
            try { go build ./... }
            finally { $env:GOOS = $oldOS; $env:GOARCH = $oldArch; $env:CGO_ENABLED = $oldCgo }
        }
        Invoke-Step 'go vet (linux)' {
            $oldOS = $env:GOOS; $oldCgo = $env:CGO_ENABLED
            $env:GOOS = 'linux'; $env:CGO_ENABLED = '0'
            try { go vet ./... }
            finally { $env:GOOS = $oldOS; $env:CGO_ENABLED = $oldCgo }
        }
        Invoke-Step 'go test' { go test -count=1 ./... }
    }
}

# ---------------------------------------------------------------- Web
if (-not $SkipWeb) {
    $pkg = Join-Path $Root 'web/package.json'
    if (-not (Test-Path $pkg)) {
        Write-Host "No web/package.json yet - skipping web checks." -ForegroundColor Yellow
    } elseif (-not (Test-Tool 'npm')) {
        Write-Host "Node.js/npm is not installed or not on PATH." -ForegroundColor Red
        $script:Failed.Add('npm installed')
    } else {
        Push-Location (Join-Path $Root 'web')
        try {
            if (-not (Test-Path 'node_modules')) {
                if (Test-Path 'package-lock.json') {
                    Invoke-Step 'npm ci' { npm ci --no-audit --no-fund }
                } else {
                    Invoke-Step 'npm install' { npm install --no-audit --no-fund }
                }
            }
            $scripts = (Get-Content -Raw 'package.json' | ConvertFrom-Json).scripts
            if ($scripts.lint)  { Invoke-Step 'web lint (tsc)' { npm run lint --silent } }
            if ($scripts.build) {
                Invoke-Step 'web build' { npm run build --silent }
                # Tailwind must actually run (without postcss.config.cjs the UI
                # shipped unstyled and every test still passed).
                Invoke-Step 'web styles' {
                    $css = Get-ChildItem 'dist/assets' -Filter '*.css' -ErrorAction SilentlyContinue | Get-Content -Raw
                    if ($css -match '\.flex\{' -and $css -notmatch '@tailwind') { $global:LASTEXITCODE = 0 }
                    else { 'dist/assets/*.css has no Tailwind utilities: is postcss.config.cjs missing?'; $global:LASTEXITCODE = 1 }
                }
            }
            if ($scripts.test)  { Invoke-Step 'web test' { npm test --silent } }
        } finally { Pop-Location }
    }
}

# ---------------------------------------------------------------- Summary
Write-Host ""
Write-Host "=== verify summary ===" -ForegroundColor Cyan
Write-Host ("passed: {0}" -f ($script:Passed -join ', '))
if ($script:Failed.Count -gt 0) {
    Write-Host ("FAILED: {0}" -f ($script:Failed -join ', ')) -ForegroundColor Red
    exit 1
}
Write-Host "ALL CHECKS PASSED" -ForegroundColor Green
exit 0
