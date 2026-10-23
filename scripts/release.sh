#!/usr/bin/env bash
# Prepare CLI release binaries. Native frontend bundles/DMGs are a separate gate.
set -euo pipefail
version="${1:-}"
dry_run=false
if [ "${2:-}" = --dry-run ]; then dry_run=true; fi
if [ "$#" -gt 2 ] || { [ -n "${2:-}" ] && [ "${2:-}" != --dry-run ]; } || [[ ! "$version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([-+][0-9A-Za-z.+-]+)?$ ]]; then
    echo 'Usage: scripts/release.sh vMAJOR.MINOR.PATCH[-prerelease] [--dry-run]' >&2
    exit 1
fi
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
targets=(linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64)
# Return before git tagging, directory creation, checksum truncation, or builds.
if "$dry_run"; then
    printf 'Would require a clean working tree, build %s and generate checksums.txt:\n' "$version"
    printf '  %s\n' "${targets[@]}"
    printf 'Would stage assets in %s/dist/%s and create the local tag after successful builds.\n' "$repo_root" "$version"
    exit 0
fi
cd "$repo_root"
if [ -n "$(git status --porcelain)" ]; then
    echo 'Working tree is dirty; commit or stash changes before release preparation.' >&2
    exit 1
fi
destination="$repo_root/dist/$version"
if [ -e "$destination" ]; then echo "Release directory already exists: $destination" >&2; exit 1; fi
stage=$(mktemp -d "${TMPDIR:-/tmp}/overkill-release.XXXXXXXX")
trap 'rm -rf "$stage"' EXIT
for target in "${targets[@]}"; do
    IFS=/ read -r goos goarch <<< "$target"
    asset="overkill-$goos-$goarch"
    if [ "$goos" = windows ]; then asset="$asset.exe"; fi
    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath \
        -ldflags="-s -w -X main.Version=$version" -o "$stage/$asset" ./cmd/overkill
done
(
    cd "$stage"
    for asset in overkill-*; do
        if command -v sha256sum >/dev/null 2>&1; then sha256sum "$asset"
        elif command -v shasum >/dev/null 2>&1; then shasum -a 256 "$asset"
        else echo 'SHA256 generation requires sha256sum or shasum' >&2; exit 1
        fi
    done > checksums.txt
)
python3 "$repo_root/scripts/verify-release-assets.py" "$stage"
mkdir -p "$repo_root/dist"
mv "$stage" "$destination"
if ! git rev-parse --verify "refs/tags/$version" >/dev/null 2>&1; then
    git tag -a "$version" -m "Release $version"
fi
printf 'Prepared verified CLI release assets in %s. Nothing was pushed or published.\n' "$destination"
printf 'Push the tag after required checks pass; release.yml generates publication notes.\n'
