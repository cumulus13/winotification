<#
.SYNOPSIS
Build script for WiNotification on Windows

.AUTHOR
Hadi Cahyadi <cumulus13@gmail.com>

.DESCRIPTION
Handles building, cleaning, and dependency management for WiNotification
#>

param(
    [Parameter(Position=0)]
    [ValidateSet('build', 'release', 'clean', 'tidy', 'deps')]
    [string]$Target = 'build'
)

$Binary = "dist\WiNotification.exe"
$Pkg = ".\cmd\winotification"
$Tags = "windows"

# Set environment variables for build
$env:CGO_ENABLED = "1"
$env:GOOS = "windows"
$env:GOARCH = "amd64"

switch ($Target) {
    'build' {
        Write-Host "Building..." -ForegroundColor Cyan
        
        # Create dist directory if it doesn't exist
        if (-not (Test-Path "dist")) {
            New-Item -ItemType Directory -Path "dist" | Out-Null
        }
        
        # Copy config and icons if they exist (silently)
        if (Test-Path "config.toml") {
            Copy-Item "config.toml" "dist\" -ErrorAction SilentlyContinue
        }
        if (Test-Path "icons") {
            Copy-Item -Path "icons\*" -Destination "dist\icons\" -Recurse -ErrorAction SilentlyContinue
        }
        
        # Build
        go build -tags $Tags -o $Binary $Pkg
        
        if ($LASTEXITCODE -eq 0) {
            Write-Host "Build OK => $Binary" -ForegroundColor Green
        } else {
            Write-Host "Build FAILED" -ForegroundColor Red
            exit 1
        }
    }
    
    'release' {
        Write-Host "Release building..." -ForegroundColor Cyan
        $LdFlags = "-H windowsgui -s -w"
        
        # Create dist directory if it doesn't exist
        if (-not (Test-Path "dist")) {
            New-Item -ItemType Directory -Path "dist" | Out-Null
        }
        
        # Copy config and icons if they exist (silently)
        if (Test-Path "config.toml") {
            Copy-Item "config.toml" "dist\" -ErrorAction SilentlyContinue
        }
        if (Test-Path "icons") {
            Copy-Item -Path "icons\*" -Destination "dist\icons\" -Recurse -ErrorAction SilentlyContinue
        }
        
        # Build release
        go build -tags $Tags -ldflags "$LdFlags" -o $Binary $Pkg
        
        if ($LASTEXITCODE -eq 0) {
            Write-Host "Release build OK => $Binary" -ForegroundColor Green
        } else {
            Write-Host "Release build FAILED" -ForegroundColor Red
            exit 1
        }
    }
    
    'clean' {
        Write-Host "Cleaning..." -ForegroundColor Cyan
        if (Test-Path "dist") {
            Remove-Item -Path "dist" -Recurse -Force
            Write-Host "Clean OK" -ForegroundColor Green
        } else {
            Write-Host "Nothing to clean" -ForegroundColor Yellow
        }
    }
    
    'tidy' {
        Write-Host "Tidying modules..." -ForegroundColor Cyan
        go mod tidy
        if ($LASTEXITCODE -eq 0) {
            Write-Host "Tidy OK" -ForegroundColor Green
        }
    }
    
    'deps' {
        Write-Host "Downloading dependencies..." -ForegroundColor Cyan
        go mod download
        if ($LASTEXITCODE -eq 0) {
            Write-Host "Dependencies downloaded OK" -ForegroundColor Green
        }
    }
}