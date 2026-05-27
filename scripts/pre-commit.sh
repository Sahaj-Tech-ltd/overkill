#!/usr/bin/env bash
# pre-commit.sh — Run before every commit. Tests changed packages only.
# Fast enough to not be annoying (<30s target).

set -euo pipefail

RED='\033[31m'; GREEN='\033[32m'; YELLOW='\033[33m'; CYAN='\033[36m'; NC='\033[0m'
PASS="${GREEN}✓${NC}"; FAIL="${RED}✗${NC}"

cd "$(git rev-parse --show-toplevel)"
echo -e "${CYAN}━━━ Pre-Commit Tests ━━━${NC}\n"

failures=0

# ── 1. Find changed Go packages ─────────────────────────────────────
echo -n "  [1/3] Finding changed packages ... "
staged=$(git diff --cached --name-only --diff-filter=ACM 2>/dev/null | grep '\.go$' | grep -v '^deprecated/' || true)

if [ -z "$staged" ]; then
    echo -e "${YELLOW}no Go changes${NC}"
    echo ""
    echo -e "  ${GREEN}Nothing to test. Commit away.${NC}"
    exit 0
fi

# Extract unique package directories
pkgs=$(echo "$staged" | xargs -I{} dirname {} | sort -u | sed 's|^|./|')
echo -e "$PASS  ($(echo "$pkgs" | wc -l) package(s))"

# ── 2. Run tests for changed packages ───────────────────────────────
echo -n "  [2/3] Running tests ... "
# Run each changed package + its _test.go siblings
pkg_list=$(echo "$pkgs" | tr '\n' ' ')
test_output=$(go test -short -count=1 -timeout=60s $pkg_list 2>&1) || test_exit=$?

if [ "${test_exit:-0}" -ne 0 ]; then
    echo -e "$FAIL"
    echo ""
    echo "$test_output" | grep -E '^(--- FAIL|FAIL|ok|\?)' | sed 's/^/       /'
    echo ""
    # Show first few failures
    echo "$test_output" | grep -A3 "^--- FAIL" | head -30 | sed 's/^/       /'
    ((failures++))
else
    echo -e "$PASS"
    echo "$test_output" | grep -E '^(ok|\?)' | sed 's/^/       /'
fi

# ── 3. Race detector on changed packages ────────────────────────────
echo -n "  [3/3] Race detector ... "
race_output=$(go test -race -short -count=1 -timeout=60s $pkg_list 2>&1) || race_exit=$?

if [ "${race_exit:-0}" -ne 0 ]; then
    echo -e "$FAIL"
    echo "$race_output" | grep "DATA RACE" -A10 | head -20 | sed 's/^/       /'
    ((failures++))
else
    echo -e "$PASS"
fi

# ── Summary ─────────────────────────────────────────────────────────
echo ""
if [ $failures -eq 0 ]; then
    echo -e "  ${GREEN}All tests passed. Committing.${NC}"
    exit 0
else
    echo -e "  ${RED}$failures check(s) failed.${NC} Fix before committing."
    echo -e "  Run ${CYAN}go test -v -count=1 $pkg_list${NC} to debug."
    exit 1
fi
