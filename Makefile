VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X flock/internal/cli.Version=$(VERSION)

.PHONY: build build-all test lint notices app dmg windows windows-test

build:
	go build -ldflags "$(LDFLAGS)" -o bin/flock ./cmd/flock

build-all:
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/flock-darwin-arm64 ./cmd/flock
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/flock-windows-amd64.exe ./cmd/flock

test:
	go test -race ./...

lint:
	go vet ./...
	test -z "$$(gofmt -l .)"

# THIRD_PARTY_NOTICES, after changing dependencies (CI fails while it's out of date).
notices:
	go run ./tools/notices

# build/macos/Flock.app (with the flock CLI inside) and build/macos/Flock.dmg.
app:
	VERSION=$(VERSION) macos/build-app.sh

dmg: app

# flock.exe and flock-helper.exe for Windows (copy both to the Windows machine and run `flock.exe install`).
windows:
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/windows-amd64/flock.exe ./cmd/flock
	GOOS=windows GOARCH=amd64 go build -ldflags "-s -w -X main.version=$(VERSION)" -o dist/windows-amd64/flock-helper.exe ./cmd/flock-helper

# The tray app's models and API client (windows/FlockKit) run on any OS; the app itself is built on Windows (windows/build.ps1).
windows-test:
	dotnet test windows/FlockKit.Tests
