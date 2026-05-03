package pipeline

import (
	"strings"
	"testing"
)

func TestDecomposeIntoSlices_ProducesSlices(t *testing.T) {
	spec := `# Auth Module
Build authentication with JWT tokens and session management.

## User Registration
Create an API endpoint for user registration with email validation.

## Login Flow
Implement the login endpoint that returns JWT tokens after validation.
`

	slices, err := DecomposeIntoSlices(spec)
	if err != nil {
		t.Fatalf("DecomposeIntoSlices() error = %v", err)
	}

	if len(slices) < 2 {
		t.Fatalf("expected at least 2 slices, got %d", len(slices))
	}

	foundAuth := false
	foundReg := false
	foundLogin := false
	for _, s := range slices {
		if strings.Contains(strings.ToLower(s.Title), "auth") {
			foundAuth = true
		}
		if strings.Contains(strings.ToLower(s.Title), "registration") {
			foundReg = true
		}
		if strings.Contains(strings.ToLower(s.Title), "login") {
			foundLogin = true
		}
	}

	if !foundAuth {
		t.Error("expected slice with 'auth' in title")
	}
	if !foundReg {
		t.Error("expected slice with 'registration' in title")
	}
	if !foundLogin {
		t.Error("expected slice with 'login' in title")
	}
}

func TestDecomposeIntoSlices_NoHeaders(t *testing.T) {
	spec := `This is a plain spec with no headers at all.
It just describes a feature in plain text.
Nothing special about it.`

	slices, err := DecomposeIntoSlices(spec)
	if err != nil {
		t.Fatalf("DecomposeIntoSlices() error = %v", err)
	}

	if len(slices) != 1 {
		t.Fatalf("expected 1 slice for no-header spec, got %d", len(slices))
	}

	if slices[0].Title != "Implementation" {
		t.Errorf("expected title 'Implementation', got %q", slices[0].Title)
	}

	if slices[0].Priority != 1 {
		t.Errorf("expected priority 1, got %d", slices[0].Priority)
	}
}

func TestDecomposeIntoSlices_DetectsLayers(t *testing.T) {
	spec := `## API Layer
Create an API endpoint for handling requests.
Build the route handler with proper validation.
`

	slices, err := DecomposeIntoSlices(spec)
	if err != nil {
		t.Fatalf("DecomposeIntoSlices() error = %v", err)
	}

	if len(slices) == 0 {
		t.Fatal("expected at least one slice")
	}

	hasAPI := false
	for _, layer := range slices[0].Layers {
		if layer == "api" {
			hasAPI = true
		}
	}

	if !hasAPI {
		t.Errorf("expected 'api' layer, got layers: %v", slices[0].Layers)
	}
}

func TestDecomposeIntoSlices_ClassifiesHITL(t *testing.T) {
	spec := `## Approval Workflow
This feature requires user approval before proceeding.
The human reviewer must validate the submission.
`

	slices, err := DecomposeIntoSlices(spec)
	if err != nil {
		t.Fatalf("DecomposeIntoSlices() error = %v", err)
	}

	if len(slices) == 0 {
		t.Fatal("expected at least one slice")
	}

	if slices[0].Classification != ClassHITL {
		t.Errorf("expected HITL classification, got %q", slices[0].Classification)
	}
}

func TestDecomposeIntoSlices_ClassifiesAFK(t *testing.T) {
	spec := `## Background Processing
Process data in the background using a queue.
No manual intervention needed.
`

	slices, err := DecomposeIntoSlices(spec)
	if err != nil {
		t.Fatalf("DecomposeIntoSlices() error = %v", err)
	}

	if len(slices) == 0 {
		t.Fatal("expected at least one slice")
	}

	if slices[0].Classification != ClassAFK {
		t.Errorf("expected AFK classification, got %q", slices[0].Classification)
	}
}

