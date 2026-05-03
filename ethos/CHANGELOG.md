# Changelog

All notable changes to Ethos are tracked in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added
- GitHub Actions CI matrix (Linux/macOS, amd64/arm64) covering build, vet, gofmt, and race tests.
- `release.yml` workflow: cross-compiled binaries, plugin builds, SHA256SUMS, and auto-changelog on `v*` tags.
- `gosec` and `govulncheck` security workflows with `.gosec-ignore` / `.govulncheck-ignore` allow-lists.
- Standalone CodeQL workflow (`codeql.yml`) for Go and Python.
- `.golangci.yml` with a curated linter set.
- Build-time version injection via `-ldflags="-X main.Version=..."` in the Makefile.

### Changed
- `cmd/ethos.Version` is now a `var` (was `const`) so it can be overridden at link time.
- Dependabot moved to weekly cadence with minor/patch grouping per ecosystem.

### Removed
- Tracked `ethos` binary at the repo root (now `.gitignore`d).
