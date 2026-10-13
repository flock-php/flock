VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X flock/internal/cli.Version=$(VERSION)

.PHONY: build build-all test lint

build:
	go build -ldflags "$(LDFLAGS)" -o bin/flock ./cmd/flock

build-all:
	GOOS=darwin  GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/flock-darwin-arm64 ./cmd/flock
	GOOS=darwin  GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/flock-darwin-amd64 ./cmd/flock
	GOOS=windows GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/flock-windows-amd64.exe ./cmd/flock
	GOOS=windows GOARCH=arm64 go build -ldflags "$(LDFLAGS)" -o dist/flock-windows-arm64.exe ./cmd/flock

test:
	go test -race ./...

lint:
	go vet ./...
	test -z "$$(gofmt -l .)"
