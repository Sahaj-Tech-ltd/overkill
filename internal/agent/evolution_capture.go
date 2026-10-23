package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/learning"
)

// SetEvolutionEngine binds recording and reviewed guidance to this workspace.
// The model has no tool for approving or installing generated guidance.
func (a *Agent) SetEvolutionEngine(engine *learning.Engine) {
	a.mu.Lock()
	a.evolutionEngine = engine
	a.mu.Unlock()
}

func (a *Agent) EvolutionEngine() *learning.Engine {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.evolutionEngine
}

func (a *Agent) recordEvolutionTurn(parent context.Context) {
	a.mu.RLock()
	engine := a.evolutionEngine
	if engine == nil {
		a.mu.RUnlock()
		return
	}
	record := learning.LearningRecord{SessionID: a._sessionID, ModelID: a.model}
	callIndexes := make(map[string]int)
	start := max(0, len(a.history)-256)
	for i := len(a.history) - 1; i >= start; i-- {
		if a.history[i].Role == "user" {
			start = i
			record.TaskHash = fmt.Sprintf("%x", sha256.Sum256([]byte(a.history[i].Content)))
			break
		}
	}
	for _, msg := range a.history[start:] {
		record.EventCount++
		if msg.Role == "assistant" && msg.Content != "" {
			record.FinalOutput = msg.Content
		}
		for _, call := range msg.ToolCalls {
			if len(record.ToolExecutions) < 32 {
				if call.ID != "" {
					callIndexes[call.ID] = len(record.ToolExecutions)
				}
				record.ToolExecutions = append(record.ToolExecutions, learning.ToolExecution{ToolName: call.Name})
			}
		}
		// Keep error evidence without copying paths, arguments or tool output.
		if msg.Role == "tool" && evolutionToolFailed(msg.Content) {
			if index, ok := callIndexes[msg.ToolCallID]; ok {
				record.ToolExecutions[index].Error = "tool execution failed"
			}
		}
	}
	a.mu.RUnlock()
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	if err := engine.RecordTurn(ctx, record); err != nil {
		a.emit("evolution_record_failed", map[string]any{"session_id": a.SessionID()})
	}
}

func evolutionToolFailed(content string) bool {
	if strings.HasPrefix(content, "error:") {
		return true
	}
	if len(content) > externalToolContextLimit+8192 {
		return false
	}
	var envelope struct {
		Trust string `json:"trust"`
		Data  string `json:"data"`
	}
	return json.Unmarshal([]byte(content), &envelope) == nil && envelope.Trust == "untrusted_external_tool_output" && strings.HasPrefix(envelope.Data, "error:")
}

func (a *Agent) withEvolutionGuidance(ctx context.Context, prompt string) string {
	engine := a.EvolutionEngine()
	if engine == nil {
		return prompt
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	guidance, err := engine.LoadAppliedGuidance(ctx)
	if err != nil {
		a.emit("evolution_guidance_unavailable", map[string]any{"session_id": a.SessionID()})
		return prompt
	}
	if len(guidance) == 0 {
		return prompt
	}
	var block strings.Builder
	block.WriteString("\n\nOperator-reviewed project skills (limited to this workspace; subordinate to tool permissions and the current user request):\n")
	for _, skill := range guidance {
		block.WriteString("\n")
		block.WriteString(skill.Body)
		block.WriteString("\n")
	}
	return prompt + block.String()
}
