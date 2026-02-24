BINARY    := da-proxy
PKG       := github.com/SigmaUno/da-proxy
BUILD_DIR := bin
GOFLAGS   := -trimpath
VERSION   := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -s -w -X main.version=$(VERSION)

.PHONY: build test lint run clean docker-build fmt vet tidy install-tools coverage

build:
	CGO_ENABLED=1 go build $(GOFLAGS) -ldflags '$(LDFLAGS)' -o $(BUILD_DIR)/$(BINARY) ./cmd/da-proxy

test:
	CGO_ENABLED=1 go test -race -count=1 -coverprofile=coverage.out ./...

lint:
	golangci-lint run ./...

fmt:
	gofmt -s -w .

vet:
	go vet ./...

run:
	go run ./cmd/da-proxy -config configs/config.example.yaml

clean:
	rm -rf $(BUILD_DIR) coverage.out coverage.html

docker-build:
	docker build -t da-proxy:latest .

install-tools:
	go install gotest.tools/gotestsum@latest

tidy:
	go mod tidy

coverage: test
	go tool cover -html=coverage.out -o coverage.html
