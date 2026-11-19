$ErrorActionPreference = "Continue"
$kit = Split-Path -Parent $MyInvocation.MyCommand.Path
$results = Join-Path $kit "results.txt"

$lines = @()
$lines += "caffe windows test kit - $(Get-Date -Format o)"
$lines += "windows: $([System.Environment]::OSVersion.VersionString)  arch: $env:PROCESSOR_ARCHITECTURE"
$git = Get-Command git -ErrorAction SilentlyContinue
if ($git) { $lines += "git: $(& git --version)" } else { $lines += "git: NOT FOUND (install Git for Windows; Git-based tests will fail)" }
$lines += ""

$failed = 0
foreach ($dir in Get-ChildItem (Join-Path $kit "tests") -Directory | Sort-Object Name) {
    $exe = Join-Path $dir.FullName "$($dir.Name).test.exe"
    if (-not (Test-Path $exe)) { continue }
    Write-Host "== $($dir.Name)"
    Push-Location $dir.FullName
    $out = & $exe "-test.count=1" "-test.timeout=10m" 2>&1 | Out-String
    $code = $LASTEXITCODE
    Pop-Location
    Set-Content -Path (Join-Path $dir.FullName "output.txt") -Value $out
    if ($code -eq 0) {
        $lines += "PASS  $($dir.Name)"
    } else {
        $failed++
        $lines += "FAIL  $($dir.Name)  (exit $code)"
        $lines += ($out -split "`n" | Where-Object { $_ -match '^(--- FAIL|panic:)|_test\.go:\d+' } | ForEach-Object { "      $($_.TrimEnd())" })
    }
}
$lines += ""
$lines += "packages failed: $failed"
Set-Content -Path $results -Value $lines
Write-Host ""
Get-Content $results
Write-Host ""
Write-Host "Results written to $results - send this file back."
