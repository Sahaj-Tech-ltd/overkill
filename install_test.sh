#!/bin/sh
# install_test.sh — smoke-test install.sh against a temp HOME.
#
# Skips if `go` is not on PATH (the test exercises the source-build path).

set -eu

if ! command -v go >/dev/null 2>&1; then
    echo "[test] go not on PATH, skipping install_test"
    exit 0
fi

here=$(cd "$(dirname "$0")" && pwd)
tmphome=$(mktemp -d)
cleanup() { chmod -R u+w "$tmphome" 2>/dev/null || true; rm -rf "$tmphome"; }
trap cleanup EXIT

echo "[test] running install.sh with HOME=$tmphome"
HOME="$tmphome" \
    OVERKILL_BUILD_FROM_SOURCE=1 \
    OVERKILL_LOCAL_SOURCE="$here" \
    INSTALL_VERBOSE=1 \
    sh "${here}/install.sh"

bin="${tmphome}/.local/bin/overkill"
if [ ! -x "$bin" ]; then
    echo "[test] FAIL: $bin not found or not executable"
    exit 1
fi
echo "[test] PASS: binary at $bin"

for path in \
    "${tmphome}/.overkill/config.toml" \
    "${tmphome}/.overkill/sessions" \
    "${tmphome}/.overkill/plugins" \
    "${tmphome}/.overkill/cache" \
    "${tmphome}/.overkill/journal/raw" \
    "${tmphome}/.overkill/memories"
do
    if [ ! -e "$path" ]; then
        echo "[test] FAIL: skeleton missing $path"
        exit 1
    fi
done
echo "[test] PASS: ~/.overkill/ skeleton created"

if grep -Fq ".local/bin" "${tmphome}/.bashrc" 2>/dev/null \
   || grep -Fq ".local/bin" "${tmphome}/.zshrc" 2>/dev/null
then
    echo "[test] PASS: PATH hint written (or PATH already contained dir)"
else
    # If neither rc file existed, the script silently skipped; this is fine
    # in a fresh tmphome.
    if [ ! -f "${tmphome}/.bashrc" ] && [ ! -f "${tmphome}/.zshrc" ]; then
        echo "[test] OK: no rc files in tmphome, PATH hint skip is expected"
    fi
fi

echo "[test] all checks passed"
