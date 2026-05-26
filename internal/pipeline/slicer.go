package pipeline

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type SliceClass string

const (
	ClassHITL SliceClass = "HITL"
	ClassAFK  SliceClass = "AFK"
)

type Slice struct {
	ID             string
	Title          string
	Description    string
	Layers         []string
	Classification SliceClass
	Dependencies   []string
	Priority       int
}

type UserStory struct {
	Actor   string
	Want    string
	Benefit string
}

type PRD struct {
	ProblemStatement string
	Solution         string
	UserStories      []UserStory
	ImplDecisions    []string
	TestDecisions    []string
	OutOfScope       []string
}

var nonAlnum = regexp.MustCompile("[^a-z0-9]+")

func sliceID(title string) string {
	s := strings.ToLower(title)
	s = nonAlnum.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	return s
}

func detectLayers(text string) []string {
	lower := strings.ToLower(text)
	seen := make(map[string]bool)
	var layers []string

	keywords := map[string][]string{
		"schema": {"database", "schema", "migration", "table", "column", "sql"},
		"api":    {"api", "endpoint", "route", "handler", "http", "rest", "grpc"},
		"ui":     {"ui", "frontend", "component", "page", "view", "render", "css", "html"},
		"tests":  {"test", "testing", "spec", "coverage", "assertion"},
	}

	for layer, words := range keywords {
		for _, w := range words {
			if strings.Contains(lower, w) {
				if !seen[layer] {
					seen[layer] = true
					layers = append(layers, layer)
				}
				break
			}
		}
	}

	return layers
}

func classifySlice(text string) SliceClass {
	lower := strings.ToLower(text)
	hits := []string{"user", "human", "approval", "review"}
	for _, w := range hits {
		if strings.Contains(lower, w) {
			return ClassHITL
		}
	}
	return ClassAFK
}

var depPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)depends?\s+on\s+(\S+)`),
	regexp.MustCompile(`(?i)after\s+(\S+)`),
	regexp.MustCompile(`(?i)requires?\s+(\S+)`),
}

func extractDeps(text string) []string {
	lower := strings.ToLower(text)
	var deps []string
	seen := make(map[string]bool)

	for _, pat := range depPatterns {
		matches := pat.FindAllStringSubmatch(lower, -1)
		for _, m := range matches {
			if len(m) > 1 {
				d := strings.Trim(m[1], ".,;: ")
				if d != "" && !seen[d] {
					seen[d] = true
					deps = append(deps, d)
				}
			}
		}
	}

	return deps
}

func DecomposeIntoSlices(spec string) ([]Slice, error) {
	if strings.TrimSpace(spec) == "" {
		return nil, fmt.Errorf("slicer: empty spec")
	}

	lines := strings.Split(spec, "\n")

	type rawSlice struct {
		title       string
		description string
	}

	var slices []rawSlice
	var current *rawSlice

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "## ") {
			if current != nil {
				slices = append(slices, *current)
			}
			current = &rawSlice{
				title:       strings.TrimPrefix(trimmed, "## "),
				description: "",
			}
		} else if strings.HasPrefix(trimmed, "# ") {
			if current != nil {
				slices = append(slices, *current)
			}
			current = &rawSlice{
				title:       strings.TrimPrefix(trimmed, "# "),
				description: "",
			}
		} else if current != nil {
			if current.description != "" || trimmed != "" {
				current.description += line + "\n"
			}
		}
	}

	if current != nil {
		slices = append(slices, *current)
	}

	if len(slices) == 0 {
		slices = append(slices, rawSlice{
			title:       "Implementation",
			description: spec,
		})
	}

	var result []Slice
	for i, rs := range slices {
		id := sliceID(rs.title)
		desc := strings.TrimSpace(rs.description)
		s := Slice{
			ID:             id,
			Title:          strings.TrimSpace(rs.title),
			Description:    desc,
			Layers:         detectLayers(desc),
			Classification: classifySlice(desc),
			Dependencies:   extractDeps(desc),
			Priority:       i + 1,
		}
		result = append(result, s)
	}

	return result, nil
}

func TopologicalSort(slices []Slice) ([]Slice, error) {
	if len(slices) == 0 {
		return nil, nil
	}

	byID := make(map[string]*Slice)
	inDegree := make(map[string]int)
	adj := make(map[string][]string)

	for i := range slices {
		id := slices[i].ID
		byID[id] = &slices[i]
		if _, ok := inDegree[id]; !ok {
			inDegree[id] = 0
		}
	}

	for i := range slices {
		for _, dep := range slices[i].Dependencies {
			if _, ok := byID[dep]; ok {
				adj[dep] = append(adj[dep], slices[i].ID)
				inDegree[slices[i].ID]++
			}
		}
	}

	var queue []string
	for id, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, id)
		}
	}
	sort.Strings(queue)

	var sorted []Slice
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		sorted = append(sorted, *byID[id])

		neighbors := adj[id]
		sort.Strings(neighbors)
		for _, neighbor := range neighbors {
			inDegree[neighbor]--
			if inDegree[neighbor] == 0 {
				queue = append(queue, neighbor)
				sort.Strings(queue)
			}
		}
	}

	if len(sorted) != len(slices) {
		return nil, fmt.Errorf("circular dependency detected")
	}

	return sorted, nil
}

func GeneratePRD(problem, solution string, stories []UserStory) *PRD {
	return &PRD{
		ProblemStatement: problem,
		Solution:         solution,
		UserStories:      stories,
	}
}

func FormatSlice(slice Slice) string {
	var b strings.Builder

	fmt.Fprintf(&b, "## %s %s (%s)\n", slice.ID, slice.Title, slice.Classification)
	fmt.Fprintf(&b, "\n%s\n", strings.TrimSpace(slice.Description))

	if len(slice.Layers) > 0 {
		fmt.Fprintf(&b, "Layers: %s\n", strings.Join(slice.Layers, " → "))
	} else {
		fmt.Fprintf(&b, "Layers: none\n")
	}

	if len(slice.Dependencies) > 0 {
		fmt.Fprintf(&b, "Dependencies: %s\n", strings.Join(slice.Dependencies, ", "))
	} else {
		fmt.Fprintf(&b, "Dependencies: none\n")
	}

	fmt.Fprintf(&b, "Priority: %d\n", slice.Priority)

	return b.String()
}

func FormatPRD(prd *PRD) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Product Requirements Document\n\n")
	fmt.Fprintf(&b, "## Problem\n%s\n\n", prd.ProblemStatement)
	fmt.Fprintf(&b, "## Solution\n%s\n\n", prd.Solution)

	fmt.Fprintf(&b, "## User Stories\n")
	for _, us := range prd.UserStories {
		fmt.Fprintf(&b, "- As a %s, I want %s, so that %s\n", us.Actor, us.Want, us.Benefit)
	}

	if len(prd.ImplDecisions) > 0 {
		fmt.Fprintf(&b, "\n## Implementation Decisions\n")
		for _, d := range prd.ImplDecisions {
			fmt.Fprintf(&b, "- %s\n", d)
		}
	}

	if len(prd.TestDecisions) > 0 {
		fmt.Fprintf(&b, "\n## Test Decisions\n")
		for _, d := range prd.TestDecisions {
			fmt.Fprintf(&b, "- %s\n", d)
		}
	}

	if len(prd.OutOfScope) > 0 {
		fmt.Fprintf(&b, "\n## Out of Scope\n")
		for _, item := range prd.OutOfScope {
			fmt.Fprintf(&b, "- %s\n", item)
		}
	}

	return b.String()
}
