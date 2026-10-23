#!/usr/bin/env bash
# Shared local hook runner. Never inspect output to decide whether a command
# succeeded: tooling errors can be silent, and diagnostics can be informational.
failures=0
run_check() {
    local label="$1" output status=0
    shift
    printf '  %s ... ' "$label"
    output=$("$@" 2>&1) || status=$?
    if [ "$status" -eq 0 ]; then
        printf 'PASS\n'
    else
        printf 'FAIL (exit %s)\n' "$status"
        printf '%s\n' "$output"
        failures=$((failures + 1))
    fi
}
finish_checks() {
    if [ "$failures" -ne 0 ]; then
        printf '\n%s check(s) failed.\n' "$failures"
        return 1
    fi
    printf '\nAll checks passed.\n'
}
