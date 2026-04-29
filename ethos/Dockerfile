FROM golang:1.23-alpine AS go-builder

RUN apk add --no-cache git ca-certificates

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /ethos ./cmd/ethos

FROM python:3.12-slim AS python-builder

WORKDIR /build

COPY bridge/ bridge/
RUN pip install --no-cache-dir --prefix=/install -e "./bridge[dev]"

FROM python:3.12-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    git \
    && rm -rf /var/lib/apt/lists/*

COPY --from=go-builder /ethos /usr/local/bin/ethos
COPY --from=python-builder /install /usr/local

RUN chmod +x /usr/local/bin/ethos

WORKDIR /workspace

ENTRYPOINT ["ethos"]
