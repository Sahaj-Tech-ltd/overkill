package main

import (
	"fmt"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/journal"
	"github.com/Sahaj-Tech-ltd/overkill/internal/personality"
)

// buildRecalibrationProbe assembles a concrete "your predecessor
// model failed here — verify the new model doesn't have the same
// gaps" summary (§4.16 recalibration probe).
//
// We pull failhypo records tagged with the PREVIOUS model_id and
// group by subject to surface the top N concrete failure subjects.
// The agent reads these as a checklist for the first few turns:
// either re-probe to confirm the new model has the same weakness,
// or skip past and rely on the auto-filter to drop the old records
// from `failhypo_search` going forward.
//
// Returns "" when:
//   - no fingerprint changed (prev nil)
//   - no failhypo store wired
//   - no records exist for the previous model's family/version
//
// The result is intentionally TERSE — the agent has many other
// system messages on the boot path, and a long probe summary
// crowds them all.
func buildRecalibrationProbe(fhStore *journal.FailedHypothesisStore, prev *personality.ModelFingerprint, maxItems int) string {
	if fhStore == nil || prev == nil {
		return ""
	}
	prevFamily := strings.TrimSpace(prev.Family)
	prevVersion := strings.TrimSpace(prev.Version)
	if prevFamily == "" && prevVersion == "" {
		return ""
	}
	all, err := fhStore.All()
	if err != nil || len(all) == 0 {
		return ""
	}
	// Group prior-model records by Subject — the noun-phrase the
	// hypothesis was about. "auth-middleware: 4 prior failures" is
	// more actionable than 4 separate lines.
	//
	// Match strategy: a record belongs to the prior model when its
	// ModelID either equals the previous fingerprint's normalized
	// version OR starts with the previous family prefix. This
	// catches both exact-tag matches and family-grain matches when
	// the raw ID and the normalized form drift.
	bySubject := map[string]int{}
	totalPrior := 0
	for _, h := range all {
		if !belongsToPriorModel(h.ModelID, prevFamily, prevVersion) {
			continue
		}
		totalPrior++
		key := strings.TrimSpace(h.Subject)
		if key == "" {
			key = "(untagged)"
		}
		bySubject[key]++
	}
	if totalPrior == 0 {
		return ""
	}

	type subjectCount struct {
		Subject string
		Count   int
	}
	ranked := make([]subjectCount, 0, len(bySubject))
	for s, c := range bySubject {
		ranked = append(ranked, subjectCount{s, c})
	}
	// Sort: count desc, then subject asc for stable output.
	for i := 0; i < len(ranked); i++ {
		for j := i + 1; j < len(ranked); j++ {
			if ranked[j].Count > ranked[i].Count ||
				(ranked[j].Count == ranked[i].Count && ranked[j].Subject < ranked[i].Subject) {
				ranked[i], ranked[j] = ranked[j], ranked[i]
			}
		}
	}
	if maxItems > 0 && len(ranked) > maxItems {
		ranked = ranked[:maxItems]
	}

	label := prevVersion
	if label == "" {
		label = prevFamily
	}
	var b strings.Builder
	fmt.Fprintf(&b, "[recalibration probe] The previous model (%s) failed %d time(s) across %d distinct area(s).\n",
		label, totalPrior, len(bySubject))
	b.WriteString("Use these as a checklist for the first few turns — re-verify each before relying on the new model in the same way:\n")
	for _, r := range ranked {
		fmt.Fprintf(&b, "  - %s (%d prior failure(s))\n", r.Subject, r.Count)
	}
	if len(bySubject) > len(ranked) {
		fmt.Fprintf(&b, "  - …and %d more, query `failhypo_search` with `model_id: %q` for the full set.\n",
			len(bySubject)-len(ranked), label)
	}
	return b.String()
}

// belongsToPriorModel decides whether a failhypo record's ModelID
// tag should be counted against the previous fingerprint. Exact
// version match OR family-prefix match — see buildRecalibrationProbe
// for the rationale.
func belongsToPriorModel(modelID, prevFamily, prevVersion string) bool {
	modelID = strings.ToLower(strings.TrimSpace(modelID))
	if modelID == "" {
		return false
	}
	if prevVersion != "" && modelID == strings.ToLower(prevVersion) {
		return true
	}
	if prevFamily != "" && strings.HasPrefix(modelID, strings.ToLower(prevFamily)) {
		return true
	}
	return false
}
