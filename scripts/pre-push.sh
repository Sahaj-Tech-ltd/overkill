#!/usr/bin/env bash
# pre-push.sh — Run before every git push. Catches everything GitHub CI would flag.
# Usage: ./scripts/pre-push.sh        (after commit, before push)
#        ./scripts/pre-push.sh --fix  (auto-fix formatting)

set -euo pipefail

RED='\033[31m'; GREEN='\033[32m'; YELLOW='\033[33m'; CYAN='\033[36m'; NC='\033[0m'
PASS="${GREEN}✓${NC}"; FAIL="${RED}✗${NC}"; WARN="${YELLOW}⚠${NC}"
FIX_MODE=false; [[ "${1:-}" == "--fix" ]] && FIX_MODE=true

cd "$(git rev-parse --show-toplevel)"
echo -e "${CYAN}━━━ Overkill Pre-Push Check ━━━${NC}\n"

failures=0

# ── 1. Secret scanning ──────────────────────────────────────────────
echo -n "  [1/8] Secret scan (gitleaks) ... "
if command -v gitleaks &>/dev/null; then
    # git mode (no --no-git): scans only git-tracked files, much faster
    # 30s timeout prevents hanging on large repos
    if [ -f .gitleaks.toml ]; then
        config_flag="-c .gitleaks.toml"
    else
        config_flag=""
    fi
    result=$(timeout 30 gitleaks detect $config_flag 2>&1) || gitleaks_exit=$?
    gitleaks_exit=${gitleaks_exit:-0}
    if [ "$gitleaks_exit" -eq 124 ]; then
        echo -e "${YELLOW}timeout (30s)${NC}"
    elif [ "$gitleaks_exit" -ne 0 ]; then
        echo -e "$FAIL"
        echo "$result" | tail -20
        ((failures++))
    else
        echo -e "$PASS"
    fi
else
    # Fallback: basic grep for common patterns (exclude deprecated, tui/node_modules)
    if grep -rlE 'ghp_[A-Za-z0-9]{36,}|gho_[A-Za-z0-9]{36,}|sk-[A-Za-z0-9]{32,}|xai-[A-Za-z0-9]{32,}' \
        --exclude-dir=tui/node_modules --exclude-dir=deprecated . 2>/dev/null | head -1 | grep -q .; then
        echo -e "$FAIL  (found secrets — run: gitleaks detect)"
        ((failures++))
    else
        echo -e "$PASS  (basic grep, install gitleaks for thorough)"
    fi
fi

# ── 2. Go formatting ─────────────────────────────────────────────────
echo -n "  [2/8] Go formatting (gofmt) ... "
unformatted=$(gofmt -l . 2>/dev/null)
if [ -n "$unformatted" ]; then
    if $FIX_MODE; then
        gofmt -w . 2>/dev/null
        echo -e "${YELLOW}fixed${NC} ($(echo "$unformatted" | wc -l) files)"
    else
        echo -e "$FAIL  ($(echo "$unformatted" | wc -l) files)"
        echo "$unformatted" | head -10 | sed 's/^/       /'
        ((failures++))
    fi
else
    echo -e "$PASS"
fi

# ── 3. Go vet ────────────────────────────────────────────────────────
echo -n "  [3/8] Go vet ... "
if go vet ./... 2>&1 | grep -q .; then
    echo -e "$FAIL"
    go vet ./... 2>&1 | head -5 | sed 's/^/       /'
    ((failures++))
else
    echo -e "$PASS"
fi

# ── 4. golangci-lint (opt-in via --full) ─────────────────────────────
echo -n "  [4/8] Go lint (golangci-lint) ... "
if [ "${FULL:-false}" = "true" ] && command -v golangci-lint &>/dev/null; then
    changed=$(git diff --cached --name-only --diff-filter=ACM 2>/dev/null | grep '\.go$' | grep -v '^deprecated/' | grep -v '_test\.go$' | xargs -I{} dirname {} | sort -u | sed 's|^|./|' | tr '\n' ' ')
    if [ -z "$changed" ]; then
        echo -e "${YELLOW}no Go changes${NC}"
    else
        if golangci-lint run --timeout=60s $changed 2>&1 | grep -qE 'issues found'; then
            echo -e "$FAIL"
            golangci-lint run --timeout=60s $changed 2>&1 | head -15 | sed 's/^/       /'
            ((failures++))
        else
            echo -e "$PASS"
        fi
    fi
