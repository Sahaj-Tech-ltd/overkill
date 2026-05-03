package skills

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

const validSkillMD = `---
name: code-review
version: 1.0.0
description: Code quality review with actionable feedback for developers
author: ethos-team
category: review
tags: [code, review, quality]
triggers: [review, "code review", critique]
enabled: true
---

# Code Review Skill

## Instructions
When reviewing code, focus on correctness and performance.
`

const validSkillMD2 = `---
name: test-helper
version: 2.1.0
description: A helpful testing utility for writing better test suites
author: test-author
category: testing
tags: [test, helper, utility]
triggers: [test, "test helper"]
enabled: false
---

# Test Helper

This skill helps you write better tests with more coverage and confidence.
`

const noFrontmatterMD = `# No Frontmatter

This is just a plain markdown file with no frontmatter at all.
`

const missingNameMD = `---
description: Some description that is long enough to pass validation
version: 1.0.0
---

# Missing Name

This skill has instructions but the name field is missing from frontmatter.
`

const missingDescMD = `---
name: some-skill
version: 1.0.0
---

# Missing Description

This skill has no description field at all in the frontmatter section.
`

const emptyBodyMD = `---
name: empty-body
description: A skill with an empty body that should fail validation
---

`

const fullFrontmatterMD = `---
name: full-skill
version: 3.2.1
description: A comprehensive skill with every possible field populated
author: full-author
category: general
tags: [full, comprehensive, example]
triggers: [full, comprehensive, "full skill"]
enabled: true
custom_field: custom_value
another_field: another_value
---

# Full Frontmatter Skill

This is the full instruction body for the comprehensive skill test case.
`

func writeTestSkill(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	err := os.WriteFile(path, []byte(content), 0644)
	require.NoError(t, err)
	return path
}

func TestLoader_LoadFile_Valid(t *testing.T) {
	dir := t.TempDir()
	path := writeTestSkill(t, dir, "skill.md", validSkillMD)

	loader := NewLoader("", "")
	skill, err := loader.LoadFile(path)
	require.NoError(t, err)
	require.NotNil(t, skill)
	require.Equal(t, "code-review", skill.Name)
	require.Equal(t, "1.0.0", skill.Version)
	require.Equal(t, "Code quality review with actionable feedback for developers", skill.Description)
	require.Equal(t, "ethos-team", skill.Author)
	require.Equal(t, "review", skill.Category)
	require.Equal(t, []string{"code", "review", "quality"}, skill.Tags)
	require.Equal(t, []string{"review", "code review", "critique"}, skill.Triggers)
	require.Contains(t, skill.Instructions, "Code Review Skill")
	require.Equal(t, path, skill.FilePath)
	require.False(t, skill.CreatedAt.IsZero())
	require.False(t, skill.UpdatedAt.IsZero())
}

func TestLoader_LoadFile_NoFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := writeTestSkill(t, dir, "skill.md", noFrontmatterMD)

	loader := NewLoader("", "")
	_, err := loader.LoadFile(path)
	require.Error(t, err)
	require.Contains(t, err.Error(), "frontmatter")
}

func TestLoader_LoadFile_MissingName(t *testing.T) {
	dir := t.TempDir()
	path := writeTestSkill(t, dir, "skill.md", missingNameMD)

	loader := NewLoader("", "")
	skill, err := loader.LoadFile(path)
	require.NoError(t, err)
	require.Empty(t, skill.Name)
}

func TestLoader_LoadFile_MissingDescription(t *testing.T) {
	dir := t.TempDir()
	path := writeTestSkill(t, dir, "skill.md", missingDescMD)

	loader := NewLoader("", "")
	skill, err := loader.LoadFile(path)
	require.NoError(t, err)
	require.Empty(t, skill.Description)
}

func TestLoader_LoadFile_FullFrontmatter(t *testing.T) {
	dir := t.TempDir()
	path := writeTestSkill(t, dir, "skill.md", fullFrontmatterMD)

	loader := NewLoader("", "")
	skill, err := loader.LoadFile(path)
	require.NoError(t, err)
	require.Equal(t, "full-skill", skill.Name)
	require.Equal(t, "3.2.1", skill.Version)
	require.Equal(t, "A comprehensive skill with every possible field populated", skill.Description)
	require.Equal(t, "full-author", skill.Author)
	require.Equal(t, "general", skill.Category)
	require.Equal(t, []string{"full", "comprehensive", "example"}, skill.Tags)
	require.Equal(t, []string{"full", "comprehensive", "full skill"}, skill.Triggers)
	require.Contains(t, skill.Instructions, "Full Frontmatter Skill")
	require.Contains(t, skill.Metadata["custom_field"], "custom_value")
	require.Contains(t, skill.Metadata["another_field"], "another_value")
}

