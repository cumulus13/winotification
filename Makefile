# Makefile for WiNotification
# Author: Hadi Cahyadi <cumulus13@gmail.com>
# Note: this is for cross-compile or MinGW shells on Windows.

BINARY   = dist/WiNotification.exe
PKG      = ./cmd/winotification
LDFLAGS  = -H windowsgui -s -w
TAGS     = windows

.PHONY: build release clean tidy deps

build:
	@mkdir -p dist
	@cp -n config.toml dist/ 2>/dev/null || true
	@cp -rn icons dist/ 2>/dev/null || true
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
		go build -tags $(TAGS) -o $(BINARY) $(PKG)
	@echo "Build OK => $(BINARY)"

release:
	@mkdir -p dist
	@cp -n config.toml dist/ 2>/dev/null || true
	@cp -rn icons dist/ 2>/dev/null || true
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
		go build -tags $(TAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)
	@echo "Release build OK => $(BINARY)"

clean:
	rm -rf dist/

tidy:
	go mod tidy

deps:
	go mod download