else
    echo -e "${YELLOW}skipped (use FULL=true for full lint)${NC}"
fi

# ── 5. Go vulnerability check ────────────────────────────────────────
echo -n "  [5/8] Go vulns (govulncheck) ... "
if command -v govulncheck &>/dev/null; then
    if govulncheck ./... 2>&1 | grep -q 'Vulnerability #'; then
        echo -e "$FAIL"
        govulncheck ./... 2>&1 | grep 'Vulnerability' | head -5 | sed 's/^/       /'
        ((failures++))
    else
        echo -e "$PASS"
    fi
else
    echo -e "${YELLOW}skipped (not installed)${NC}"
fi

# ── 6. Go security (golangci-lint gosec) ─────────────────────────────
echo -n "  [6/8] Go security (gosec) ... "
if [ "${FULL:-false}" = "true" ] && command -v golangci-lint &>/dev/null; then
    changed=$(git diff --cached --name-only --diff-filter=ACM 2>/dev/null | grep '\.go$' | grep -v '^deprecated/' | xargs -I{} dirname {} | sort -u | sed 's|^|./|' | tr '\n' ' ')
    if [ -z "$changed" ]; then
        echo -e "${YELLOW}no Go changes${NC}"
    else
        if golangci-lint run --timeout=60s --disable-all --enable=gosec $changed 2>&1 | grep -qE 'issues found'; then
            echo -e "$FAIL"
            golangci-lint run --timeout=60s --disable-all --enable=gosec $changed 2>&1 | head -15 | sed 's/^/       /'
            ((failures++))
        else
            echo -e "$PASS"
        fi
    fi
else
    echo -e "${YELLOW}skipped (use FULL=true)${NC}"
fi

# ── 7. TypeScript checks ─────────────────────────────────────────────
echo -n "  [7/8] TS (prettier + tsc) ... "
if [ -d "tui" ] && [ -f "tui/package.json" ]; then
    cd tui
    prettier_ok=true
    npx prettier --check 'src/**/*.{ts,tsx}' --loglevel silent 2>/dev/null || prettier_ok=false
    tsc_ok=true
    npx tsc --noEmit 2>/dev/null || tsc_ok=false
    cd ..
    if $prettier_ok && $tsc_ok; then
        echo -e "$PASS"
    else
        if $FIX_MODE && ! $prettier_ok; then
            cd tui && npx prettier --write 'src/**/*.{ts,tsx}' --loglevel silent 2>/dev/null && cd ..
            echo -e "${YELLOW}prettier fixed${NC}"
        else
            echo -e "$FAIL  (run with --fix for prettier)"
            ((failures++))
        fi
    fi
else
    echo -e "${YELLOW}skipped (no tui/)${NC}"
fi

# ── 8. npm audit ─────────────────────────────────────────────────────
echo -n "  [8/8] npm audit ... "
if [ -d "tui" ] && [ -f "tui/package.json" ]; then
    cd tui
    audit_out=$(npm audit --audit-level=high 2>&1) || true
    if echo "$audit_out" | grep -qE '[1-9][0-9]* vulnerabilities'; then
        echo -e "$FAIL"
        npm audit 2>&1 | tail -10 | sed 's/^/       /'
        ((failures++))
    else
        echo -e "$PASS"
    fi
    cd ..
else
    echo -e "${YELLOW}skipped (no tui/)${NC}"
fi

# ── Summary ───────────────────────────────────────────────────────────
echo ""
if [ $failures -eq 0 ]; then
    echo -e "  ${GREEN}All checks passed. Safe to push.${NC}"
    exit 0
else
    echo -e "  ${RED}$failures check(s) failed.${NC} Fix before pushing."
    echo -e "  Run ${CYAN}./scripts/pre-push.sh --fix${NC} to auto-fix formatting."
    exit 1
fi