func TestLoader_LoadFile_EmptyBody(t *testing.T) {
	dir := t.TempDir()
	path := writeTestSkill(t, dir, "skill.md", emptyBodyMD)

	loader := NewLoader("", "")
	skill, err := loader.LoadFile(path)
	require.NoError(t, err)
	require.Empty(t, strings.TrimSpace(skill.Instructions))
}

func TestLoader_LoadDir(t *testing.T) {
	dir := t.TempDir()
	writeTestSkill(t, dir, "skill1.md", validSkillMD)
	writeTestSkill(t, dir, "skill2.md", validSkillMD2)

	loader := NewLoader("", "")
	skills, err := loader.LoadDir(dir)
	require.NoError(t, err)
	require.Len(t, skills, 2)

	names := make(map[string]bool)
	for _, s := range skills {
		names[s.Name] = true
	}
	require.True(t, names["code-review"])
	require.True(t, names["test-helper"])
}

func TestLoader_LoadDir_SkipsNonSkillFiles(t *testing.T) {
	dir := t.TempDir()
	writeTestSkill(t, dir, "skill1.md", validSkillMD)
	writeTestSkill(t, dir, "readme.txt", "not a skill")
	writeTestSkill(t, dir, "main.go", "package main")

	loader := NewLoader("", "")
	skills, err := loader.LoadDir(dir)
	require.NoError(t, err)
	require.Len(t, skills, 1)
	require.Equal(t, "code-review", skills[0].Name)
}

func TestRegistry_Register(t *testing.T) {
	reg := NewRegistry()
	skill := &Skill{
		Name:         "test-skill",
		Description:  "A test skill description that is long enough",
		Instructions: "These are instructions that are at least twenty characters long",
		Enabled:      true,
	}

	err := reg.Register(skill)
	require.NoError(t, err)

	got, ok := reg.Get("test-skill")
	require.True(t, ok)
	require.Equal(t, "test-skill", got.Name)
}

func TestRegistry_Register_Duplicate(t *testing.T) {
	reg := NewRegistry()
	skill := &Skill{
		Name:         "dup-skill",
		Description:  "A duplicate skill description that is long enough",
		Instructions: "These are instructions that are at least twenty characters long",
	}

	err := reg.Register(skill)
	require.NoError(t, err)

	err = reg.Register(skill)
	require.Error(t, err)
	require.Contains(t, err.Error(), "already registered")
}

func TestRegistry_Unregister(t *testing.T) {
	reg := NewRegistry()
	skill := &Skill{
		Name:         "remove-me",
		Description:  "A skill to be removed that is long enough text",
		Instructions: "These are instructions that are at least twenty characters long",
	}

	err := reg.Register(skill)
	require.NoError(t, err)

	removed := reg.Unregister("remove-me")
	require.True(t, removed)

	_, ok := reg.Get("remove-me")
	require.False(t, ok)
}

func TestRegistry_Match(t *testing.T) {
	reg := NewRegistry()
	skill := &Skill{
		Name:         "review-skill",
		Description:  "A review skill description that is long enough",
		Instructions: "These are instructions that are at least twenty characters long",
		Triggers:     []string{"review", "code review"},
	}

	err := reg.Register(skill)
	require.NoError(t, err)

	matches := reg.Match("I want a code review of my changes")
	require.Len(t, matches, 1)
	require.Equal(t, "review-skill", matches[0].Name)
}

func TestRegistry_Match_CaseInsensitive(t *testing.T) {
	reg := NewRegistry()
	skill := &Skill{
		Name:         "review-skill",
		Description:  "A review skill description that is long enough",
		Instructions: "These are instructions that are at least twenty characters long",
		Triggers:     []string{"review"},
	}

	err := reg.Register(skill)
	require.NoError(t, err)

	matches := reg.Match("I need a REVIEW of my code")
	require.Len(t, matches, 1)
	require.Equal(t, "review-skill", matches[0].Name)
}

func TestRegistry_Match_NoMatch(t *testing.T) {
	reg := NewRegistry()
	skill := &Skill{
		Name:         "review-skill",
		Description:  "A review skill description that is long enough",
		Instructions: "These are instructions that are at least twenty characters long",
		Triggers:     []string{"review"},
	}

	err := reg.Register(skill)
	require.NoError(t, err)

	matches := reg.Match("deploy my application to production")
	require.Len(t, matches, 0)
}

