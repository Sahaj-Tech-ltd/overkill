#!/bin/bash
# release.sh — cut an overkill release.
#
# Usage:
#   ./scripts/release.sh v0.10.0-beta   # tag, build, sign — ready to upload
#   ./scripts/release.sh v0.10.0-beta --dry-run
#
# What it does:
#   1. Clean working tree check
#   2. Cross-compile 5 targets (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64, windows/amd64)
#   3. Generate SHA256 checksums
#   4. Print upload instructions for GitHub Releases

set -euo pipefail

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

say()    { printf "${GREEN}[release]${NC} %s\n" "$*"; }
warn()   { printf "${YELLOW}[release]${NC} %s\n" "$*"; }
die()    { printf "${RED}[release] error:${NC} %s\n" "$*" >&2; exit 1; }

# ── Args ────────────────────────────────────────────────────────────────────

VERSION="${1:-}"
DRY_RUN=false
[[ "${2:-}" == "--dry-run" ]] && DRY_RUN=true

if [[ -z "$VERSION" ]]; then
    echo "Usage: $0 <version> [--dry-run]"
    echo "Example: $0 v0.10.0-beta"
    exit 1
fi

# ── Pre-flight ──────────────────────────────────────────────────────────────

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$REPO_ROOT"

if ! git diff-index --quiet HEAD -- 2>/dev/null; then
    die "working tree is dirty — commit or stash changes first"
fi

# ── Tag ─────────────────────────────────────────────────────────────────────

if git rev-parse "$VERSION" >/dev/null 2>&1; then
    warn "tag $VERSION already exists — skipping tag creation"
else
    say "creating tag $VERSION"
    if $DRY_RUN; then
        say "[dry-run] would run: git tag -a $VERSION -m 'Release $VERSION'"
    else
        git tag -a "$VERSION" -m "Release $VERSION"
        say "tag created"
    fi
fi

# ── Build ───────────────────────────────────────────────────────────────────

DIST_DIR="$REPO_ROOT/dist"
rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

TARGETS=(
    "linux/amd64"
    "linux/arm64"
    "darwin/amd64"
    "darwin/arm64"
    "windows/amd64"
)

say "cross-compiling $VERSION for ${#TARGETS[@]} targets..."

for target in "${TARGETS[@]}"; do
    IFS='/' read -r goos goarch <<< "$target"
    out="$DIST_DIR/overkill-${goos}-${goarch}"
    [[ "$goos" == "windows" ]] && out="${out}.exe"

    printf "  %-12s %-6s → %s\n" "$goos" "$goarch" "$(basename "$out")"

    if $DRY_RUN; then
        continue
    fi

    CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
        go build -trimpath \
        -ldflags="-s -w -X main.Version=$VERSION" \
        -o "$out" \
        ./cmd/overkill || die "build failed for $goos/$goarch"
done

# ── Checksums ───────────────────────────────────────────────────────────────

CHECKSUMS="$DIST_DIR/checksums.txt"
> "$CHECKSUMS"

say "generating SHA256 checksums..."
for f in "$DIST_DIR"/overkill-*; do
    if $DRY_RUN; then
        echo "  [dry-run] sha256sum $(basename "$f")"
        continue
    fi
    sha256sum "$f" | tee -a "$CHECKSUMS"
done

# ── Summary ─────────────────────────────────────────────────────────────────

say "done."
echo ""
echo "  version  : $VERSION"
echo "  binaries : $(ls "$DIST_DIR"/overkill-* | wc -l) files"
echo "  checksums: $CHECKSUMS"
echo ""
echo "Next steps:"
echo "  1. Push the tag:   git push origin $VERSION"
echo "  2. Create release:  gh release create $VERSION \\"
echo "                        --title '$VERSION' \\"
echo "                        --notes-file CHANGELOG.md \\"
echo "                        dist/overkill-* dist/checksums.txt"
echo "  3. Or upload manually at:"
echo "     https://github.com/Sahaj-Tech-ltd/overkill/releases/new?tag=$VERSION"
echo ""
