package cost

import (
	"fmt"
	"strings"
)

// FormatTokens formats a token count into a human-readable abbreviated form:
//
//	<1000      → raw number (e.g. "234")
//	1k-999k    → k suffix with 1 decimal (e.g. "45.2k")
//	1M-999M    → M suffix with 1 decimal (e.g. "1.5M")
//	1B-999B    → B suffix with 1 decimal (e.g. "2.1B")
//	1T+        → T suffix with 1 decimal
func FormatTokens(n int64) string {
	format := "%s"
	if n < 0 {
		format = "-%s"
		n = -n
	}
	abs := uint64(n)
	switch {
	case abs < 1_000:
		return fmt.Sprintf(format, fmt.Sprintf("%d", abs))
	case abs < 1_000_000:
		return fmt.Sprintf(format, floatSuffix(float64(abs)/1_000, "k"))
	case abs < 1_000_000_000:
		return fmt.Sprintf(format, floatSuffix(float64(abs)/1_000_000, "M"))
	case abs < 1_000_000_000_000:
		return fmt.Sprintf(format, floatSuffix(float64(abs)/1_000_000_000, "B"))
	default:
		return fmt.Sprintf(format, floatSuffix(float64(abs)/1_000_000_000_000, "T"))
	}
}

// FormatUSD formats a dollar amount:
//
//	<1000      → "$1.23"
//	1k+        → "$1.2k"
func FormatUSD(n float64) string {
	sign := ""
	if n < 0 {
		sign = "-"
		n = -n
	}
	if n < 1_000 {
		return sign + "$" + fmt.Sprintf("%.2f", n)
	}
	if n < 1_000_000 {
		return sign + "$" + floatSuffix(n/1_000, "k")
	}
	if n < 1_000_000_000 {
		return sign + "$" + floatSuffix(n/1_000_000, "M")
	}
	return sign + "$" + floatSuffix(n/1_000_000_000, "B")
}

func floatSuffix(v float64, suffix string) string {
	s := fmt.Sprintf("%.1f", v)
	s = strings.TrimRight(s, "0")
	s = strings.TrimRight(s, ".")
	return s + suffix
}
