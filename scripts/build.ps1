# build.ps1 — WiNotification build script
# Author: Hadi Cahyadi <cumulus13@gmail.com>
#
# Requirements:
#   - Go 1.21+
#   - CGO-capable toolchain (MinGW-w64 or MSVC) for ZeroMQ
#   - Optional: goversioninfo for embedding version info in the .exe
#
# Usage:
#   .\build.ps1              # debug build
#   .\build.ps1 -Release     # optimised release build
#   .\build.ps1 -Clean       # remove build artefacts

param(
    [switch]$Release,
    [switch]$Clean
)

$ErrorActionPreference = "Stop"
$ProjectName = "WiNotification"
$OutDir = "dist"
$Exe = "$OutDir\$ProjectName.exe"

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

$BuildFlags = @(
    "-tags", "windows"
    "-v"
)

if ($Release) {
    Write-Host "Building RELEASE $Exe ..." -ForegroundColor Cyan
    $BuildFlags += "-ldflags"
    $BuildFlags += "-s -w -H windowsgui -X main.version=$(git describe --tags --always 2>$null)"
} else {
    Write-Host "Building DEBUG $Exe ..." -ForegroundColor Green
    $BuildFlags += "-ldflags"
    $BuildFlags += "-H windowsgui"
}

$env:CGO_ENABLED = "1"
$env:GOOS = "windows"
$env:GOARCH = "amd64"

& go build @BuildFlags -o $Exe .\cmd\winotification\

if ($LASTEXITCODE -ne 0) {
    Write-Host "Build FAILED" -ForegroundColor Red
    exit $LASTEXITCODE
}

Write-Host "Build OK => $Exe" -ForegroundColor Green
Write-Host ""
Write-Host "First run: grant notification access with:" -ForegroundColor Yellow
Write-Host "  $Exe --request-access" -ForegroundColor White
