#!/usr/bin/env bash
# Run checks for staged code. Install bridge[dev] and npm ci in tui first.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
source scripts/hook-common.sh

staged=$(git diff --cached --name-only --diff-filter=ACMR)
packages=()
while IFS= read -r file; do
    case "$file" in
        deprecated/*) continue ;;
        *.go) packages+=("./$(dirname "$file")") ;;
    esac
done <<< "$staged"
if [ "${#packages[@]}" -gt 0 ]; then
    run_check 'Go tests' go test -short -count=1 -timeout=60s "${packages[@]}"
    run_check 'Go race' go test -race -short -count=1 -timeout=60s "${packages[@]}"
fi
if printf '%s\n' "$staged" | grep -Eq '^(bridge/.*\.py|bridge/pyproject\.toml)$'; then
    run_check 'Python lint' ruff check bridge/
    # pytest-timeout is declared in bridge's dev extras. Missing tooling is a
    # failed check with its original diagnostic, never an implicit success.
    run_python_tests() { (cd bridge && python -m pytest -x -q --timeout=30); }
    run_check 'Python tests' run_python_tests
fi
if printf '%s\n' "$staged" | grep -Eq '^tui/.*\.(ts|tsx|js|jsx|mjs|json)$'; then
    run_check 'TUI typecheck' npm --prefix tui run typecheck
    run_check 'TUI build' npm --prefix tui run build
    run_check 'TUI runtime imports' npm --prefix tui run test:build
fi
while IFS= read -r file; do
    case "$file" in
        *.sh)
            if command -v shellcheck >/dev/null 2>&1; then
                run_check "Shell $file" shellcheck "$file"
            else
                run_check "Shell syntax $file" bash -n "$file"
            fi ;;
    esac
done <<< "$staged"
finish_checks
