<#
.SYNOPSIS
  Upgrade-path test: installs an old release in a VM and updates it straight to the current build.

.DESCRIPTION
  Builds the old release (default v0.1.0-beta.1) from its git tag and the current tree, both as
  packages that trust a throwaway signing key (shared with vm-update-test.ps1), an unattended
  installer ISO of the old release made with that release's own ISO script, then runs
  scripts/iso/upgrade-path-test.sh in a privileged Debian 12 container with KVM: install, set up a
  config with everything the old release could do, update through the box's own updater, and check
  that the config came through and the box still works. Needs Docker Desktop, Node.js, git and
  internet. Artifacts are copied to dist/upgrade-path-artifacts.

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/vm-upgrade-path-test.ps1
#>
[CmdletBinding()]
param(
    [string]$FromTag = 'v0.1.0-beta.1',
    [string]$NewVersion = '0.1.0~beta.99',
    [switch]$Rebuild
)

$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
$work = Join-Path ([IO.Path]::GetTempPath()) 'goblin-upgrade-path'
$path = Join-Path $work 'upgrade'
$old = $FromTag.TrimStart('v') -replace '-', '~'
$src = Join-Path $work 'old-src'
$isoName = "goblin-gate-$old-amd64-unattended.iso"
New-Item -ItemType Directory -Force (Join-Path $path 'debs'), (Join-Path $path 'key') | Out-Null
if ($Rebuild) {
    foreach ($d in 'debs', 'iso') { Remove-Item -Recurse -Force (Join-Path $path $d) -ErrorAction SilentlyContinue }
    Get-ChildItem $path -Filter 'base-*.qcow2' -ErrorAction SilentlyContinue | Remove-Item -Force
    New-Item -ItemType Directory -Force (Join-Path $path 'debs') | Out-Null
}
Remove-Item -Recurse -Force (Join-Path $path 'artifacts') -ErrorAction SilentlyContinue

function Invoke-Docker([string[]]$DockerArgs) {
    $ErrorActionPreference = 'Continue'
    & docker @DockerArgs 2>&1 | ForEach-Object { Write-Host "$_" }
    return $LASTEXITCODE
}

# the throwaway signing key: reuse vm-update-test.ps1's, or make one
$keyDir = Join-Path $path 'key'
$shared = Join-Path ([IO.Path]::GetTempPath()) 'goblin-update-test\update\key'
if (-not (Test-Path (Join-Path $keyDir 'test.asc')) -and (Test-Path (Join-Path $shared 'test.asc'))) {
    Copy-Item (Join-Path $shared '*') $keyDir
}
if (-not (Test-Path (Join-Path $keyDir 'test.asc'))) {
    # a script file: Windows PowerShell mangles quotes in native arguments
    $gen = Join-Path $keyDir 'gen.sh'
    [IO.File]::WriteAllText($gen, "set -e`nexport GNUPGHOME=`$(mktemp -d)`ngpg --batch --quiet --passphrase '' --quick-gen-key 'Goblin Gate update test <test@invalid>' ed25519 sign never`ngpg --batch --armor --export-secret-keys > /k/test.asc`ngpg --batch --export > /k/test.gpg`n")
    $code = Invoke-Docker @('run', '--rm', '-v', "${keyDir}:/k", 'golang:1.26-bookworm', 'bash', '/k/gen.sh')
    Remove-Item $gen
    if ($code -ne 0) { throw 'key generation failed' }
}

# the old release's source, at its tag
if (-not (Test-Path (Join-Path $src '.git'))) {
    Remove-Item -Recurse -Force $src -ErrorAction SilentlyContinue
    git -C $Root worktree add -f --detach $src $FromTag | Out-Null
    if ($LASTEXITCODE -ne 0) { throw "git worktree for $FromTag failed" }
}

function Build-Deb([string]$Tree, [string]$Version) {
    if (Test-Path (Join-Path $path "debs\goblin-gate_$($Version)_amd64.deb")) { return }
    Write-Host "building goblin-gate $Version from $Tree"
    Push-Location (Join-Path $Tree 'web')
    try {
        if (-not (Test-Path 'node_modules')) { npm ci --no-audit --no-fund --silent; if ($LASTEXITCODE -ne 0) { throw 'npm ci failed' } }
        npm run build --silent; if ($LASTEXITCODE -ne 0) { throw 'web build failed' }
    } finally { Pop-Location }
    $code = Invoke-Docker @('run', '--rm', '-v', "${Tree}:/src", '-v', "${path}:/up", '-w', '/src', '-e', 'GOFLAGS=-buildvcs=false',
        '-e', "VERSION=$Version", '-e', 'OUT_DIR=/up/debs', '-e', 'ARCHIVE_KEYRING=/up/key/test.gpg', 'golang:1.26-bookworm', 'bash', 'scripts/build-deb.sh')
    if ($code -ne 0) { throw "package build of $Version failed" }
}
Build-Deb $src $old
Build-Deb $Root $NewVersion

# the old release's own ISO script, with only its package
if (-not (Test-Path (Join-Path $path "iso\$isoName"))) {
    Write-Host "building the $old installer ISO"
    $one = Join-Path $path 'iso-debs'
    Remove-Item -Recurse -Force $one -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force $one, (Join-Path $path 'iso') | Out-Null
    Copy-Item (Join-Path $path "debs\goblin-gate_$($old)_amd64.deb") $one
    $code = Invoke-Docker @('run', '--rm', '--privileged', '-v', "${src}:/src", '-v', "${work}:/work",
        '-e', "VERSION=$old", '-e', 'UNATTENDED=1', '-e', 'DEB_DIR=/work/upgrade/iso-debs', '-e', 'OUT_DIR=/work/upgrade/iso',
        'debian:12', 'bash', '/src/scripts/iso/build.sh')
    if ($code -ne 0) { Write-Host 'ISO BUILD FAILED' -ForegroundColor Red; exit 1 }
}

$code = Invoke-Docker @('run', '--rm', '--privileged', '-v', '/lib/modules:/lib/modules:ro',
    '-v', "$(Join-Path $Root 'scripts\iso'):/s:ro", '-v', "$(Join-Path $Root 'scripts'):/scripts:ro",
    '-v', "$(Join-Path $path 'debs'):/debs:ro", '-v', "$(Join-Path $path 'key'):/key:ro", '-v', "$(Join-Path $path 'iso'):/iso:ro",
    '-v', "${work}:/work", 'debian:12', 'bash', '/s/upgrade-path-test.sh', "/iso/$isoName", $old, $NewVersion)

$out = Join-Path $Root 'dist\upgrade-path-artifacts'
Remove-Item -Recurse -Force $out -ErrorAction SilentlyContinue
Copy-Item -Recurse (Join-Path $path 'artifacts') $out -ErrorAction SilentlyContinue
if ($code -ne 0) { Write-Host "UPGRADE PATH TEST FAILED (artifacts in $out)" -ForegroundColor Red; exit 1 }
Write-Host 'UPGRADE PATH TEST PASSED' -ForegroundColor Green
