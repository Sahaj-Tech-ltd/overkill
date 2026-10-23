package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
	"time"
)

func (a *Agent) autoMemoryPrompt() string {
	a.mu.RLock()
	e := a.autoMemory
	a.mu.RUnlock()
	return e.Prompt()
}

// Extraction completes before Done/Run returns so a one-shot CLI can persist
// facts before Shutdown. It is optional, best effort, bounded and cancellable.
// Only user text enters extraction: no tool output, attachments or assistant
// claims. Tool results are never promoted to lasting user facts.
func (a *Agent) extractAutoMemory(ctx context.Context) {
	if checkGoalBudget(ctx) != nil {
		return
	}
	a.mu.RLock()
	e := a.autoMemory
	count := a.autoMemoryMessages
	processed := a.autoMemoryProcessed
	if e == nil || !e.ShouldExtract(count-processed, fmt.Sprint(count)) {
		a.mu.RUnlock()
		return
	}
	var messages []providers.Message
	for i := len(a.history) - 1; i >= 0 && len(messages) < 20; i-- {
		if a.history[i].Role == "user" {
			messages = append(messages, providers.Message{Role: "user", Content: a.history[i].Content})
		}
	}
	a.mu.RUnlock()
	for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
		messages[i], messages[j] = messages[j], messages[i]
	}
	data, _ := json.Marshal(messages)
	extractionCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if a.sessionCtx != nil {
		stop := context.AfterFunc(a.sessionCtx, cancel)
		defer stop()
	}
	if err := e.Extract(extractionCtx, string(data)); err != nil {
		return
	}
	e.MarkExtracted(fmt.Sprint(count))
	a.mu.Lock()
	if a.autoMemory == e {
		a.autoMemoryProcessed = count
	}
	a.mu.Unlock()
}
