package diagnostic

import (
	"fmt"
	"sort"
	"strings"
)

func FormatReport(report *DiagnosticReport) string {
	var sb strings.Builder

	sb.WriteString("## Diagnostic Report\n\n")
	sb.WriteString(fmt.Sprintf("**Error:** %s\n", report.Error))
	sb.WriteString(fmt.Sprintf("**Type:** %s\n", report.ErrorType))
	sb.WriteString(fmt.Sprintf("**Confidence:** %.0f%%\n", report.Confidence*100))

	if len(report.AffectedFiles) > 0 {
		sb.WriteString("\n### Affected Files\n")
		for _, f := range report.AffectedFiles {
			modified := ""
			if f.Modified {
				modified = " *(modified)*"
			}
			role := f.Role
			if role == "" {
				role = "unknown"
			}
			sb.WriteString(fmt.Sprintf("- `%s` — %s%s\n", f.Path, role, modified))
		}
	}

	if len(report.RootCauses) > 0 {
		sb.WriteString("\n### Root Causes\n")
		sortedCauses := make([]RootCause, len(report.RootCauses))
		copy(sortedCauses, report.RootCauses)
		sort.Slice(sortedCauses, func(i, j int) bool {
			return sortedCauses[i].Confidence > sortedCauses[j].Confidence
		})
		for i, rc := range sortedCauses {
			sb.WriteString(fmt.Sprintf("%d. %s (confidence: %.0f%%)\n", i+1, rc.Description, rc.Confidence*100))
			if rc.File != "" {
				if rc.Line > 0 {
					sb.WriteString(fmt.Sprintf("   - File: `%s:%d`\n", rc.File, rc.Line))
				} else {
					sb.WriteString(fmt.Sprintf("   - File: `%s`\n", rc.File))
				}
			}
			for _, e := range rc.Evidence {
				sb.WriteString(fmt.Sprintf("   - Evidence: %s\n", e))
			}
		}
	}

	if report.Recommendation != "" {
		sb.WriteString("\n### Recommended Approach\n")
		sb.WriteString(report.Recommendation)
		sb.WriteString("\n")

		if len(report.Approaches) > 0 {
			best := report.Approaches[0]
			sb.WriteString(FormatApproach(&best))
		}
	}

	return sb.String()
}

func FormatApproach(approach *Approach) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("- Confidence: %.0f%%\n", approach.Confidence*100))
	sb.WriteString(fmt.Sprintf("- Risk: %s\n", approach.Risk))

	if len(approach.Steps) > 0 {
		sb.WriteString("- Steps:\n")
		for _, step := range approach.Steps {
			sb.WriteString(fmt.Sprintf("  1. %s\n", step))
		}
	}

	if len(approach.FilesToChange) > 0 {
		sb.WriteString("- Files to change:\n")
		for _, f := range approach.FilesToChange {
			sb.WriteString(fmt.Sprintf("  - `%s`\n", f))
		}
	}

	if approach.EstimatedTime != "" {
		sb.WriteString(fmt.Sprintf("- Estimated time: %s\n", approach.EstimatedTime))
	}

	return strings.TrimSuffix(sb.String(), "\n")
}