func TestDecomposeIntoSlices_ExtractsDeps(t *testing.T) {
	spec := `## Database Setup
Create the schema and tables.

## API Layer
Build the API endpoint. Depends on database-setup.
`

	slices, err := DecomposeIntoSlices(spec)
	if err != nil {
		t.Fatalf("DecomposeIntoSlices() error = %v", err)
	}

	if len(slices) < 2 {
		t.Fatalf("expected at least 2 slices, got %d", len(slices))
	}

	apiSlice := slices[1]
	if !strings.Contains(strings.ToLower(apiSlice.Title), "api") {
		t.Fatalf("expected second slice to be API layer, got %q", apiSlice.Title)
	}

	found := false
	for _, dep := range apiSlice.Dependencies {
		if dep == "database-setup" {
			found = true
		}
	}

	if !found {
		t.Errorf("expected dependency on 'database-setup', got deps: %v", apiSlice.Dependencies)
	}
}

func TestDecomposeIntoSlices_SliceID(t *testing.T) {
	spec := `## Auth Module!!
Build auth.
`
	slices, err := DecomposeIntoSlices(spec)
	if err != nil {
		t.Fatalf("DecomposeIntoSlices() error = %v", err)
	}

	if len(slices) == 0 {
		t.Fatal("expected at least one slice")
	}

	expectedID := "auth-module"
	if slices[0].ID != expectedID {
		t.Errorf("expected ID %q, got %q", expectedID, slices[0].ID)
	}
}

func TestDecomposeIntoSlices_EmptySpec(t *testing.T) {
	_, err := DecomposeIntoSlices("")
	if err == nil {
		t.Fatal("expected error for empty spec")
	}
}

func TestDecomposeIntoSlices_Priorities(t *testing.T) {
	spec := `## First
Content one.

## Second
Content two.

## Third
Content three.
`

	slices, err := DecomposeIntoSlices(spec)
	if err != nil {
		t.Fatalf("DecomposeIntoSlices() error = %v", err)
	}

	if len(slices) != 3 {
		t.Fatalf("expected 3 slices, got %d", len(slices))
	}

	expected := []int{1, 2, 3}
	for i, s := range slices {
		if s.Priority != expected[i] {
			t.Errorf("slice %d: expected priority %d, got %d", i, expected[i], s.Priority)
		}
	}
}

func TestTopologicalSort_OrdersByDeps(t *testing.T) {
	slices := []Slice{
		{ID: "b", Title: "B", Dependencies: []string{"a"}},
		{ID: "a", Title: "A", Dependencies: []string{}},
		{ID: "c", Title: "C", Dependencies: []string{"b"}},
	}

	sorted, err := TopologicalSort(slices)
	if err != nil {
		t.Fatalf("TopologicalSort() error = %v", err)
	}

	if len(sorted) != 3 {
		t.Fatalf("expected 3 slices, got %d", len(sorted))
	}

	if sorted[0].ID != "a" {
		t.Errorf("expected first slice 'a', got %q", sorted[0].ID)
	}
	if sorted[1].ID != "b" {
		t.Errorf("expected second slice 'b', got %q", sorted[1].ID)
	}
	if sorted[2].ID != "c" {
		t.Errorf("expected third slice 'c', got %q", sorted[2].ID)
	}
}

func TestTopologicalSort_CycleDetection(t *testing.T) {
	slices := []Slice{
		{ID: "a", Title: "A", Dependencies: []string{"b"}},
		{ID: "b", Title: "B", Dependencies: []string{"c"}},
		{ID: "c", Title: "C", Dependencies: []string{"a"}},
	}

	_, err := TopologicalSort(slices)
	if err == nil {
		t.Fatal("expected error for circular dependency")
	}

	if !strings.Contains(err.Error(), "circular dependency") {
		t.Errorf("error should mention circular dependency, got: %v", err)
	}
}

