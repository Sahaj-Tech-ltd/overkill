.PHONY: build test lint clean install docker run dev

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

install: build
	cp $(BINARY) /usr/local/bin/ethos

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
