package agent

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
)

type RecoveryReport struct {
	WhatWentWrong string
	RootCause     string
	FaultChain    []string
	LearningPlan  string
	ImmediateFix  string
	Apology       string
}

type JournalEntryWriter interface {
	WriteEntry(entryType string, content string) error
}

type ErrorRecovery struct {
	writer JournalEntryWriter
}

func NewErrorRecovery(writer JournalEntryWriter) *ErrorRecovery {
	return &ErrorRecovery{writer: writer}
}

func (er *ErrorRecovery) Analyze(err error, history []providers.Message) *RecoveryReport {
	errMsg := err.Error()
	rootCause := extractRootCause(errMsg)

	faultChain := buildFaultChain(history, errMsg)
	learningPlan := extractLearningPlan(rootCause)
	immediateFix := "Retry with corrected inputs"
	apology := fmt.Sprintf(
		"Here's what went wrong: %s. Root cause: %s. I'll %s to avoid this. Let me fix this now: %s",
		errMsg, rootCause, learningPlan, immediateFix,
	)

	return &RecoveryReport{
		WhatWentWrong: errMsg,
		RootCause:     rootCause,
		FaultChain:    faultChain,
		LearningPlan:  learningPlan,
		ImmediateFix:  immediateFix,
		Apology:       apology,
	}
}

func (er *ErrorRecovery) FormatRecovery(report *RecoveryReport) string {
	var b strings.Builder

	b.WriteString("I made a mistake. Here's what happened:\n\n")
	b.WriteString(fmt.Sprintf("What went wrong: %s\n", report.WhatWentWrong))
	b.WriteString(fmt.Sprintf("Root cause: %s\n", report.RootCause))
	b.WriteString("\nTrace:\n")
	for _, entry := range report.FaultChain {
		b.WriteString(fmt.Sprintf("- %s\n", entry))
	}
	b.WriteString(fmt.Sprintf("\nMy plan to not repeat this: %s\n", report.LearningPlan))
	b.WriteString(fmt.Sprintf("What I can do right now: %s\n", report.ImmediateFix))
	b.WriteString(fmt.Sprintf("\n%s", report.Apology))

	return b.String()
}

func (er *ErrorRecovery) RecordLesson(report *RecoveryReport) error {
	if er.writer == nil {
		return nil
	}

	payload := map[string]string{
		"WhatWentWrong": report.WhatWentWrong,
		"RootCause":     report.RootCause,
		"LearningPlan":  report.LearningPlan,
	}
	content, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("recovery: marshal lesson: %w", err)
	}

	return er.writer.WriteEntry("error_lesson", string(content))
}

func extractRootCause(errMsg string) string {
	lower := strings.ToLower(errMsg)

	if strings.Contains(lower, "not found") {
		return "Resource or file does not exist"
	}
	if strings.Contains(lower, "permission denied") {
		return "Insufficient permissions"
	}
	if strings.Contains(lower, "timeout") {
		return "Operation timed out"
	}
	if strings.Contains(lower, "context cancelled") {
		return "Operation was cancelled by user or timeout"
	}
	if strings.Contains(lower, "tool") && strings.Contains(lower, "failed") {
		return "Tool execution error"
	}

	return "Unknown — error: " + errMsg
}

func extractLearningPlan(rootCause string) string {
	switch rootCause {
	case "Resource or file does not exist":
		return "Verify file paths exist before operating on them"
	case "Insufficient permissions":
		return "Check permissions before executing commands"
	case "Operation timed out":
		return "Add timeout handling and retry logic"
	case "Operation was cancelled by user or timeout":
		return "Respect context cancellation and clean up"
	case "Tool execution error":
		return "Validate tool inputs before execution"
	default:
		return "Review error patterns and add safeguards"
	}
}

func buildFaultChain(history []providers.Message, errMsg string) []string {
	var chain []string

	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]

		if msg.Role == "tool" && (strings.Contains(strings.ToLower(msg.Content), "error") || strings.Contains(strings.ToLower(msg.Content), "failed")) {
			chain = append(chain, fmt.Sprintf("result was %s", msg.Content))
		}

		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				chain = append(chain, fmt.Sprintf("attempted %s(%s)", tc.Name, tc.Arguments))
			}
		}
	}

	if len(chain) == 0 {
		return []string{"No prior tool calls to trace"}
	}

	chain = append(chain, fmt.Sprintf("root cause: %s", errMsg))

	return chain
}