func TestTopologicalSort_NoDeps(t *testing.T) {
	slices := []Slice{
		{ID: "x", Title: "X", Dependencies: []string{}},
		{ID: "y", Title: "Y", Dependencies: []string{}},
		{ID: "z", Title: "Z", Dependencies: []string{}},
	}

	sorted, err := TopologicalSort(slices)
	if err != nil {
		t.Fatalf("TopologicalSort() error = %v", err)
	}

	if len(sorted) != 3 {
		t.Fatalf("expected 3 slices, got %d", len(sorted))
	}

	ids := make(map[string]bool)
	for _, s := range sorted {
		ids[s.ID] = true
	}

	for _, expected := range []string{"x", "y", "z"} {
		if !ids[expected] {
			t.Errorf("expected slice %q in result", expected)
		}
	}
}

func TestTopologicalSort_Empty(t *testing.T) {
	sorted, err := TopologicalSort(nil)
	if err != nil {
		t.Fatalf("TopologicalSort() error = %v", err)
	}

	if sorted != nil {
		t.Errorf("expected nil for empty input, got %v", sorted)
	}
}

func TestTopologicalSort_SingleSlice(t *testing.T) {
	slices := []Slice{
		{ID: "only", Title: "Only", Dependencies: []string{}},
	}

	sorted, err := TopologicalSort(slices)
	if err != nil {
		t.Fatalf("TopologicalSort() error = %v", err)
	}

	if len(sorted) != 1 {
		t.Fatalf("expected 1 slice, got %d", len(sorted))
	}

	if sorted[0].ID != "only" {
		t.Errorf("expected 'only', got %q", sorted[0].ID)
	}
}

func TestTopologicalSort_MissingDepIgnored(t *testing.T) {
	slices := []Slice{
		{ID: "a", Title: "A", Dependencies: []string{"nonexistent"}},
	}

	sorted, err := TopologicalSort(slices)
	if err != nil {
		t.Fatalf("TopologicalSort() error = %v", err)
	}

	if len(sorted) != 1 {
		t.Fatalf("expected 1 slice, got %d", len(sorted))
	}

	if sorted[0].ID != "a" {
		t.Errorf("expected 'a', got %q", sorted[0].ID)
	}
}

func TestGeneratePRD_Complete(t *testing.T) {
	stories := []UserStory{
		{Actor: "developer", Want: "auto-formatting", Benefit: "consistent code"},
		{Actor: "reviewer", Want: "diff previews", Benefit: "faster reviews"},
	}

	prd := GeneratePRD(
		"Code quality is inconsistent",
		"Automated formatting pipeline",
		stories,
	)

	if prd.ProblemStatement != "Code quality is inconsistent" {
		t.Errorf("problem = %q, want 'Code quality is inconsistent'", prd.ProblemStatement)
	}
	if prd.Solution != "Automated formatting pipeline" {
		t.Errorf("solution = %q, want 'Automated formatting pipeline'", prd.Solution)
	}
	if len(prd.UserStories) != 2 {
		t.Fatalf("expected 2 stories, got %d", len(prd.UserStories))
	}
	if prd.UserStories[0].Actor != "developer" {
		t.Errorf("story 0 actor = %q, want 'developer'", prd.UserStories[0].Actor)
	}
	if prd.UserStories[1].Benefit != "faster reviews" {
		t.Errorf("story 1 benefit = %q, want 'faster reviews'", prd.UserStories[1].Benefit)
	}
}

func TestGeneratePRD_EmptyStories(t *testing.T) {
	prd := GeneratePRD("problem", "solution", nil)
	if prd.ProblemStatement != "problem" {
		t.Errorf("problem = %q, want 'problem'", prd.ProblemStatement)
	}
	if prd.UserStories != nil {
		t.Errorf("expected nil stories, got %v", prd.UserStories)
	}
}

func TestFormatSlice_Readable(t *testing.T) {
	s := Slice{
		ID:             "auth-module",
		Title:          "Auth Module",
		Description:    "Build the authentication system",
		Layers:         []string{"schema", "api", "ui"},
		Classification: ClassHITL,
		Dependencies:   []string{"user-model"},
		Priority:       1,
	}

	out := FormatSlice(s)

	checks := []string{
		"auth-module",
		"Auth Module",
		"HITL",
		"Build the authentication system",
		"schema → api → ui",
		"user-model",
		"Priority: 1",
	}

	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("FormatSlice() output missing %q\nGot:\n%s", want, out)
		}
	}
}

