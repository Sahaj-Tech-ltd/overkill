# Releasing Overkill

This runbook covers cutting and publishing a new Overkill release. The
`release.yml` GitHub workflow does the heavy lifting — your job is to
prepare the repo and push a tag.

## Versioning

Overkill follows [Semantic Versioning](https://semver.org/):

- `vMAJOR` — breaking changes
- `vMINOR` — backward-compatible features
- `vPATCH` — backward-compatible fixes

Pre-releases use suffixes like `v0.10.0-beta.1`. The release workflow marks
any tag containing a `-` as a pre-release on GitHub.

## Pre-release checklist

1. CI is green on `main`.
2. `go test -race ./...` clean locally.
3. `golangci-lint run` clean locally.
4. `CHANGELOG.md` has an entry describing the release.
5. The README reflects any user-visible behavior changes.

## Cut the release

The version string is injected at build time, so you do **not** need to
edit `cmd/overkill/root.go` for each release. The `Version` `var` defaults
to `0.1.0-dev` for local development; the release workflow links in the
tag name via `-ldflags="-X main.Version=${TAG}"`.

```bash
# 1. Update CHANGELOG.md: rename [Unreleased] to [vX.Y.Z] - YYYY-MM-DD,
#    create a fresh empty [Unreleased] section above it, commit.
git add CHANGELOG.md
git commit -m "chore(release): vX.Y.Z"

# 2. Tag and push.
git tag -a vX.Y.Z -m "overkill vX.Y.Z"
git push origin main
git push origin vX.Y.Z
```

The push of `vX.Y.Z` triggers `release.yml`, which:

1. Cross-compiles `overkill` for `linux/amd64`, `linux/arm64`,
   `darwin/amd64`, `darwin/arm64` with `-trimpath` + version ldflags.
2. Builds the example plugins (`notes`, `git-stats`) for every target.
3. Stages `install.sh` and a `examples/plugins/` source tarball.
4. Generates a combined `SHA256SUMS` file.
5. Generates a changelog from `git log <previous-tag>..<this-tag>`.
6. Creates a GitHub Release with everything attached.

## Smoke test the release

After the workflow finishes:

```bash
# Fresh shell so PATH/env aren't polluted.
curl -fsSL https://raw.githubusercontent.com/Sahaj-Tech-ltd/overkill/vX.Y.Z/install.sh | sh
overkill --version    # → "overkill vX.Y.Z"
overkill --help
```

Verify against `SHA256SUMS`:

```bash
curl -fsSL -O https://github.com/Sahaj-Tech-ltd/overkill/releases/download/vX.Y.Z/SHA256SUMS
curl -fsSL -O https://github.com/Sahaj-Tech-ltd/overkill/releases/download/vX.Y.Z/overkill-vX.Y.Z-linux-amd64
sha256sum --check --ignore-missing SHA256SUMS
```

## Hotfix flow

For an urgent fix on a released line:

```bash
git checkout -b release/vX.Y vX.Y.0
# cherry-pick or commit the fix
git tag -a vX.Y.1 -m "overkill vX.Y.1"
git push origin release/vX.Y vX.Y.1
```

## Rollback

If a release is broken:

1. Mark the GitHub release as a pre-release (or delete it) so the
   "latest" pointer no longer resolves to it.
2. The previous tag automatically becomes "latest" via
   `releases/latest/download/...`.
3. File a follow-up issue, fix, and cut the next patch version.
