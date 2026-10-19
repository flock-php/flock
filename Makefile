VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X flock/internal/cli.Version=$(VERSION)

.PHONY: build build-all test lint notices app dmg windows windows-test linux

build:
	go build -ldflags "$(LDFLAGS)" -o bin/flock ./cmd/flock

build-all:
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/flock-darwin-arm64 ./cmd/flock
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/flock-windows-amd64.exe ./cmd/flock
	GOOS=linux   GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/flock-linux-amd64 ./cmd/flock
	GOOS=linux   GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/flock-linux-arm64 ./cmd/flock

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

# flock.exe, flock-helper.exe and flockd.exe for Windows (copy them to the Windows machine and run `flock.exe install`).
windows:
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/windows-amd64/flock.exe ./cmd/flock
	GOOS=windows GOARCH=amd64 go build -ldflags "-s -w -X main.version=$(VERSION)" -o dist/windows-amd64/flock-helper.exe ./cmd/flock-helper
	GOOS=windows GOARCH=amd64 go build -ldflags "-s -w -H windowsgui" -o dist/windows-amd64/flockd.exe ./cmd/flockd

# The Linux CLI as release archives (install.sh and the AUR package download these):
# dist/flock-<version>-linux-{x64,arm64}.tar.gz with flock-php (util-linux owns flock), each with its .sha256.
linux:
	for a in x64:amd64 arm64:arm64; do \
		name=flock-$(VERSION)-linux-$${a%%:*}; \
		rm -rf dist/$$name && mkdir -p dist/$$name && \
		CGO_ENABLED=0 GOOS=linux GOARCH=$${a##*:} go build -trimpath -ldflags "$(LDFLAGS)" -o dist/$$name/flock-php ./cmd/flock && \
		cp LICENSE NOTICE THIRD_PARTY_NOTICES dist/$$name/ && \
		tar -C dist/$$name -czf dist/$$name.tar.gz flock-php LICENSE NOTICE THIRD_PARTY_NOTICES && \
		rm -r dist/$$name && \
		(cd dist && sha256sum $$name.tar.gz > $$name.tar.gz.sha256) || exit 1; \
	done

# The tray app's models and API client (windows/FlockKit) run on any OS; the app itself is built on Windows (windows/build.ps1).
windows-test:
	dotnet test windows/FlockKit.Tests
