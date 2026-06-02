package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type field struct {
	values map[int]bool
}

type CronExpr struct {
	Minute  field
	Hour    field
	Day     field
	Month   field
	Weekday field
}

func newField() field {
	return field{values: make(map[int]bool)}
}

func (f field) matches(v int) bool {
	return f.values[v]
}

func ParseCron(expr string) (*CronExpr, error) {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return nil, fmt.Errorf("cron: expected 5 fields, got %d", len(parts))
	}

	ranges := [5][2]int{
		{0, 59},
		{0, 23},
		{1, 31},
		{1, 12},
		{0, 6},
	}

	var ce CronExpr
	fields := []*field{&ce.Minute, &ce.Hour, &ce.Day, &ce.Month, &ce.Weekday}

	for i, p := range parts {
		f, err := parseField(p, ranges[i][0], ranges[i][1])
		if err != nil {
			return nil, fmt.Errorf("cron: field %d: %w", i+1, err)
		}
		*fields[i] = f
	}

	return &ce, nil
}

func parseField(s string, min, max int) (field, error) {
	f := newField()

	for _, part := range strings.Split(s, ",") {
		if err := parseFieldPart(part, min, max, f); err != nil {
			return f, err
		}
	}

	if len(f.values) == 0 {
		return f, fmt.Errorf("cron: empty field values")
	}

	return f, nil
}

func parseFieldPart(s string, min, max int, f field) error {
	if strings.Contains(s, "/") {
		return parseStep(s, min, max, f)
	}
	if strings.Contains(s, "-") {
		return parseRange(s, min, max, f)
	}
	if s == "*" {
		for i := min; i <= max; i++ {
			f.values[i] = true
		}
		return nil
	}

	v, err := strconv.Atoi(s)
	if err != nil {
		return fmt.Errorf("cron: invalid value %q", s)
	}
	if v < min || v > max {
		return fmt.Errorf("cron: value %d out of range [%d,%d]", v, min, max)
	}
	f.values[v] = true
	return nil
}

func parseStep(s string, min, max int, f field) error {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 {
		return fmt.Errorf("cron: invalid step expression %q", s)
	}

	base := parts[0]
	step, err := strconv.Atoi(parts[1])
	if err != nil || step <= 0 {
		return fmt.Errorf("cron: invalid step value %q", parts[1])
	}

	rangeMin := min
	rangeMax := max

	if base != "*" {
		rParts := strings.SplitN(base, "-", 2)
		if len(rParts) == 2 {
			rangeMin, err = strconv.Atoi(rParts[0])
			if err != nil {
				return fmt.Errorf("cron: invalid range start %q", rParts[0])
			}
			rangeMax, err = strconv.Atoi(rParts[1])
			if err != nil {
				return fmt.Errorf("cron: invalid range end %q", rParts[1])
			}
		} else {
			v, err := strconv.Atoi(base)
			if err != nil {
				return fmt.Errorf("cron: invalid step base %q", base)
			}
			rangeMin = v
			rangeMax = max
		}
	}

	for i := rangeMin; i <= rangeMax; i += step {
		if i >= min && i <= max {
			f.values[i] = true
		}
	}

	return nil
}

func parseRange(s string, min, max int, f field) error {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return fmt.Errorf("cron: invalid range %q", s)
	}

	start, err := strconv.Atoi(parts[0])
	if err != nil {
		return fmt.Errorf("cron: invalid range start %q", parts[0])
	}
	end, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Errorf("cron: invalid range end %q", parts[1])
	}

	if start > end {
		return fmt.Errorf("cron: range start %d > end %d", start, end)
	}
	if start < min || end > max {
		return fmt.Errorf("cron: range [%d,%d] out of bounds [%d,%d]", start, end, min, max)
	}

	for i := start; i <= end; i++ {
		f.values[i] = true
	}

	return nil
}

func (c *CronExpr) Next(after time.Time) time.Time {
	t := after.Add(time.Minute).Truncate(time.Minute)

	loc := t.Location()

	for i := 0; i < 366*24*60; i++ {
		if !c.Month.matches(int(t.Month())) {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
			continue
		}
		if !c.Day.matches(t.Day()) {
			t = t.AddDate(0, 0, 1).Truncate(time.Hour).Truncate(time.Minute)
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, loc)
			continue
		}
		if !c.Weekday.matches(int(t.Weekday())) {
			t = t.AddDate(0, 0, 1).Truncate(time.Hour).Truncate(time.Minute)
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, loc)
			continue
		}
		if !c.Hour.matches(t.Hour()) {
			t = t.Add(time.Hour).Truncate(time.Hour)
			continue
		}
		if !c.Minute.matches(t.Minute()) {
			t = t.Add(time.Minute)
			continue
		}

		return t
	}

	return time.Time{}
}
