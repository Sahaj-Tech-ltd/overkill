#!/usr/bin/env bash
# Required build/test gates. FULL=true adds installed optional Go analyzers.
# --fix formats tracked Go files before checking; no command downloads tools.
set -euo pipefail
cd "$(git rev-parse --show-toplevel)"
source scripts/hook-common.sh
fix=false
if [ "${1:-}" = '--fix' ]; then fix=true; fi

# Gitleaks implements its own deadline on macOS and Linux. A deadline failure
# remains a failure: an incomplete scan cannot establish absence of secrets.
run_check 'Secret scan' gitleaks detect --timeout 30

go_files=()
while IFS= read -r file; do go_files+=("$file"); done < <(git ls-files '*.go')
check_format() {
    local output
    if [ "${#go_files[@]}" -eq 0 ]; then return 0; fi
    if "$fix"; then gofmt -w "${go_files[@]}"; fi
    output=$(gofmt -l "${go_files[@]}") || return $?
    if [ -n "$output" ]; then printf '%s\n' "$output"; return 1; fi
}
run_check 'Go formatting' check_format
run_check 'Go vet' go vet ./...
run_check 'Go tests' go test -count=1 -timeout=5m ./...
run_check 'Go race' go test -race -count=1 -timeout=8m ./...
run_check 'Go build' go build ./...
run_check 'TUI typecheck' npm --prefix tui run typecheck
run_check 'TUI build' npm --prefix tui run build
run_check 'TUI runtime imports' npm --prefix tui run test:build
run_check 'npm audit' npm --prefix tui audit --audit-level=high
if [ "${FULL:-false}" = true ]; then
    run_check 'Go lint' golangci-lint run --timeout=5m ./...
    run_check 'Go vulnerabilities' govulncheck ./...
fi
finish_checks
