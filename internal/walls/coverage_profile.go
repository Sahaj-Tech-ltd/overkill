package walls

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

var coverageLocation = regexp.MustCompile(`^.+:[0-9]+\.[0-9]+,[0-9]+\.[0-9]+$`)

func parseGoCoverage(ctx context.Context, profile io.Reader) (float64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if profile == nil {
		return 0, fmt.Errorf("coverage profile is required")
	}
	const maxProfileBytes = 16 << 20
	data, err := io.ReadAll(io.LimitReader(profile, maxProfileBytes+1))
	if err != nil {
		return 0, fmt.Errorf("read coverage profile: %w", err)
	}
	if len(data) > maxProfileBytes {
		return 0, fmt.Errorf("coverage profile exceeds 16 MiB")
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) < 1 || (lines[0] != "mode: set" && lines[0] != "mode: count" && lines[0] != "mode: atomic") {
		return 0, fmt.Errorf("invalid Go coverage mode")
	}
	type block struct {
		statements uint64
		covered    bool
	}
	seen := map[string]block{}
	var total, covered uint64
	for _, line := range lines[1:] {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		// Go profile locations contain the exact source filename, which may
		// itself contain spaces. Only the final two ASCII-space-separated fields
		// are metadata; normalizing the prefix can merge distinct source blocks.
		last := strings.LastIndexByte(line, ' ')
		if last < 0 {
			return 0, fmt.Errorf("invalid Go coverage record")
		}
		previous := strings.LastIndexByte(line[:last], ' ')
		if previous < 0 {
			return 0, fmt.Errorf("invalid Go coverage record")
		}
		location := line[:previous]
		statementText, countText := line[previous+1:last], line[last+1:]

		colon := strings.LastIndex(location, ":")
		if colon < 1 || !coverageLocation.MatchString(location) {
			return 0, fmt.Errorf("invalid coverage location")
		}
		coordinates := strings.FieldsFunc(location[colon+1:], func(r rune) bool { return r == '.' || r == ',' })
		if len(coordinates) != 4 {
			return 0, fmt.Errorf("invalid coverage coordinates")
		}
		for _, coordinate := range coordinates {
			if value, err := strconv.ParseUint(coordinate, 10, 32); err != nil || value == 0 {
				return 0, fmt.Errorf("invalid coverage coordinate")
			}
		}
		statements, err := strconv.ParseUint(statementText, 10, 32)
		if err != nil {
			return 0, fmt.Errorf("invalid coverage statement count")
		}
		count, err := strconv.ParseUint(countText, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid coverage execution count")
		}
		if prior, exists := seen[location]; exists {
			if prior.statements != statements {
				return 0, fmt.Errorf("inconsistent duplicate coverage block")
			}
			if count > 0 && !prior.covered {
				covered += statements
			}
			seen[location] = block{statements, prior.covered || count > 0}
		} else {
			total += statements
			if count > 0 {
				covered += statements
			}
			seen[location] = block{statements, count > 0}
		}
	}
	if total == 0 {
		return 0, fmt.Errorf("coverage profile contains no measurable statements")
	}
	return float64(covered) / float64(total), nil
}
