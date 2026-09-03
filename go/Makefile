.PHONY: build test lint install clean

BIN_EXT := $(if $(filter windows,$(shell go env GOOS)),.exe,)

build:
	go build -o bin/power-monitor-pp-cli$(BIN_EXT) ./cmd/power-monitor-pp-cli

test:
	go test ./...

lint:
	golangci-lint run

install:
	go install ./cmd/power-monitor-pp-cli

clean:
	rm -rf bin/

build-mcp:
	go build -o bin/power-monitor-pp-mcp$(BIN_EXT) ./cmd/power-monitor-pp-mcp

install-mcp:
	go install ./cmd/power-monitor-pp-mcp

build-all: build build-mcp
