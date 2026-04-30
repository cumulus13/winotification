# Makefile for WiNotification
# Author: Hadi Cahyadi <cumulus13@gmail.com>
# Note: this is for cross-compile or MinGW shells on Windows.

BINARY      = dist/WiNotification.exe
TEST_BINARY = dist/winotif-test.exe
PKG         = ./cmd/winotification
TEST_PKG    = ./cmd/winotif-test
LDFLAGS     = -H windowsgui -s -w
TAGS        = windows

.PHONY: build release test-cli clean tidy deps

build: _dist
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
		go build -tags $(TAGS) -o $(BINARY) $(PKG)
	@echo "Build OK => $(BINARY)"

release: _dist
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
		go build -tags $(TAGS) -ldflags "$(LDFLAGS)" -o $(BINARY) $(PKG)
	@echo "Release build OK => $(BINARY)"

# winotif-test: console binary — no -H windowsgui, works in any terminal
test-cli: _dist
	CGO_ENABLED=1 GOOS=windows GOARCH=amd64 \
		go build -tags $(TAGS) -o $(TEST_BINARY) $(TEST_PKG)
	@echo "Test CLI OK => $(TEST_BINARY)"

# Build both in one shot
all: build test-cli

_dist:
	@mkdir -p dist
	@cp -n config.toml dist/ 2>/dev/null || true
	@cp -rn icons dist/ 2>/dev/null || true

clean:
	rm -rf dist/

tidy:
	go mod tidy

deps:
	go mod download

