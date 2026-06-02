package agent

import (
	"strings"
	"testing"
)

func TestSpecDriver_SimpleQuery(t *testing.T) {
	sd := NewSpecDriver()
	if sd.ShouldSpec("what is 2+2") {
		t.Error("ShouldSpec() = true for simple query, want false")
	}
}

func TestSpecDriver_ShortRequest(t *testing.T) {
	sd := NewSpecDriver()
	if sd.ShouldSpec("fix the typo") {
		t.Error("ShouldSpec() = true for short request, want false")
	}
}

func TestSpecDriver_ComplexMultiStep(t *testing.T) {
	sd := NewSpecDriver()
	input := "I need you to refactor the entire authentication module and then migrate the user database to a new schema, also update all the tests and after that update the documentation for every endpoint in the complete system with full integration coverage across the entire architecture pipeline. This is a large task that requires careful planning and execution of multiple steps in sequence. Please make sure to handle all the edge cases and verify everything works correctly before moving on to the next step."
	if !sd.ShouldSpec(input) {
		t.Error("ShouldSpec() = false for complex multi-step task, want true")
	}
}

func TestSpecDriver_RefactoringWithArchTerms(t *testing.T) {
	sd := NewSpecDriver()
	input := "Rewrite the pipeline architecture to use an event-driven system with full integration testing across the module boundaries, also refactor the complete codebase and ensure every component is updated properly. The pipeline should handle all messages correctly and the architecture must support the full system integration end-to-end across every single module in the entire project without any exceptions or omissions"
	if !sd.ShouldSpec(input) {
		t.Error("ShouldSpec() = false for refactoring with architectural terms, want true")
	}
}

func TestSpecDriver_BuildSpecPrompt_ContainsSections(t *testing.T) {
	sd := NewSpecDriver()
	prompt := sd.BuildSpecPrompt("do something complex")

	required := []string{
		"## Problem",
		"## Approach",
		"## Files to Modify",
		"## Test Plan",
		"## Edge Cases",
		"After creating the plan, execute it step by step.",
	}
	for _, section := range required {
		if !strings.Contains(prompt, section) {
			t.Errorf("BuildSpecPrompt() missing section %q", section)
		}
	}
}

func TestSpecDriver_BuildSpecPrompt_IncludesUserInput(t *testing.T) {
	sd := NewSpecDriver()
	userInput := "migrate the complete database system"
	prompt := sd.BuildSpecPrompt(userInput)
	if !strings.Contains(prompt, userInput) {
		t.Error("BuildSpecPrompt() does not include original user input at the end")
	}
}

func TestSpecDriver_SetEnabledFalse(t *testing.T) {
	sd := NewSpecDriver()
	sd.SetEnabled(false)
	input := "I need you to refactor the entire authentication module and then migrate the user database to a new schema, also update all the tests and after that update the documentation for every endpoint"
	if sd.ShouldSpec(input) {
		t.Error("ShouldSpec() = true after SetEnabled(false), want false")
	}
	if sd.IsEnabled() {
		t.Error("IsEnabled() = true after SetEnabled(false), want false")
	}
}

func TestSpecDriver_CustomThreshold(t *testing.T) {
	sd := NewSpecDriver()
	sd.SetThreshold(0.0)
	if !sd.ShouldSpec("fix the typo") {
		t.Error("ShouldSpec() = false with zero threshold, want true")
	}
}

func TestSpecDriver_CodeBlockBoostsScore(t *testing.T) {
	sd := NewSpecDriver()
	sd.SetThreshold(0.1)
	input := "here is my code ```go\nfmt.Println(\"hello\")\n``` fix this bug"
	if !sd.ShouldSpec(input) {
		t.Error("ShouldSpec() = false for input with code block, want true")
	}
}

func TestSpecDriver_DefaultEnabled(t *testing.T) {
	sd := NewSpecDriver()
	if !sd.IsEnabled() {
		t.Error("NewSpecDriver().IsEnabled() = false, want true by default")
	}
}

func TestSpecDriver_DefaultThreshold(t *testing.T) {
	sd := NewSpecDriver()
	input := "this is a moderately long sentence that has more than fifty words in it but does not contain any multi-step indicators or architectural terms or scope words so the score should be just the word count bonus of zero point three which is below the default threshold"
	if sd.ShouldSpec(input) {
		t.Error("ShouldSpec() = true for input scoring exactly 0.3, which is below 0.7 threshold")
	}
}
