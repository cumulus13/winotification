@echo off
REM Batch file for WiNotification
REM Author: Hadi Cahyadi <cumulus13@gmail.com>
REM Note: This is for Windows command prompt or PowerShell.

SET BINARY=dist\WiNotification.exe
SET PKG=.\cmd\winotification
SET LDFLAGS=-H windowsgui -s -w
SET TAGS=windows

REM Define targets
IF "%1"=="" GOTO :usage
IF "%1"=="build" GOTO :build
IF "%1"=="release" GOTO :release
IF "%1"=="clean" GOTO :clean
IF "%1"=="tidy" GOTO :tidy
IF "%1"=="deps" GOTO :deps
GOTO :usage

:build
    @echo Building...
    if not exist dist mkdir dist
    if exist config.toml copy config.toml dist\ >nul 2>&1
    if exist icons xcopy icons dist\icons /E /I /Y >nul 2>&1
    set CGO_ENABLED=1
    set GOOS=windows
    set GOARCH=amd64
    go build -tags %TAGS% -o %BINARY% %PKG%
    if %errorlevel% equ 0 (
        echo Build OK => %BINARY%
    ) else (
        echo Build FAILED
    )
    goto :eof

:release
    @echo Release building...
    if not exist dist mkdir dist
    if exist config.toml copy config.toml dist\ >nul 2>&1
    if exist icons xcopy icons dist\icons /E /I /Y >nul 2>&1
    set CGO_ENABLED=1
    set GOOS=windows
    set GOARCH=amd64
    go build -tags %TAGS% -ldflags "%LDFLAGS%" -o %BINARY% %PKG%
    if %errorlevel% equ 0 (
        echo Release build OK => %BINARY%
    ) else (
        echo Release build FAILED
    )
    goto :eof

:clean
    @echo Cleaning...
    if exist dist rmdir /s /q dist
    echo Clean OK
    goto :eof

:tidy
    @echo Tidying modules...
    go mod tidy
    goto :eof

:deps
    @echo Downloading dependencies...
    go mod download
    goto :eof

:usage
    echo Usage: %0 [build^|release^|clean^|tidy^|deps]
    echo.
    echo   build   - Build with debug symbols
    echo   release - Build release version (stripped, windows GUI)
    echo   clean   - Remove dist directory
    echo   tidy    - Run go mod tidy
    echo   deps    - Download dependencies
    goto :eof