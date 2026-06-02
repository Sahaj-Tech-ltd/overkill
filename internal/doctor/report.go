package doctor

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// PrettyOptions tunes terminal rendering. NoColor disables ANSI escapes (for
// CI logs and dumb terminals); Verbose forces Detail/Fix to print even on
// SevOK results (default suppresses them to keep happy-path output one-line).
type PrettyOptions struct {
	NoColor bool
	Verbose bool
}

const (
	ansiReset  = "\033[0m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiRed    = "\033[31m"
	ansiBlue   = "\033[34m"
	ansiDim    = "\033[2m"
	ansiBold   = "\033[1m"
)

func badge(sev Severity, noColor bool) string {
	var glyph, color string
	switch sev {
	case SevOK:
		glyph, color = "✓", ansiGreen
	case SevWarn:
		glyph, color = "△", ansiYellow
	case SevFail:
		glyph, color = "✗", ansiRed
	case SevInfo:
		glyph, color = "ℹ", ansiBlue
	case SevSkip:
		glyph, color = "·", ansiDim
	default:
		glyph = "?"
	}
	if noColor {
		return glyph
	}
	return color + glyph + ansiReset
}

// PrettyPrint renders a Summary to w with category groupings and color-coded
// status badges. Detail and Fix are only shown on warn/fail unless Verbose.
func PrettyPrint(w io.Writer, s Summary, opts PrettyOptions) {
	groups := map[Category][]Result{}
	var order []Category
	seen := map[Category]bool{}
	for _, r := range s.Checks {
		if !seen[r.Category] {
			seen[r.Category] = true
			order = append(order, r.Category)
		}
		groups[r.Category] = append(groups[r.Category], r)
	}

	bold := ansiBold
	dim := ansiDim
	reset := ansiReset
	if opts.NoColor {
		bold, dim, reset = "", "", ""
	}

	fmt.Fprintf(w, "%soverkill doctor%s %s%s%s\n\n", bold, reset, dim, s.Timestamp.Format("2006-01-02 15:04:05 UTC"), reset)

	for _, cat := range order {
		fmt.Fprintf(w, "%s%s%s\n", bold, cat, reset)
		for _, r := range groups[cat] {
			fmt.Fprintf(w, "  %s %s", badge(r.Status, opts.NoColor), r.Name)
			if r.Status == SevOK || r.Status == SevSkip {
				if r.Detail != "" && (opts.Verbose || r.Status == SevSkip) {
					fmt.Fprintf(w, " %s%s%s", dim, r.Detail, reset)
				}
				fmt.Fprintln(w)
				continue
			}
			fmt.Fprintln(w)
			if r.Detail != "" {
				fmt.Fprintf(w, "      %s%s%s\n", dim, r.Detail, reset)
			}
			if r.Fix != "" {
				fmt.Fprintf(w, "      %sfix:%s %s\n", bold, reset, r.Fix)
			}
		}
		fmt.Fprintln(w)
	}

	c := s.Counts
	fmt.Fprintf(w, "%ssummary:%s %s%d ok%s  %s%d warn%s  %s%d fail%s  %s%d info%s",
		bold, reset,
		colorize(ansiGreen, opts.NoColor), c.OK, reset,
		colorize(ansiYellow, opts.NoColor), c.Warn, reset,
		colorize(ansiRed, opts.NoColor), c.Fail, reset,
		colorize(ansiBlue, opts.NoColor), c.Info, reset,
	)
	if c.Skip > 0 {
		fmt.Fprintf(w, "  %s%d skip%s", colorize(ansiDim, opts.NoColor), c.Skip, reset)
	}
	fmt.Fprintln(w)
}

func colorize(code string, noColor bool) string {
	if noColor {
		return ""
	}
	return code
}

// JSON marshals the summary as indented JSON suitable for piping.
func JSON(s Summary) ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}

// stringTrim is a tiny helper that keeps Detail/Fix fields tidy when callers
// build them with fmt.Sprintf.
func stringTrim(s string) string { return strings.TrimSpace(s) }
