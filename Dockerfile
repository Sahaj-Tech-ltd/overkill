FROM golang:1.26-bookworm AS go-builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /overkill ./cmd/overkill

FROM node:22-bookworm-slim AS tui-builder
WORKDIR /build/tui
COPY tui/package.json tui/package-lock.json ./
RUN npm ci
COPY tui/tsconfig.json ./
COPY tui/src ./src
COPY tui/tests ./tests
RUN npm run typecheck && npm run build && npm run test:build && npm prune --omit=dev

FROM python:3.12-slim-bookworm AS python-builder
WORKDIR /build
COPY bridge/ ./bridge/
RUN pip wheel --no-cache-dir --wheel-dir=/wheels ./bridge

FROM python:3.12-slim-bookworm
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates git libstdc++6 \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 overkill \
    && useradd --uid 10001 --gid 10001 --create-home overkill
COPY --from=tui-builder /usr/local/bin/node /usr/local/bin/node
COPY --from=go-builder /overkill /usr/local/bin/overkill
COPY --from=tui-builder /build/tui/dist /usr/local/share/overkill/tui/dist
COPY --from=tui-builder /build/tui/node_modules /usr/local/share/overkill/tui/node_modules
COPY --from=tui-builder /build/tui/package.json /usr/local/share/overkill/tui/package.json
COPY scripts/verify-packaged-tui.py /usr/local/share/overkill/verify-packaged-tui.py
COPY --from=python-builder /wheels /wheels
RUN pip install --no-cache-dir --no-index --find-links=/wheels bridge && rm -rf /wheels \
    && mkdir -p /workspace /home/overkill/.overkill \
    && chown -R overkill:overkill /workspace /home/overkill/.overkill
ENV HOME=/home/overkill OVERKILL_HOME=/home/overkill/.overkill
USER overkill
WORKDIR /workspace
ENTRYPOINT ["overkill"]
