# build.ps1 — WiNotification build script
# Author: Hadi Cahyadi <cumulus13@gmail.com>
#
# Requirements:
#   - Go 1.21+
#   - CGO-capable toolchain (MinGW-w64) — required for go-sqlite3 AND ZeroMQ
#   - Optional: goversioninfo for embedding version info in the .exe
#
# Usage:
#   .\build.ps1              # debug build (main app + test CLI)
#   .\build.ps1 -Release     # optimised release build
#   .\build.ps1 -TestOnly    # build only winotif-test.exe
#   .\build.ps1 -Clean       # remove build artefacts

param(
    [switch]$Release,
    [switch]$Clean,
    [switch]$TestOnly
)

$ErrorActionPreference = "Stop"
$ProjectName = "WiNotification"
$OutDir      = "dist"
$Exe         = "$OutDir\$ProjectName.exe"
$TestExe     = "$OutDir\winotif-test.exe"

if ($Clean) {
    Write-Host "Cleaning $OutDir ..." -ForegroundColor Yellow
    Remove-Item -Recurse -Force $OutDir -ErrorAction SilentlyContinue
    exit 0
}

New-Item -ItemType Directory -Force $OutDir | Out-Null

# Copy config and icons to dist
Copy-Item -Force config.toml $OutDir\ -ErrorAction SilentlyContinue
if (Test-Path icons) {
    Copy-Item -Recurse -Force icons $OutDir\
}

$env:CGO_ENABLED = "1"
$env:GOOS        = "windows"
$env:GOARCH      = "amd64"

# ── winotif-test (console binary — always has a console window) ───────────────
Write-Host "Building winotif-test.exe ..." -ForegroundColor Cyan

$TestFlags = @("-tags", "windows", "-v")
if ($Release) {
    $TestFlags += "-ldflags"; $TestFlags += "-s -w"
}
& go build @TestFlags -o $TestExe .\cmd\winotif-test\
if ($LASTEXITCODE -ne 0) { Write-Host "winotif-test build FAILED" -ForegroundColor Red; exit 1 }
Write-Host "  OK => $TestExe" -ForegroundColor Green

if ($TestOnly) { exit 0 }

# ── WiNotification (GUI binary — no console window in release) ───────────────
Write-Host "Building $Exe ..." -ForegroundColor Cyan

$BuildFlags = @("-tags", "windows", "-v")
if ($Release) {
    $BuildFlags += "-ldflags"
    $BuildFlags += "-s -w -H windowsgui -X main.version=$(git describe --tags --always 2>$null)"
} else {
    $BuildFlags += "-ldflags"; $BuildFlags += "-H windowsgui"
}

& go build @BuildFlags -o $Exe .\cmd\winotification\
if ($LASTEXITCODE -ne 0) { Write-Host "Build FAILED" -ForegroundColor Red; exit 1 }
Write-Host "  OK => $Exe" -ForegroundColor Green

Write-Host ""
Write-Host "Quick-test all enabled backends:" -ForegroundColor Yellow
Write-Host "  $TestExe --config config.toml" -ForegroundColor White
Write-Host ""
Write-Host "First run (grant notification access):" -ForegroundColor Yellow
Write-Host "  $Exe --request-access" -ForegroundColor White
