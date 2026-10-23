# Releasing Overkill

Latest local candidate — 2026-10-22: EptytH macOS arm64 development DMG. SHA-256 `db066b34fd9a23537142e83561bebd38e17c3d7944f38ebeec6fd854e37792cb`. Includes scoped ACP runtime provisioning/ancestry, coordination contract inspection, draft-safe chat admission, configured readonly, bounded image tools, workspace mentions and terminal committed-state input. Final gates:107 Go race packages, vet, Windows source compilation,87 desktop tests,47 terminal tests and actual Linux/arm64 Docker build/runtime. Installed fake-storage smoke passes file edit/cancel/peer exchange/same-version replacement/relaunch/database recovery. Root verifies all28 desktop sources, signed backend, image hash and byte-identical independent backend rebuild from current source (`.cache/transport-mentions-artifact-root-checks.json`). Receipt: `.cache/distribution/overkill-0.1.0-dev-arm64-EptytH/installed-mock-storage-smoke.json`. Real installed Keychain, minimum OS and cross-version release acceptance remain pending. Ad-hoc signing, no Developer ID/notarization, distributionReady=false. Provisional K3FCSM predates final bridge/mention/input repairs and is superseded; prior candidates retain historical scope.

Release preparation currently produces five CLI executable assets:
`overkill-linux-amd64`, `overkill-linux-arm64`, `overkill-darwin-amd64`,
`overkill-darwin-arm64`, and `overkill-windows-amd64.exe`. The manifest is
`checksums.txt`, with one SHA256 hash and exact basename per asset. Both
installers require successful verification before replacing an executable.
Native terminal runtime bundles and the macOS DMG remain separate release
gates; a CLI binary alone is not the complete desktop app.

Before tagging, require successful Go tests and race detection with PostgreSQL,
frontend build and authenticated integration, Python wheel/pgvector tests,
platform tests, installer fixtures, and Docker startup verification. Update
user-facing release documentation for the version's actual behavior.

Use semantic versions such as `v1.2.3` or `v1.2.3-beta.1`:

```sh
scripts/release.sh v1.2.3 --dry-run
```

Dry-run prints the plan and performs no filesystem, Git, or build mutations,
even when release artifacts already exist. After required gates pass and the
working tree is clean:

```sh
scripts/release.sh v1.2.3
```

This builds all targets into a temporary directory, generates portable SHA256
checksums, stages them under `dist/v1.2.3/`, and creates a local annotated tag
only after builds succeed. It refuses to replace an existing version directory.
Nothing is pushed or published by the script.

Push the reviewed tag explicitly when ready. The release workflow builds the
five targets, combines their checksums, checks out the tagged repository for
publication, verifies the staged manifest, and uses an explicit repository plus
GitHub-generated release notes. Versions containing `-` are marked prerelease.
No missing changelog file is required. Docker publication is a separate tagged
workflow with multi-platform emulation enabled.

Smoke-test the published installer from a fresh temporary home and validate
`overkill --version`, `overkill --help`, and the generated configuration. Do not
present a CLI release as a stable DMG until the complete native runtime,
application lifecycle, signing/notarization policy, and installation tests pass.
