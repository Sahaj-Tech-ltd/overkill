.PHONY: build test lint clean install docker run dev install-all plugins release

BINARY=bin/overkill
GO=go
# VERSION may be overridden: `make build VERSION=v1.2.3`. Defaults to git
# describe so local builds carry a meaningful identifier.
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
GOFLAGS=-trimpath -ldflags="-s -w -X main.Version=$(VERSION)"

build:
	$(GO) build $(GOFLAGS) -o $(BINARY) ./cmd/overkill

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

test-cover:
	$(GO) test -cover ./internal/...

lint:
	golangci-lint run
	ruff check bridge/

lint-fix:
	golangci-lint run --fix
	ruff format bridge/

clean:
	rm -rf bin/ coverage.out

install:
	go install ./cmd/overkill

install-global: build
	sudo cp $(BINARY) /usr/local/bin/overkill

docker:
	docker build -t overkill:latest .

run: build
	./$(BINARY)

dev:
	$(GO) run ./cmd/overkill

bridge-install:
	cd bridge && pip install -e ".[dev]"

bridge-test:
	cd bridge && pytest

bridge-lint:
	cd bridge && ruff check .

proto:
	protoc --go_out=. --go_opt=paths=source_relative \
		--grpc-gateway_out=. --grpc-gateway_opt=paths=source_relative \
		bridge/proto/*.proto

all: lint test build

install-all:
	$(GO) build $(GOFLAGS) -o $(HOME)/go/bin/overkill ./cmd/overkill
	@echo "overkill installed to $(HOME)/go/bin/overkill"

# Build the bundled example plugins into examples/plugins/<name>/<name>.
plugins:
	$(GO) build -o examples/plugins/notes/notes ./examples/plugins/notes
	$(GO) build -o examples/plugins/git-stats/git-stats ./examples/plugins/git-stats
	@echo "plugins built — copy or symlink directories into ~/.overkill/plugins/ to install"

# Cross-compile release binaries for all platforms.
release:
	@bash scripts/release.sh $(VERSION)
