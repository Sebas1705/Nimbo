# Smoke test of the portable Windows app.
#   powershell -File packaging\smoke-test.ps1 -Zip backend\build\distributions\Nimbo-0.2.0-windows-x64.zip
#
# Unzips it somewhere temporary, starts Nimbo.exe without a window (NIMBO_HEADLESS=1) and checks what a user
# relies on: it stays alive, serves the UI, the API and the bundled labs, a second launch reuses the running one
# instead of starting another, and a port held by another program makes it pick a free one.
param(
    [Parameter(Mandatory = $true)][string]$Zip,
    [int]$Port = 18780
)

$ErrorActionPreference = "Stop"
$work = Join-Path ([IO.Path]::GetTempPath()) ("nimbo-smoke-" + [guid]::NewGuid().ToString("N"))
$started = New-Object System.Collections.Generic.List[System.Diagnostics.Process]
$listener = $null
$failures = New-Object System.Collections.Generic.List[string]

function Check([string]$what, [bool]$ok, [string]$detail = "") {
    if ($ok) { Write-Host "  ok   $what" } else { Write-Host "  FAIL $what $detail"; $script:failures.Add($what) }
}

function WaitHealthy([int]$port, [int]$seconds) {
    for ($i = 0; $i -lt $seconds * 2; $i++) {
        try { return Invoke-RestMethod "http://127.0.0.1:$port/api/health" -TimeoutSec 2 } catch { Start-Sleep -Milliseconds 500 }
    }
    return $null
}

try {
    New-Item -ItemType Directory -Force $work | Out-Null
    Expand-Archive -LiteralPath (Resolve-Path $Zip) -DestinationPath $work
    $exe = Join-Path $work "Nimbo\Nimbo.exe"
    Check "the zip contains Nimbo.exe" (Test-Path $exe)
    Check "it brings its own Java runtime" ((Test-Path (Join-Path $work "Nimbo\runtime\bin\server\jvm.dll")) -and (Test-Path (Join-Path $work "Nimbo\runtime\lib\modules")))

    # Keep the app's data (the log) inside the temp folder, and never open a browser or a tray icon.
    $env:LOCALAPPDATA = Join-Path $work "localappdata"
    $env:NIMBO_HEADLESS = "1"
    $env:NIMBO_PORT = "$Port"

    Write-Host "first launch"
    $first = Start-Process $exe -PassThru
    $started.Add($first)
    $health = WaitHealthy $Port 40
    Check "it answers /api/health" ($null -ne $health -and $health.status -eq "ok")
    Check "it reports a version" ($null -ne $health -and -not [string]::IsNullOrEmpty($health.version))
    Start-Sleep -Seconds 4
    Check "it is still alive a few seconds later" (-not $first.HasExited)
    $ui = Invoke-WebRequest "http://127.0.0.1:$Port/" -UseBasicParsing
    Check "it serves the UI" ($ui.StatusCode -eq 200 -and $ui.Content -match "<title>Nimbo</title>")
    Check "the labs are bundled" ((Invoke-RestMethod "http://127.0.0.1:$Port/api/labs").labs.Count -ge 8)
    Check "the stacks are bundled" ((Invoke-RestMethod "http://127.0.0.1:$Port/api/stacks").stacks.Count -ge 3)

    Write-Host "second launch on the same port"
    $second = Start-Process $exe -PassThru
    $started.Add($second)
    $exited = $second.WaitForExit(20000)
    Check "the second launch exits by itself" $exited
    Check "and the first one keeps running" (-not $first.HasExited)

    Write-Host "port held by another program"
    $other = $Port + 1
    $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Parse("127.0.0.1"), $other)
    $listener.Start()
    $env:NIMBO_PORT = "$other"
    # its own data folder, so the log read below holds only this launch
    $env:LOCALAPPDATA = Join-Path $work "localappdata-third"
    $third = Start-Process $exe -PassThru
    $started.Add($third)
    $log = Join-Path $env:LOCALAPPDATA "Nimbo\nimbo.log"
    $line = $null
    for ($i = 0; $i -lt 60 -and -not $line; $i++) {
        Start-Sleep -Milliseconds 500
        if (Test-Path $log) { $line = Get-Content $log | Select-String "listening on http://127.0.0.1:(\d+)" | Select-Object -Last 1 }
    }
    $fallback = if ($line) { [int]([regex]::Match($line.Line, ":(\d+)$").Groups[1].Value) } else { 0 }
    Check "it picks another port" ($fallback -gt 0 -and $fallback -ne $other -and $fallback -ne $Port) "(log said: $($line.Line))"
    $h3 = if ($fallback -gt 0) { WaitHealthy $fallback 20 } else { $null }
    Check "and answers there" ($null -ne $h3 -and $h3.status -eq "ok")
    Check "the log file is written" (Test-Path $log)
}
finally {
    if ($listener) { $listener.Stop() }
    foreach ($p in $started) { if (-not $p.HasExited) { Stop-Process -Id $p.Id -Force -ErrorAction SilentlyContinue } }
    Start-Sleep -Seconds 1
    Remove-Item $work -Recurse -Force -ErrorAction SilentlyContinue
}

if ($failures.Count -gt 0) {
    Write-Host "`n$($failures.Count) check(s) failed: $($failures -join '; ')"
    exit 1
}
Write-Host "`nAll checks passed."
