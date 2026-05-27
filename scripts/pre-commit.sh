#!/usr/bin/env bash
# pre-commit.sh — Run before every commit. Language-aware: tests only
# what changed (Go, Python, TypeScript). Fast path when nothing changed.
# Target: <15s for typical commits.

set -euo pipefail

RED='\033[31m'; GREEN='\033[32m'; YELLOW='\033[33m'; CYAN='\033[36m'; NC='\033[0m'
PASS="${GREEN}✓${NC}"; FAIL="${RED}✗${NC}"; SKIP="${YELLOW}—${NC}"

cd "$(git rev-parse --show-toplevel)"
echo -e "${CYAN}━━━ Pre-Commit ━━━${NC}\n"

failures=0
ran_something=false

# ── What changed? ────────────────────────────────────────────────────
staged=$(git diff --cached --name-only --diff-filter=ACM 2>/dev/null || true)

go_files=$(echo "$staged" | grep '\.go$' | grep -v '^deprecated/' || true)
py_files=$(echo "$staged" | grep '\.py$' | grep -v '^deprecated/' || true)
ts_files=$(echo "$staged" | grep -E '\.(ts|tsx|js|jsx)$' | grep -v 'node_modules/' | grep -v '^deprecated/' || true)
sh_files=$(echo "$staged" | grep -E '\.sh$' || true)

# ── 1. Go ───────────────────────────────────────────────────────────
if [ -n "$go_files" ]; then
    ran_something=true
    pkgs=$(echo "$go_files" | xargs -I{} dirname {} | sort -u | sed 's|^|./|')
    pkg_list=$(echo "$pkgs" | tr '\n' ' ')

    echo -n "  Go test ($(echo "$pkgs" | wc -l) pkg) ... "
    output=$(go test -short -count=1 -timeout=60s $pkg_list 2>&1) || test_exit=$?
    if [ "${test_exit:-0}" -ne 0 ]; then
        echo -e "$FAIL"
        echo "$output" | grep -E '^(--- FAIL|FAIL|ok|\?)' | sed 's/^/       /'
        echo "$output" | grep -A3 "^--- FAIL" | head -20 | sed 's/^/       /'
        ((failures++))
    else
        echo -e "$PASS"
    fi

    echo -n "  Go race  ... "
    race_out=$(go test -race -short -count=1 -timeout=60s $pkg_list 2>&1) || race_exit=$?
    if [ "${race_exit:-0}" -ne 0 ]; then
        echo -e "$FAIL"
        echo "$race_out" | grep "DATA RACE" -A8 | head -20 | sed 's/^/       /'
        ((failures++))
    else
        echo -e "$PASS"
    fi
else
    echo -e "  Go         ${SKIP}  (no changes)"
fi

# ── 2. Python ───────────────────────────────────────────────────────
if [ -n "$py_files" ]; then
    ran_something=true
    echo -n "  Python ruff ... "
    if command -v ruff &>/dev/null; then
        ruff_out=$(ruff check bridge/ --exclude '*/proto/*' 2>&1) || ruff_exit=$?
        if [ "${ruff_exit:-0}" -ne 0 ]; then
            echo -e "$FAIL"
            echo "$ruff_out" | head -10 | sed 's/^/       /'
            ((failures++))
        else
            echo -e "$PASS"
        fi
    else
        echo -e "${YELLOW}ruff not installed${NC}"
    fi

    echo -n "  Python test ... "
    if [ -f bridge/pyproject.toml ] || [ -f bridge/setup.py ]; then
        pytest_out=$(cd bridge && python -m pytest -x -q --timeout=30 2>&1) || pytest_exit=$?
        if [ "${pytest_exit:-0}" -ne 0 ]; then
            echo -e "$FAIL"
            echo "$pytest_out" | tail -15 | sed 's/^/       /'
            ((failures++))
        else
            echo -e "$PASS"
        fi
    else
        echo -e "${SKIP}  (no test config)"
    fi
else
    echo -e "  Python     ${SKIP}  (no changes)"
fi

# ── 3. TypeScript / React ───────────────────────────────────────────
if [ -n "$ts_files" ]; then
    ran_something=true

    echo -n "  TS typecheck ... "
    if [ -f tui/package.json ]; then
        tsc_out=$(cd tui && npx tsc --noEmit 2>&1) || tsc_exit=$?
        if [ "${tsc_exit:-0}" -ne 0 ]; then
            echo -e "$FAIL"
            echo "$tsc_out" | head -10 | sed 's/^/       /'
            ((failures++))
        else
            echo -e "$PASS"
        fi
    else
        echo -e "${SKIP}  (no tui/)"
    fi

    echo -n "  TS test     ... "
    if [ -f tui/package.json ]; then
        if grep -q '"test"' tui/package.json 2>/dev/null; then
            test_out=$(cd tui && npm test -- --run 2>&1) || test_exit=$?
            if [ "${test_exit:-0}" -ne 0 ]; then
                echo -e "$FAIL"
                echo "$test_out" | tail -10 | sed 's/^/       /'
                ((failures++))
            else
                echo -e "$PASS"
            fi
        else
            echo -e "${SKIP}  (no test script)"
        fi
    else
        echo -e "${SKIP}"
    fi
else
    echo -e "  TypeScript ${SKIP}  (no changes)"
fi

# ── 4. Shell ────────────────────────────────────────────────────────
if [ -n "$sh_files" ]; then
    ran_something=true
    echo -n "  Shell check ... "
    if command -v shellcheck &>/dev/null; then
        sh_out=$(shellcheck $sh_files 2>&1) || sh_exit=$?
        if [ "${sh_exit:-0}" -ne 0 ]; then
            echo -e "$FAIL"
            echo "$sh_out" | head -10 | sed 's/^/       /'
            ((failures++))
        else
            echo -e "$PASS"
        fi
    else
        # Basic: check for common issues
        bash -n scripts/pre-commit.sh 2>/dev/null && echo -e "$PASS  (bash -n)" || echo -e "${YELLOW}install shellcheck for thorough${NC}"
    fi
else
    echo -e "  Shell      ${SKIP}  (no changes)"
fi

# ── Summary ─────────────────────────────────────────────────────────
echo ""
if ! $ran_something; then
    echo -e "  ${YELLOW}No code changes detected. Skipping all checks.${NC}"
elif [ $failures -eq 0 ]; then
    echo -e "  ${GREEN}All checks passed. Committing.${NC}"
else
    echo -e "  ${RED}$failures check(s) failed.${NC} Fix before committing."
    exit 1
fi
