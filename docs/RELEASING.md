# Releasing Overkill

Current preview: **v0.11.0-beta.1**. This release adds the desktop workspace, agent coordination, automatic checkpoints, durable resume, DAG memory, reviewed project skill evolution and isolated per-item context. The upstream dependency updates and Blacksmith workflow migration are preserved.

Use GitHub Releases for desktop installers, CLI executables, checksum manifests and acceptance evidence. GHCR packages contain container images. Preview tags never move the container `latest` tag.

The macOS Apple Silicon DMG is ad-hoc signed and not notarized. The Windows x64 desktop setup EXE is unsigned. Publish their actual installed-app acceptance and platform limitations alongside each asset; Windows cross-compilation alone is not an installed desktop test. The dependency inventory retains unresolved vendor source/replacement obligations and `distributionReady: false`; a preview is not a stable distribution certification. Installer source and dependency notices ship in the corresponding packages.

The Windows desktop workflow is dispatched against a full expected commit SHA. It builds the native installer on Windows, installs in a path with spaces, uses actual Windows safeStorage, launches the bundled PostgreSQL/backend, checks a streamed workspace request, verifies automatic encrypted restart/persisted history, performs same-version replacement, and uninstalls. Download an installer for release only after its workflow and matching acceptance receipt pass.

The macOS packaging scripts refresh exact Go notices and verified resource hashes before assembling a DMG. `test-installed-desktop.mjs --mock-storage-smoke` checks the copied app, writes/cancellation/peer exchange, replacement/restart and database recovery using simulated credential storage. It does not establish real installed Keychain, Gatekeeper, minimum-OS or cross-version acceptance.

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
