package compaction

import (
	"fmt"
	"strings"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

const maxMessageChars = 2000

func truncateMessage(content string, maxLen int) string {
	if len(content) <= maxLen {
		return content
	}
	return content[:maxLen] + "\n[...truncated...]"
}

func formatMessagesForSummary(messages []providers.Message) string {
	var b strings.Builder
	for _, msg := range messages {
		switch msg.Role {
		case "user":
			b.WriteString("User: ")
			b.WriteString(truncateMessage(msg.Content, maxMessageChars))
			b.WriteString("\n\n")
		case "assistant":
			b.WriteString("Assistant: ")
			b.WriteString(truncateMessage(msg.Content, maxMessageChars))
			if len(msg.ToolCalls) > 0 {
				for _, tc := range msg.ToolCalls {
					b.WriteString(fmt.Sprintf("\n[Called tool: %s(%s)]", tc.Name, truncateMessage(tc.Arguments, 500)))
				}
			}
			b.WriteString("\n\n")
		case "tool":
			toolName := msg.ToolCallID
			if toolName == "" {
				toolName = "unknown"
			}
			b.WriteString(fmt.Sprintf("Tool (%s): ", toolName))
			b.WriteString(truncateMessage(msg.Content, maxMessageChars))
			b.WriteString("\n\n")
		default:
			b.WriteString(fmt.Sprintf("%s: ", msg.Role))
			b.WriteString(truncateMessage(msg.Content, maxMessageChars))
			b.WriteString("\n\n")
		}
	}
	return b.String()
}

func buildDetailedSummaryPrompt(messages []providers.Message, targetTokens int) string {
	conversation := formatMessagesForSummary(messages)
	return fmt.Sprintf(`Summarize this conversation in detail, preserving key decisions, code references, file paths, and important context. Target length: approximately %d tokens.

Guidelines:
- Preserve specific file paths, function names, and code references
- Keep track of decisions made and their reasoning
- Note any errors encountered and how they were resolved
- Maintain the sequence of important events
- Include any unresolved issues or open questions

Conversation:
%s

Provide a detailed summary:`, targetTokens, conversation)
}

func buildAggressiveSummaryPrompt(messages []providers.Message, targetTokens int) string {
	conversation := formatMessagesForSummary(messages)
	return fmt.Sprintf(`Summarize in concise bullet points. Target: approximately %d tokens. Focus on:
- Decisions made
- Code/files changed
- Errors encountered and resolutions
- Unresolved issues

Conversation:
%s

Provide a concise bullet-point summary:`, targetTokens/2, conversation)
}
