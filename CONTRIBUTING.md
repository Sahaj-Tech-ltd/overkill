# Contributing to Overkill

Thanks for your interest in Overkill! This guide covers everything you need to contribute effectively.

## Dev Setup

```bash
git clone https://github.com/Sahaj-Tech-ltd/overkill.git
cd overkill
make build      # → bin/overkill with version embedded via -ldflags
make test       # → go test ./...
make lint       # → golangci-lint + ruff
make install-all  # → installs to $HOME/go/bin/overkill
```

The `Version` constant in `cmd/overkill/root.go` is overridden at link time
via `-ldflags="-X main.Version=$(VERSION)"`. The `Makefile` pulls
`VERSION` from `git describe` by default; release builds inject the tag
name. See [docs/RELEASING.md](docs/RELEASING.md) for the full release
runbook.

## Quick Start

```bash
go build ./...
go test -race ./...
golangci-lint run
ruff check bridge/
```

If all four pass, you're ready to contribute.

## Contribution Priorities

We prioritize contributions in this order:

1. **Bug fixes** — especially security, data loss, or crash bugs
2. **Security improvements** — injection prevention, sandboxing, audit
3. **Performance** — token usage, latency, memory
4. **New skills** — follow the SKILL.md spec
5. **New tools** — follow the tool interface in `internal/tools/`
6. **Documentation** — clarity, accuracy, examples

## Branch Naming

| Type | Prefix | Example |
|------|--------|---------|
| Bug fix | `fix/` | `fix/compaction-race-condition` |
| Feature | `feat/` | `feat/telegram-channel` |
| Docs | `docs/` | `docs/api-reference` |
| Test | `test/` | `test/compaction-integration` |
| Refactor | `refactor/` | `refactor/session-manager` |
| Security | `security/` | `security/prompt-injection-guard` |

## Commit Style

We use [Conventional Commits](https://www.conventionalcommits.org/):

```
feat(routing): add complexity-based model classifier
fix(compaction): resolve race condition in DAG summary
docs(readme): add comparison table
security(tools): add path traversal blocking
```

## Pull Request Process

1. **Open as draft first**, mark *Ready for review* once CI is green.
2. **One concern per PR** — mixing refactors with features makes review harder.
3. **Max 5 open PRs per author** — helps us review faster.
4. **Fill out the PR template** — especially validation evidence and security impact.
5. **No refactor-only PRs** unless explicitly requested by maintainers.
6. **AI-assisted PRs must disclose** and include a `Co-Authored-By` trailer.
7. **Security-sensitive paths** (`internal/security/`, `internal/auth/`,
   `.github/workflows/**`, `SECURITY.md`) require approval from a
   `CODEOWNERS` reviewer for the relevant team. See
   [.github/CODEOWNERS](.github/CODEOWNERS).

## Reporting Security Issues

Please **do not** open a public issue for security vulnerabilities.
Follow the disclosure process in [SECURITY.md](SECURITY.md).

### Pre-PR Checklist

- [ ] `go test ./...` passes
- [ ] `golangci-lint run` passes
- [ ] `ruff check bridge/` passes
- [ ] `go build ./...` succeeds
- [ ] No hardcoded secrets or credentials
- [ ] New code has tests

## Skill vs Tool Decision

Not sure whether to build a skill or a tool?

| Aspect | Tool | Skill |
|--------|-------|-------|
| Lives in | `internal/tools/` | `skills/` or `optional-skills/` |
| Language | Go | Markdown (SKILL.md) |
| Purpose | System-level operation (shell, fs, git) | Agent behavior pattern (code review, testing) |
| Speed | Native | Interpreted by agent |
| When | Need to DO something | Need to GUIDE behavior |

## Code Style

### Go
- Follow `gofmt` and `golangci-lint` defaults
- Error handling: wrap with context (`fmt.Errorf("reading config: %w", err)`)
- No panic in library code
- Interfaces defined where they're consumed, not where they're implemented

### Python (Bridge)
- `ruff` for formatting and linting
- Type hints on all public functions
- `pyproject.toml` for dependency management

## Getting Help

- [Discussions](https://github.com/Sahaj-Tech-ltd/overkill/discussions)
- [Issues](https://github.com/Sahaj-Tech-ltd/overkill/issues)

## License

By contributing, you agree that your contributions will be licensed under both MIT and Apache-2.0.