func TestRegistry_Search(t *testing.T) {
	reg := NewRegistry()
	skill1 := &Skill{
		Name:         "code-review",
		Description:  "Code quality review with actionable feedback for devs",
		Instructions: "These are instructions that are at least twenty characters long",
		Tags:         []string{"review", "quality"},
	}
	skill2 := &Skill{
		Name:         "deploy-helper",
		Description:  "Helps deploy applications to various cloud providers",
		Instructions: "These are instructions that are at least twenty characters long",
		Tags:         []string{"deploy", "cloud"},
	}

	err := reg.Register(skill1)
	require.NoError(t, err)
	err = reg.Register(skill2)
	require.NoError(t, err)

	results := reg.Search("review")
	require.Len(t, results, 1)
	require.Equal(t, "code-review", results[0].Name)

	results = reg.Search("deploy")
	require.Len(t, results, 1)
	require.Equal(t, "deploy-helper", results[0].Name)

	results = reg.Search("cloud")
	require.Len(t, results, 1)
	require.Equal(t, "deploy-helper", results[0].Name)
}

func TestRegistry_EnableDisable(t *testing.T) {
	reg := NewRegistry()
	skill := &Skill{
		Name:         "toggle-skill",
		Description:  "A skill for testing enable disable functionality",
		Instructions: "These are instructions that are at least twenty characters long",
		Enabled:      true,
	}

	err := reg.Register(skill)
	require.NoError(t, err)

	err = reg.Disable("toggle-skill")
	require.NoError(t, err)

	got, ok := reg.Get("toggle-skill")
	require.True(t, ok)
	require.False(t, got.Enabled)

	err = reg.Enable("toggle-skill")
	require.NoError(t, err)

	got, ok = reg.Get("toggle-skill")
	require.True(t, ok)
	require.True(t, got.Enabled)
}

