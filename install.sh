#!/bin/sh
# install.sh — POSIX installer for overkill.
#
# Detects platform, prefers `go install` when Go is on PATH, otherwise
# downloads a pre-built binary, drops it in $HOME/.local/bin/overkill (or
# $OVERKILL_INSTALL_DIR), and bootstraps ~/.overkill/.
#
# Env knobs:
#   OVERKILL_INSTALL_DIR=/path        override install location
#   OVERKILL_BUILD_FROM_SOURCE=1      force `go install` path
#   INSTALL_VERBOSE=1              verbose output
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/Sahaj-Tech-ltd/overkill/main/prod/install.sh | sh
#   sh install.sh

set -eu

REPO="Sahaj-Tech-ltd/overkill"
GO_PKG="github.com/Sahaj-Tech-ltd/overkill/cmd/overkill"
RELEASE_BASE="https://github.com/${REPO}/releases/latest/download"

INSTALL_DIR="${OVERKILL_INSTALL_DIR:-${HOME}/.local/bin}"
OVERKILL_HOME="${HOME}/.overkill"
VERBOSE="${INSTALL_VERBOSE:-0}"

# ---------------------------------------------------------------- helpers

say() { printf '%s\n' "[overkill] $*"; }
vsay() { [ "${VERBOSE}" = "1" ] && printf '%s\n' "[overkill] $*" || true; }
die() { printf '%s\n' "[overkill] error: $*" >&2; exit 1; }

need() {
    command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

detect_platform() {
    uname_s=$(uname -s 2>/dev/null || echo unknown)
    uname_m=$(uname -m 2>/dev/null || echo unknown)

    case "$uname_s" in
        Linux)  os=linux ;;
        Darwin) os=darwin ;;
        MINGW*|MSYS*|CYGWIN*)
            die "Native Windows detected. Use the PowerShell installer instead:
  irm https://raw.githubusercontent.com/Sahaj-Tech-ltd/overkill/main/prod/install.ps1 | iex
Or use WSL with this installer, or build from source: 'go install ${GO_PKG}@latest'."
            ;;
        *) die "unsupported OS: $uname_s" ;;
    esac

    case "$uname_m" in
        x86_64|amd64) arch=x86_64 ;;
        arm64|aarch64) arch=arm64 ;;
        *) die "unsupported architecture: $uname_m" ;;
    esac

    PLATFORM="$os"
    ARCH="$arch"
}

ensure_dir() {
    if [ ! -d "$1" ]; then
        vsay "creating $1"
        mkdir -p "$1" || die "could not create $1"
    fi
}

writable() {
    # writable <dir> — true if dir exists and we can write to it, or if any
    # existing ancestor is writable (so we can mkdir -p into it).
    d="$1"
    while [ -n "$d" ] && [ "$d" != "/" ]; do
        if [ -d "$d" ]; then
            [ -w "$d" ] && return 0 || return 1
        fi
        d=$(dirname "$d")
    done
    return 1
}

# ---------------------------------------------------------------- steps

step_platform() {
    detect_platform
    say "detected ${PLATFORM}/${ARCH}"
}

step_install_dir() {
    if writable "$INSTALL_DIR"; then
        ensure_dir "$INSTALL_DIR"
        say "install dir: $INSTALL_DIR"
        return
    fi

    # Fallback to /usr/local/bin only with sudo present.
    if [ "$INSTALL_DIR" = "${HOME}/.local/bin" ] && command -v sudo >/dev/null 2>&1; then
        INSTALL_DIR="/usr/local/bin"
        say "install dir not writable; falling back to $INSTALL_DIR (will use sudo)"
        SUDO="sudo"
        return
    fi

    die "install dir $INSTALL_DIR is not writable and no fallback available"
}

step_build_from_source() {
    BIN_PATH="${INSTALL_DIR}/overkill"
    if [ -n "${OVERKILL_LOCAL_SOURCE:-}" ] && [ -d "${OVERKILL_LOCAL_SOURCE}" ]; then
        say "building from local source at ${OVERKILL_LOCAL_SOURCE}"
        ( cd "${OVERKILL_LOCAL_SOURCE}" && go build -o "$BIN_PATH" ./cmd/overkill ) \
            || die "local source build failed"
    else
        say "building from source via 'go install ${GO_PKG}@latest'"
        GOBIN="$INSTALL_DIR" go install "${GO_PKG}@latest" \
            || die "go install failed (repo may not yet be published; try OVERKILL_LOCAL_SOURCE=/path/to/checkout)"
    fi
    [ -x "$BIN_PATH" ] || die "build completed but $BIN_PATH not found"
}

