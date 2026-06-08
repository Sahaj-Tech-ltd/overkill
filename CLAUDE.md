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
| affine | 3010 | affine.databunker.uk | No (has own auth) |
| stirling-pdf | 8080 | pdf.databunker.uk | Yes |
| syncthing | 8384 | sync.databunker.uk | Yes |
| shadow | 3004 | shadow.databunker.uk | No |
| attentive | 3003 | attentive.databunker.uk | No |
| uptime-kuma | 3001 (host) | status.databunker.uk | No |
| ladder | 8184 | ladder.databunker.uk | No |
| crw | 3005 | crw.databunker.uk (future) | No |
| adguard | 53/80/443/3000/853 | — | — |

### Key Cross-Stack Dependencies

- Uptime-Kuma runs on host network; Caddy reaches it via `host.docker.internal:3001`.
- Most other services Caddy proxies via `host.docker.internal:<port>` — these ports are blocked from external access by iptables DOCKER-USER rules (see Security section below).
- **autoheal** watches all containers (`AUTOHEAL_CONTAINER_LABEL=all`) and auto-restarts any that become unhealthy.

### Authelia Stack (`authelia/`)

Contains four containers: `authelia`, `authelia-redis`, `authelia-postgres`, and `caddy`. The `Caddyfile` defines all virtual host routing and which routes require forward-auth. User accounts are in `config/users_database.yml`.

### Affine (`affine/`)

Requires both `postgres` (pgvector/pg16) and `redis`. Runs a migration job (`affine_migration`) before starting the main server. Data lives at `/root/.affine/self-host/` (use absolute path — `~` expands differently under sudo). Daily backup cron at 3 AM to `/opt/backups/affine/`.

## Security

### Docker bypasses UFW — critical to understand

Docker injects iptables rules that bypass UFW entirely. Any container port bound to `0.0.0.0` is publicly reachable even if UFW has no ALLOW rule for it. **Do not assume UFW protects Docker-exposed ports.**

To block external access to a port exposed by Docker, rules must be added to the `DOCKER-USER` iptables chain. A systemd service (`docker-block-external.service`) applies these rules on every boot after Docker starts:

```bash
# View/edit blocked ports
sudo cat /usr/local/bin/docker-block-external.sh

# Re-apply rules after changes
sudo systemctl restart docker-block-external.service

# Check rules are active
sudo iptables -L DOCKER-USER -n -v
```

Ports currently blocked from external access: `3000 3001 3002 3010 8080 8081 8090 8091 8092 8182 8184 8687 9090 9100 2586 8095`

### `.env` files contain secrets — never commit them

All `.env` files are gitignored. When adding a new service, copy from `.env.example` and fill in values manually. The git repo tracks compose files only.

### SSH

Password authentication is disabled. Pubkey only. Root login disabled.

### Ladder (`ladder/`)

Paywall bypass proxy (alternative to defunct 12ft.io). Go binary, Docker, port 8184 → 8080. No auth. Uses GoogleBot UA spoofing with domain-based rulesets from everywall/ladder-rules. Caddy routes `ladder.databunker.uk` directly (no Authelia). External access blocked via nftables — only reachable through Caddy.

### crw (`crw/`)

Fast, self-hosted Firecrawl-compatible web scraper. Rust binary, Docker, port 3005 → 3000. No auth, internal-only (127.0.0.1). REST API at `/v1/scrape`, `/v1/crawl`, `/v1/map`, `/v1/search`. ~50MB RAM idle. Hermes MCP integration via `crw-mcp` binary at `~/.local/bin/crw-mcp` (embedded mode).
