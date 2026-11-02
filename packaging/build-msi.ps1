# Builds the Windows installer (WiX 3: candle + light, on PATH or under Program Files (x86)\WiX Toolset v3.x).
#   powershell -File packaging\build-msi.ps1 -Version 0.2.1 -Exe build\bin\Nimbo.exe -Out dist\Nimbo-0.2.1-windows-x64.msi
param(
    [Parameter(Mandatory = $true)][string]$Version,
    [Parameter(Mandatory = $true)][string]$Exe,
    [Parameter(Mandatory = $true)][string]$Out
)
$ErrorActionPreference = "Stop"
$root = Split-Path -Parent $PSScriptRoot

if ($Version -notmatch '^\d+\.\d+\.\d+$') { throw "An MSI version is three numbers (x.y.z), got '$Version'" }

function Find-Wix([string]$tool) {
    $cmd = Get-Command $tool -ErrorAction SilentlyContinue
    if ($cmd) { return $cmd.Source }
    $dir = Get-ChildItem "${env:ProgramFiles(x86)}" -Filter "WiX Toolset v3*" -Directory -ErrorAction SilentlyContinue | Sort-Object Name -Descending | Select-Object -First 1
    if ($dir -and (Test-Path (Join-Path $dir.FullName "bin\$tool"))) { return (Join-Path $dir.FullName "bin\$tool") }
    throw "$tool not found: install WiX Toolset 3 (the windows-2022 runner image has it)."
}
$candle = Find-Wix "candle.exe"
$light = Find-Wix "light.exe"

$work = Join-Path ([IO.Path]::GetTempPath()) ("nimbo-msi-" + [guid]::NewGuid().ToString("N"))
New-Item -ItemType Directory -Force $work | Out-Null
try {
    # The license is shown by Windows Installer's properties only, so plain text is enough.
    Copy-Item (Join-Path $root "LICENSE") (Join-Path $work "LICENSE.txt")
    & $candle -nologo -arch x64 -ext WixUtilExtension `
        "-dProductVersion=$Version" "-dExe=$((Resolve-Path $Exe).Path)" `
        "-dIcon=$(Join-Path $root 'packaging\nimbo.ico')" "-dLicense=$(Join-Path $work 'LICENSE.txt')" `
        -out (Join-Path $work "nimbo.wixobj") (Join-Path $root "packaging\nimbo.wxs")
    if ($LASTEXITCODE -ne 0) { throw "candle failed" }
    New-Item -ItemType Directory -Force (Split-Path -Parent (Join-Path (Get-Location) $Out)) | Out-Null
    & $light -nologo -ext WixUtilExtension -sval -out (Join-Path (Get-Location) $Out) (Join-Path $work "nimbo.wixobj")
    if ($LASTEXITCODE -ne 0) { throw "light failed" }
    Write-Host "wrote $Out"
}
finally {
    Remove-Item $work -Recurse -Force -ErrorAction SilentlyContinue
}