func TestFormatSlice_NoLayers(t *testing.T) {
	s := Slice{
		ID:             "simple",
		Title:          "Simple",
		Description:    "No layers here",
		Layers:         nil,
		Classification: ClassAFK,
		Dependencies:   nil,
		Priority:       2,
	}

	out := FormatSlice(s)

	if !strings.Contains(out, "Layers: none") {
		t.Errorf("expected 'Layers: none', got:\n%s", out)
	}
	if !strings.Contains(out, "Dependencies: none") {
		t.Errorf("expected 'Dependencies: none', got:\n%s", out)
	}
}

func TestFormatPRD_Readable(t *testing.T) {
	prd := &PRD{
		ProblemStatement: "Slow deployments",
		Solution:         "Parallel build system",
		UserStories: []UserStory{
			{Actor: "devops", Want: "faster deploys", Benefit: "shorter feedback loops"},
		},
		ImplDecisions:  []string{"Use Go pipelines", "BadgerDB for state"},
		TestDecisions:  []string{"Integration tests for each stage"},
		OutOfScope:     []string{"Kubernetes integration"},
	}

	out := FormatPRD(prd)

	checks := []string{
		"# Product Requirements Document",
		"## Problem\nSlow deployments",
		"## Solution\nParallel build system",
		"## User Stories",
		"- As a devops, I want faster deploys, so that shorter feedback loops",
		"## Implementation Decisions",
		"- Use Go pipelines",
		"- BadgerDB for state",
		"## Test Decisions",
		"- Integration tests for each stage",
		"## Out of Scope",
		"- Kubernetes integration",
	}

	for _, want := range checks {
		if !strings.Contains(out, want) {
			t.Errorf("FormatPRD() output missing %q\nGot:\n%s", want, out)
		}
	}
}

func TestFormatPRD_Minimal(t *testing.T) {
	prd := &PRD{
		ProblemStatement: "bug",
		Solution:         "fix it",
		UserStories:      nil,
	}

	out := FormatPRD(prd)

	if !strings.Contains(out, "## Problem\nbug") {
		t.Errorf("expected problem section, got:\n%s", out)
	}
	if strings.Contains(out, "## Implementation Decisions") {
		t.Errorf("should not include empty implementation decisions, got:\n%s", out)
	}
	if strings.Contains(out, "## Out of Scope") {
		t.Errorf("should not include empty out of scope, got:\n%s", out)
	}
}

func TestSliceID_SpecialChars(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Auth Module", "auth-module"},
		{"  Spaces  Around  ", "spaces-around"},
		{"Foo!!!Bar###Baz", "foo-bar-baz"},
		{"UPPER CASE", "upper-case"},
		{"already-lower", "already-lower"},
		{"Mix3d_Cas3", "mix3d-cas3"},
	}

	for _, tt := range tests {
		got := sliceID(tt.input)
		if got != tt.expected {
			t.Errorf("sliceID(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestDecomposeIntoSlices_MultipleLayers(t *testing.T) {
	spec := `## Full Stack Feature
Implement the database schema, API endpoint, and frontend UI component.
Include test coverage for all layers.
`

	slices, err := DecomposeIntoSlices(spec)
	if err != nil {
		t.Fatalf("DecomposeIntoSlices() error = %v", err)
	}

	if len(slices) == 0 {
		t.Fatal("expected at least one slice")
	}

	expectedLayers := map[string]bool{"schema": false, "api": false, "ui": false, "tests": false}
	for _, layer := range slices[0].Layers {
		if _, ok := expectedLayers[layer]; ok {
			expectedLayers[layer] = true
		}
	}

	for layer, found := range expectedLayers {
		if !found {
			t.Errorf("expected layer %q not detected, got layers: %v", layer, slices[0].Layers)
		}
	}
}
