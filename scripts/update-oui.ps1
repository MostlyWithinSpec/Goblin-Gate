<#
.SYNOPSIS
  Regenerates internal/devices/oui.txt.gz (MAC vendor table of the device inventory) from the IEEE MA-L
  registry.

.DESCRIPTION
  Downloads https://standards-oui.ieee.org/oui/oui.csv and writes one "aabbcc<TAB>Vendor" line per
  assignment, sorted, gzip-compressed. Run it now and then and commit the result.

.EXAMPLE
  powershell -NoProfile -ExecutionPolicy Bypass -File scripts/update-oui.ps1
#>
[CmdletBinding()]
param([string]$Url = 'https://standards-oui.ieee.org/oui/oui.csv')
$ErrorActionPreference = 'Stop'
$Root = Split-Path -Parent $PSScriptRoot
$out = Join-Path $Root 'internal\devices\oui.txt.gz'
$csv = Join-Path ([IO.Path]::GetTempPath()) 'goblin-oui.csv'
# the IEEE site refuses requests without a browser-like user agent
Invoke-WebRequest -Uri $Url -OutFile $csv -UseBasicParsing -UserAgent 'Mozilla/5.0 (goblin-gate update-oui)'
$rows = Import-Csv $csv
if ($rows.Count -lt 10000) { throw "only $($rows.Count) rows in $Url" }
$lines = $rows | Where-Object { $_.Registry -eq 'MA-L' -and $_.Assignment -match '^[0-9A-Fa-f]{6}$' } |
    ForEach-Object { '{0}{1}{2}' -f $_.Assignment.ToLower(), "`t", ($_.'Organization Name' -replace '\s+', ' ').Trim() } |
    Sort-Object -Unique
$text = "# OUI vendor table (IEEE MA-L, $(Get-Date -Format yyyy-MM-dd)). Regenerate with scripts/update-oui.ps1.`n" + ($lines -join "`n") + "`n"
$fs = [IO.File]::Create($out)
$gz = New-Object IO.Compression.GZipStream($fs, [IO.Compression.CompressionLevel]::Optimal)
$bytes = [Text.Encoding]::UTF8.GetBytes($text)
$gz.Write($bytes, 0, $bytes.Length)
$gz.Close(); $fs.Close()
Remove-Item $csv
"{0} vendors -> {1} ({2:N0} bytes)" -f $lines.Count, $out, (Get-Item $out).Length
