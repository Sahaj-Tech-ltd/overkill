.PHONY: build test lint clean install docker run dev install-all plugins

BINARY=bin/ethos
GO=go
GOFLAGS=-trimpath -ldflags="-s -w"

build:
	$(GO) build $(GOFLAGS) -o $(BINARY) ./cmd/ethos

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
	go install ./cmd/ethos

install-global: build
	sudo cp $(BINARY) /usr/local/bin/ethos

docker:
	docker build -t ethos:latest .

run: build
	./$(BINARY)

dev:
	$(GO) run ./cmd/ethos

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
	$(GO) build $(GOFLAGS) -o $(HOME)/go/bin/ethos ./cmd/ethos
	@echo "ethos installed to $(HOME)/go/bin/ethos"

# Build the bundled example plugins into examples/plugins/<name>/<name>.
plugins:
	$(GO) build -o examples/plugins/notes/notes ./examples/plugins/notes
	$(GO) build -o examples/plugins/git-stats/git-stats ./examples/plugins/git-stats
	@echo "plugins built — copy or symlink directories into ~/.ethos/plugins/ to install"