func TestRegistry_Enable_NotFound(t *testing.T) {
	reg := NewRegistry()
	err := reg.Enable("nonexistent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestRegistry_Disable_NotFound(t *testing.T) {
	reg := NewRegistry()
	err := reg.Disable("nonexistent")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestRegistry_ListByCategory(t *testing.T) {
	reg := NewRegistry()

	skill1 := &Skill{
		Name:         "review-one",
		Description:  "First review skill description text here",
		Category:     "review",
		Instructions: "These are instructions that are at least twenty characters long",
	}
	skill2 := &Skill{
		Name:         "deploy-one",
		Description:  "First deploy skill description text here",
		Category:     "deploy",
		Instructions: "These are instructions that are at least twenty characters long",
	}
	skill3 := &Skill{
		Name:         "review-two",
		Description:  "Second review skill description text here",
		Category:     "review",
		Instructions: "These are instructions that are at least twenty characters long",
	}

	err := reg.Register(skill1)
	require.NoError(t, err)
	err = reg.Register(skill2)
	require.NoError(t, err)
	err = reg.Register(skill3)
	require.NoError(t, err)

	reviewSkills := reg.ListByCategory("review")
	require.Len(t, reviewSkills, 2)

	deploySkills := reg.ListByCategory("deploy")
	require.Len(t, deploySkills, 1)

	empty := reg.ListByCategory("nonexistent")
	require.Len(t, empty, 0)
}

func TestRegistry_ListByTag(t *testing.T) {
	reg := NewRegistry()

	skill1 := &Skill{
		Name:         "tagged-one",
		Description:  "First tagged skill description text here now",
		Tags:         []string{"go", "test"},
		Instructions: "These are instructions that are at least twenty characters long",
	}
	skill2 := &Skill{
		Name:         "tagged-two",
		Description:  "Second tagged skill description text here now",
		Tags:         []string{"python", "test"},
		Instructions: "These are instructions that are at least twenty characters long",
	}
	skill3 := &Skill{
		Name:         "tagged-three",
		Description:  "Third tagged skill description text here now",
		Tags:         []string{"rust"},
		Instructions: "These are instructions that are at least twenty characters long",
	}

	err := reg.Register(skill1)
	require.NoError(t, err)
	err = reg.Register(skill2)
	require.NoError(t, err)
	err = reg.Register(skill3)
	require.NoError(t, err)

	testSkills := reg.ListByTag("test")
	require.Len(t, testSkills, 2)

	goSkills := reg.ListByTag("go")
	require.Len(t, goSkills, 1)
	require.Equal(t, "tagged-one", goSkills[0].Name)
}

func TestRegistry_Count(t *testing.T) {
	reg := NewRegistry()
	require.Equal(t, 0, reg.Count())

	skill := &Skill{
		Name:         "count-skill",
		Description:  "A skill for testing count functionality here",
		Instructions: "These are instructions that are at least twenty characters long",
	}

	err := reg.Register(skill)
	require.NoError(t, err)
	require.Equal(t, 1, reg.Count())
}

func TestRegistry_CountByCategory(t *testing.T) {
	reg := NewRegistry()

	skill1 := &Skill{
		Name:         "cat-a1",
		Description:  "Category A first skill description text here",
		Category:     "alpha",
		Instructions: "These are instructions that are at least twenty characters long",
	}
	skill2 := &Skill{
		Name:         "cat-a2",
		Description:  "Category A second skill description text here",
		Category:     "alpha",
		Instructions: "These are instructions that are at least twenty characters long",
	}
	skill3 := &Skill{
		Name:         "cat-b1",
		Description:  "Category B first skill description text here",
		Category:     "beta",
		Instructions: "These are instructions that are at least twenty characters long",
	}

	err := reg.Register(skill1)
	require.NoError(t, err)
	err = reg.Register(skill2)
	require.NoError(t, err)
	err = reg.Register(skill3)
	require.NoError(t, err)

	counts := reg.CountByCategory()
	require.Equal(t, 2, counts["alpha"])
	require.Equal(t, 1, counts["beta"])
}

func TestValidate_ValidSkill(t *testing.T) {
	skill := &Skill{
		Name:         "valid-skill",
		Description:  "A valid skill description for testing purposes",
		Version:      "1.0.0",
		Instructions: "These are instructions that are at least twenty characters long",
		Triggers:     []string{"valid", "test"},
	}

	errs := Validate(skill)
	require.Len(t, errs, 0)
}

func TestValidate_MissingName(t *testing.T) {
	skill := &Skill{
		Description:  "A valid description for testing validation logic",
		Instructions: "These are instructions that are at least twenty characters long",
	}

	errs := Validate(skill)
	require.True(t, len(errs) > 0)
	found := false
	for _, e := range errs {
		if e.Field == "name" {
			found = true
		}
	}
	require.True(t, found)
}

func TestValidate_InvalidName(t *testing.T) {
	tests := []struct {
		name      string
		skillName string
		expectErr bool
	}{
		{"spaces", "has spaces", true},
		{"uppercase", "Uppercase", true},
		{"too short", "a", true},
		{"too long", strings.Repeat("a", 51), true},
		{"valid", "my-valid-skill123", false},
		{"single char too short", "ab", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			skill := &Skill{
				Name:         tt.skillName,
				Description:  "A valid description for testing validation logic",
				Instructions: "These are instructions that are at least twenty characters long",
			}
			errs := Validate(skill)
			hasNameErr := false
			for _, e := range errs {
				if e.Field == "name" {
					hasNameErr = true
				}
			}
			if tt.expectErr {
				require.True(t, hasNameErr, "expected name error for %q", tt.skillName)
			} else {
				require.False(t, hasNameErr, "unexpected name error for %q", tt.skillName)
			}
		})
	}
}

func TestValidate_MissingDescription(t *testing.T) {
	skill := &Skill{
		Name:         "some-skill",
		Instructions: "These are instructions that are at least twenty characters long",
	}

	errs := Validate(skill)
	found := false
	for _, e := range errs {
		if e.Field == "description" {
			found = true
		}
	}
	require.True(t, found)
}

func TestValidate_InvalidVersion(t *testing.T) {
	skill := &Skill{
		Name:         "versioned-skill",
		Description:  "A valid description for testing version validation",
		Version:      "not-semver",
		Instructions: "These are instructions that are at least twenty characters long",
	}

	errs := Validate(skill)
	found := false
	for _, e := range errs {
		if e.Field == "version" {
			found = true
		}
	}
	require.True(t, found)
}

func TestRegistry_ConcurrentAccess(t *testing.T) {
	reg := NewRegistry()
	var wg sync.WaitGroup

	for i := range 100 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			name := strings.Repeat("a", 2) + "-" + strings.Repeat("b", 2)
			if idx < 50 {
				_ = idx
			}
			suffix := strings.Repeat("x", idx%10) + "skill"
			if len(suffix) > 47 {
				suffix = suffix[:47]
			}
			name = "skill-" + suffix
			if len(name) < 2 {
				name = "ab"
			}
			if len(name) > 50 {
				name = name[:50]
			}

			skill := &Skill{
				Name:         name,
				Description:  "Concurrent skill description that is long enough for validation",
				Instructions: "These are instructions that are at least twenty characters long",
				Triggers:     []string{name},
			}

			_ = reg.Register(skill)
			reg.Get(name)
			reg.Match("test query")
			reg.Search(name)
			reg.Count()
		}(i)
	}

	wg.Wait()

	count := reg.Count()
	require.True(t, count > 0)
}

