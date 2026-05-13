// Package main — bridge between agent.HallucinationScanner and the
// internal/halluscan package. Lives here so the agent package
// doesn't import halluscan directly.
package main

import (
	"os"

	"github.com/Sahaj-Tech-ltd/overkill/internal/halluscan"
)

// halluscanAdapter implements agent.HallucinationScanner by
// delegating to the halluscan package. Construction is cheap so we
// build a fresh one per agent. The scanner itself has no state
// beyond config — Scan calls are pure functions of (content,
// evidence).
type halluscanAdapter struct {
	scanner *halluscan.Scanner
}

// newHalluscanAdapter returns nil when the scanner is disabled via
// OVERKILL_NO_HALLUSCAN=1. CI / scripted runs that don't want [?]
// markers in their fixture-checked output disable here.
func newHalluscanAdapter() *halluscanAdapter {
	if os.Getenv("OVERKILL_NO_HALLUSCAN") != "" {
		return nil
	}
	return &halluscanAdapter{scanner: halluscan.NewScanner()}
}

// Scan is the agent.HallucinationScanner contract: take the
// assembled response + the evidence corpus, return content with
// [?] markers after unverified backtick-quoted identifiers.
func (a *halluscanAdapter) Scan(content, evidence string) string {
	res := a.scanner.Scan(content, evidence)
	return res.Annotated
}
