# Install

## One-liner (Linux, macOS, WSL)

```sh
curl -fsSL https://raw.githubusercontent.com/Sahaj-Tech-ltd/overkill/main/install.sh | sh
```

The installer detects your platform, prefers `go install` when Go is on `PATH`, otherwise downloads a pre-built binary, drops it in `~/.local/bin/overkill`, and bootstraps `~/.overkill/`.

## Windows (PowerShell)

```powershell
irm https://raw.githubusercontent.com/Sahaj-Tech-ltd/overkill/main/install.ps1 | iex
```

Installs to `%LOCALAPPDATA%\overkill\bin\overkill.exe` and adds it to your user `PATH`.

## With Go

Go 1.26 or newer is required to build from source. The terminal UI requires Node.js 20 or newer.

```sh
go install github.com/Sahaj-Tech-ltd/overkill/cmd/overkill@latest
```

## From source

```sh
git clone https://github.com/Sahaj-Tech-ltd/overkill.git
cd overkill
make install-all   # builds and copies to ~/go/bin/overkill
```

## Runtime dependencies

Runtime requirements depend on the entry point:

- A configured model provider or reachable local model endpoint is needed for agent turns.
- The terminal frontend requires Node.js 20+ and its built `tui/dist` output and dependencies; the CLI executable alone does not contain that frontend.
- PostgreSQL is required for the API-backed interactive path and durable sessions, memory and coordination. Set `database_url` or `DATABASE_URL`; a standalone CLI run without a database does not gain those persistence features.
- `git` is required for Git tools and repository operations. Optional tools and servers include:

- `gopls`, `typescript-language-server`, `pyright`, `rust-analyzer` — LSP tools
- Any MCP server you wire up
- `gh` — GitHub Gist sharing

The development desktop bundle supplies its own Electron/Node runtime, backend
and private PostgreSQL binaries. Its acceptance and signing scope is documented
in the [desktop guide](../../desktop/README.md). Messaging adapters may require
platform credentials and external services; see the [gateway matrix](../gateways.md).
The optional Python semantic bridge additionally requires Python 3.11+, its
installed dependencies, models, and pgvector for persistent vector memory; see
the [bridge guide](../../bridge/README.md). The [storage map](architecture.md#storage)
distinguishes database records, local files and disposable process state.

## Supported platforms

| Platform | Arch | Installer |
|---|---|---|
| Linux | x86_64, arm64 | `install.sh` |
| macOS | Intel, Apple Silicon | `install.sh` |
| Windows | x86_64 | `install.ps1` |
| WSL2 | x86_64, arm64 | `install.sh` |

## Terminal UI build

From a source checkout, install Node.js 20 or newer, then run:

```sh
cd tui
npm ci
npm run typecheck
npm run build
npm run test:build
npm start
```

`build` emits JavaScript into `tui/dist`; `start` runs that output directly
with Node. Use `npm run dev` when editing TypeScript sources. The build smoke
check loads every emitted module without a TypeScript runtime loader; backend
connection and provider configuration are separate runtime requirements.

## Local verification hooks

Install `gitleaks`, Go 1.26+, Node.js 20+, and bridge development dependencies
(`pip install -e './bridge[dev]'`). Run `npm ci --prefix tui` before using the
hooks. `scripts/pre-commit.sh` checks staged languages;
`scripts/pre-push.sh` checks Go tests, race detection, builds, terminal UI output,
and dependency advisories. Missing required tools fail with their diagnostics.
`FULL=true scripts/pre-push.sh` also requires `golangci-lint` and `govulncheck`.

## Complete local Docker setup

Docker Engine with the Compose plugin is required. Set a persistent random
URL-safe database password in your environment (for example, generate one with
`openssl rand -hex 24` and store it in your password manager). Keep the same
password when reusing the PostgreSQL volume:

```sh
export OVERKILL_POSTGRES_PASSWORD='<your URL-safe password>'
docker compose up -d --wait postgres
docker compose run --rm overkill config init
docker compose run --rm overkill
```

The image bundles the Overkill executable, Node.js, the compiled terminal UI and its production
dependencies, and the installed Python bridge core. It runs as a regular user.
Semantic Python models require a separate `[ml]` installation; see the bridge
README. The database includes pgvector and has a readiness check. Database and
application configuration live in the `postgres-data` and `overkill-data`
volumes. PostgreSQL binds only to host loopback. Set `OVERKILL_POSTGRES_PORT` if
5432 is already in use. Re-running `config init` preserves an existing file by
returning an error; skip that command after initial setup.

For the native binary with this local database, use:

```sh
export DATABASE_URL="postgres://overkill:${OVERKILL_POSTGRES_PASSWORD}@127.0.0.1:${OVERKILL_POSTGRES_PORT:-5432}/overkill?sslmode=disable"
```

`docker compose down` preserves data. Use `down --volumes` only when you intend
to delete this stack's stored data. `scripts/verify-docker.sh` builds and checks
an isolated disposable stack without changing your regular Compose project.

## Distribution scope

The current downloaded release assets contain the CLI executable. They do not
include the terminal frontend, Node runtime, or a macOS application bundle.
Use the source/frontend instructions above or Docker for interactive mode until
complete native runtime bundles and the DMG have passed their release gates.
The installers require the release's exact `checksums.txt` entry and preserve
an installed executable if verification fails. Generated configuration uses
`overkill config init`, which shares the application's current schema and does
not resolve provider API keys into installation command arguments or logs.