step_download_binary() {
    need curl
    asset="overkill-${PLATFORM}-${ARCH}"
    url="${RELEASE_BASE}/${asset}"
    sha_url="${url}.sha256"
    tmp=$(mktemp -d 2>/dev/null || mktemp -d -t overkill)
    trap 'rm -rf "$tmp"' EXIT

    say "downloading ${url}"
    if ! curl -fsSL "$url" -o "${tmp}/overkill"; then
        die "failed to download $url — try OVERKILL_BUILD_FROM_SOURCE=1 with Go on PATH"
    fi

    # SHA256 verify if a sibling .sha256 exists upstream.
    if curl -fsSL "$sha_url" -o "${tmp}/overkill.sha256" 2>/dev/null; then
        say "verifying SHA256"
        expected=$(awk '{print $1}' "${tmp}/overkill.sha256")
        if command -v sha256sum >/dev/null 2>&1; then
            actual=$(sha256sum "${tmp}/overkill" | awk '{print $1}')
        elif command -v shasum >/dev/null 2>&1; then
            actual=$(shasum -a 256 "${tmp}/overkill" | awk '{print $1}')
        else
            say "warning: no sha256sum/shasum available, skipping verification"
            actual="$expected"
        fi
        [ "$actual" = "$expected" ] || die "SHA256 mismatch: expected $expected got $actual"
    else
        vsay "no .sha256 sibling found, skipping checksum verification (pre-release)"
    fi

    chmod +x "${tmp}/overkill"
    BIN_PATH="${INSTALL_DIR}/overkill"
    if [ "${SUDO:-}" = "sudo" ]; then
        sudo mv "${tmp}/overkill" "$BIN_PATH"
    else
        mv "${tmp}/overkill" "$BIN_PATH"
    fi
    say "installed binary to $BIN_PATH"
}

step_install() {
    if [ "${OVERKILL_BUILD_FROM_SOURCE:-0}" = "1" ]; then
        command -v go >/dev/null 2>&1 || die "OVERKILL_BUILD_FROM_SOURCE=1 set but 'go' not on PATH"
        step_build_from_source
        return
    fi
    if command -v go >/dev/null 2>&1; then
        step_build_from_source
    else
        step_download_binary
    fi
}

step_path_hint() {
    case ":$PATH:" in
        *":${INSTALL_DIR}:"*) return 0 ;;
    esac
    line="export PATH=\"${INSTALL_DIR}:\$PATH\""
    for rc in "${HOME}/.bashrc" "${HOME}/.zshrc"; do
        [ -f "$rc" ] || continue
        if ! grep -Fq "$line" "$rc" 2>/dev/null; then
            printf '\n# added by overkill installer\n%s\n' "$line" >> "$rc"
            say "added $INSTALL_DIR to PATH in $rc"
        fi
    done
    say "open a new shell or run: $line"
}

step_bootstrap_home() {
    ensure_dir "$OVERKILL_HOME"
    for sub in sessions plugins cache journal/raw memories; do
        ensure_dir "${OVERKILL_HOME}/${sub}"
    done

    cfg="${OVERKILL_HOME}/config.toml"
    if [ ! -f "$cfg" ]; then
        cat > "$cfg" <<'EOF'
# overkill config — edit freely. Run `overkill doctor` to validate.

[agent]
default_model = "anthropic/claude-sonnet-4-5"
max_tokens    = 8192
temperature   = 0.2

[providers.anthropic]
type    = "anthropic"
api_key = "${ANTHROPIC_API_KEY}"

[providers.openai]
type    = "openai"
api_key = "${OPENAI_API_KEY}"

[ui]
theme = "catppuccin-mocha"
EOF
        say "wrote default config to $cfg"
    else
        vsay "config already present at $cfg, leaving untouched"
    fi
}

step_finish() {
    say "done."
    printf '\n'
    printf '  binary  : %s\n' "$BIN_PATH"
    if [ -x "$BIN_PATH" ]; then
        ver=$("$BIN_PATH" --version 2>/dev/null || echo "(version unavailable)")
        printf '  version : %s\n' "$ver"
    fi
    printf '\nNext steps:\n'
    printf '  1. run `overkill` to launch the TUI\n'
    printf '  2. run `overkill doctor` to self-check every subsystem\n'
    printf '  3. run `overkill --help` for the CLI surface\n'
}

# ---------------------------------------------------------------- main

main() {
    SUDO=""
    BIN_PATH=""

    step_platform
    step_install_dir
    step_install
    step_path_hint
    step_bootstrap_home
    step_finish
}

main "$@"