func TestLoader_LoadAll(t *testing.T) {
	bundledDir := t.TempDir()
	userDir := t.TempDir()

	writeTestSkill(t, bundledDir, "bundled.md", validSkillMD)
	writeTestSkill(t, userDir, "user.md", validSkillMD2)

	loader := NewLoader(bundledDir, userDir)
	skills, err := loader.LoadAll()
	require.NoError(t, err)
	require.Len(t, skills, 2)

	bundledCount := 0
	userCount := 0
	for _, s := range skills {
		if s.Bundled {
			bundledCount++
		} else {
			userCount++
		}
	}
	require.Equal(t, 1, bundledCount)
	require.Equal(t, 1, userCount)
}

func TestLoader_LoadDir_EmptyDir(t *testing.T) {
	dir := t.TempDir()

	loader := NewLoader("", "")
	skills, err := loader.LoadDir(dir)
	require.NoError(t, err)
	require.Len(t, skills, 0)
}

func TestLoader_LoadFile_Nonexistent(t *testing.T) {
	loader := NewLoader("", "")
	_, err := loader.LoadFile("/nonexistent/skill.md")
	require.Error(t, err)
}

func TestRegistry_Register_Nil(t *testing.T) {
	reg := NewRegistry()
	err := reg.Register(nil)
	require.Error(t, err)
}

func TestRegistry_Get_NotFound(t *testing.T) {
	reg := NewRegistry()
	_, ok := reg.Get("nonexistent")
	require.False(t, ok)
}

func TestRegistry_Unregister_NotFound(t *testing.T) {
	reg := NewRegistry()
	removed := reg.Unregister("nonexistent")
	require.False(t, removed)
}

func TestRegistry_Get_CaseInsensitive(t *testing.T) {
	reg := NewRegistry()
	skill := &Skill{
		Name:         "case-test",
		Description:  "A skill for testing case insensitive lookups",
		Instructions: "These are instructions that are at least twenty characters long",
	}

	err := reg.Register(skill)
	require.NoError(t, err)

	got, ok := reg.Get("Case-Test")
	require.True(t, ok)
	require.Equal(t, "case-test", got.Name)

	got, ok = reg.Get("CASE-TEST")
	require.True(t, ok)
	require.Equal(t, "case-test", got.Name)
}

func TestValidate_ShortDescription(t *testing.T) {
	skill := &Skill{
		Name:         "short-desc",
		Description:  "too short",
		Instructions: "These are instructions that are at least twenty characters long",
	}

	errs := Validate(skill)
	found := false
	for _, e := range errs {
		if e.Field == "description" {
			found = true
		}
	}
	require.True(t, found)
}

func TestValidate_ShortInstructions(t *testing.T) {
	skill := &Skill{
		Name:         "short-inst",
		Description:  "A valid description for testing instruction validation",
		Instructions: "too short",
	}

	errs := Validate(skill)
	found := false
	for _, e := range errs {
		if e.Field == "instructions" {
			found = true
		}
	}
	require.True(t, found)
}

func TestValidate_EmptyVersion(t *testing.T) {
	skill := &Skill{
		Name:         "no-version",
		Description:  "A valid description for testing empty version validation",
		Version:      "",
		Instructions: "These are instructions that are at least twenty characters long",
	}

	errs := Validate(skill)
	for _, e := range errs {
		require.NotEqual(t, "version", e.Field, "empty version should not error")
	}
}

func TestLoader_LoadFile_ModTime(t *testing.T) {
	dir := t.TempDir()
	path := writeTestSkill(t, dir, "skill.md", validSkillMD)

	loader := NewLoader("", "")
	skill, err := loader.LoadFile(path)
	require.NoError(t, err)
	require.False(t, skill.CreatedAt.IsZero())
	require.False(t, skill.UpdatedAt.IsZero())
}
