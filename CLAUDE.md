# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

This is a self-hosted homelab running on the domain `databunker.uk`. Each service lives in its own subdirectory with a `docker-compose.yml` and an optional `.env` file. Services are managed independently — there is no root-level compose file.

## Common Commands

```bash
# Start a service
cd <service-dir> && docker compose up -d

# Stop a service
cd <service-dir> && docker compose down

# View logs
cd <service-dir> && docker compose logs -f

# Restart a specific container
docker compose restart <container-name>

# Pull latest images and redeploy
cd <service-dir> && docker compose pull && docker compose up -d
```

## Architecture

### Traffic Flow

```
Internet → cloudflared (host network) → Caddy (authelia stack, 127.0.0.1:8888)
                                              ↓ routes by hostname
                              [optional Authelia auth check] → backend service
```

- **cloudflared** receives all external HTTPS traffic via a Cloudflare tunnel and forwards to the host.
- **Caddy** (inside the `authelia` stack) is the internal reverse proxy, routing `*.databunker.uk` subdomains by hostname to their respective backends.
- **Authelia** provides SSO/forward-auth. Caddy uses the `(protected)` snippet to gate services behind login.

### Services and Ports

| Service | Internal Port | Subdomain | Auth Protected |
|---|---|---|---|
| authelia | 9091 | auth.databunker.uk | No |
| open-webui | 127.0.0.1:3030→8080 | chat.databunker.uk | Yes |
| grafana | 3002 | dash.databunker.uk | Yes |
| stirling-pdf | 8080 | pdf.databunker.uk | Yes |
| bar-assistant (UI) | 8090 | bar.databunker.uk | Yes |
| bar-assistant (API) | 8091 | bar-api.databunker.uk | No |
| affine | 3010 | affine.databunker.uk | No |
| uptime-kuma | 3001 (host) | status.databunker.uk | No |
| prometheus | 9090 | — | — |
| adguard | 53/80/443/3000/853 | — | — |

### Key Cross-Stack Dependencies

- The `caddy` container in the `authelia` stack joins the `open-webui_default` network so it can proxy to `open-webui:8080` by container name.
- Uptime-Kuma and Grafana run on `host` network or a fixed host port; Caddy reaches them via `host.docker.internal`.
- **autoheal** watches all containers (`AUTOHEAL_CONTAINER_LABEL=all`) and auto-restarts any that become unhealthy.

### Authelia Stack (`authelia/`)

Contains four containers: `authelia`, `authelia-redis`, `authelia-postgres`, and `caddy`. The `Caddyfile` defines all virtual host routing and which routes require forward-auth. User accounts are in `config/users_database.yml`.

### Monitoring Stack

- **Prometheus** scrapes itself and Uptime-Kuma metrics (via bearer token) at 30s intervals.
- **Grafana** connects to Prometheus as a datasource for dashboards.

### Affine (`affine/`)

Requires both `postgres` (pgvector/pg16) and `redis`. Runs a migration job (`affine_migration`) before starting the main server.

### Bar Assistant (`bar-assistant/`)

Three containers: `meilisearch` (search engine), `redis` (cache/session), `bar-assistant` (API server), and `salt-rim` (frontend UI). Configuration via `.env`.
