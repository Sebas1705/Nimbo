# Builds a folder you can carry to a machine without internet: the Windows installer plus the emulator image packs,
# all checked against the release's SHA256SUMS. Run it ONCE on a machine that has internet.
#
#   powershell -File packaging\make-offline-kit.ps1 -Version 0.3.0 -Out D:\nimbo-kit
#   powershell -File packaging\make-offline-kit.ps1 -Version 0.3.0 -Out D:\nimbo-kit -Packs data,k8s   # only some packs
#
# On the offline machine: run the .msi, open Nimbo, go to Sistema > Sin conexión and press "Cargar" on each pack
# (or copy the packs to the `images` folder next to Nimbo.exe first and they are found by themselves).
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$Out,
    [string[]]$Packs = @('clouds', 'data', 'k8s'),
    [string]$Repo = 'Sebas1705/Nimbo'
)
$ErrorActionPreference = 'Stop'
$base = "https://github.com/$Repo/releases/download/v$Version"
New-Item -ItemType Directory -Force -Path (Join-Path $Out 'images') | Out-Null

function Get-Asset([string]$name, [string]$dest) {
    Write-Host "Downloading $name ..."
    Invoke-WebRequest -Uri "$base/$name" -OutFile $dest -UseBasicParsing
}

$sums = Join-Path $Out 'SHA256SUMS'
Get-Asset 'SHA256SUMS' $sums
$expected = @{}
Get-Content $sums | ForEach-Object { if ($_ -match '^([0-9a-fA-F]{64})\s+\*?(\S+)$') { $expected[$Matches[2]] = $Matches[1].ToLower() } }

$wanted = @(@{ Name = "Nimbo-$Version-windows-x64.msi"; Dir = $Out }) + ($Packs | ForEach-Object { @{ Name = "Nimbo-$Version-images-$_.tar.gz"; Dir = (Join-Path $Out 'images') } })
foreach ($w in $wanted) {
    $dest = Join-Path $w.Dir $w.Name
    Get-Asset $w.Name $dest
    $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $dest).Hash.ToLower()
    if (-not $expected.ContainsKey($w.Name)) { Remove-Item $dest; throw "$($w.Name) is not listed in SHA256SUMS" }
    if ($expected[$w.Name] -ne $actual) { Remove-Item $dest; throw "$($w.Name) does not match its checksum; it was deleted" }
    Write-Host "  ok  $($w.Name)"
}
# The packs are checked by Nimbo too when a SHA256SUMS sits beside them.
Copy-Item $sums (Join-Path $Out 'images\SHA256SUMS') -Force
Write-Host "`nKit ready in $Out. Copy that folder to the offline machine."
