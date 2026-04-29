package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/Sahaj-Tech-ltd/ethos/internal/hooks"
	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
	"github.com/Sahaj-Tech-ltd/ethos/internal/security"
	"github.com/Sahaj-Tech-ltd/ethos/internal/tokenizer"
	"github.com/Sahaj-Tech-ltd/ethos/internal/tools"
)

type Agent struct {
	mu           sync.RWMutex
	provider     providers.Provider
	toolRegistry *tools.Registry
	hooks        *hooks.Registry
	scanners     []security.Scanner
	tokenizer    *tokenizer.Estimator
	model        string
	maxTokens    int
	systemPrompt string
	history      []providers.Message
	maxSteps     int
	sessionID    string
}

type Config struct {
	Provider     providers.Provider
	Tools        *tools.Registry
	Hooks        *hooks.Registry
	Scanners     []security.Scanner
	Tokenizer    *tokenizer.Estimator
	Model        string
	MaxTokens    int
	SystemPrompt string
	MaxSteps     int
	SessionID    string
}

func New(cfg Config) *Agent {
	maxSteps := cfg.MaxSteps
	if maxSteps <= 0 {
		maxSteps = 20
	}

	return &Agent{
		provider:     cfg.Provider,
		toolRegistry: cfg.Tools,
		hooks:        cfg.Hooks,
		scanners:     cfg.Scanners,
		tokenizer:    cfg.Tokenizer,
		model:        cfg.Model,
		maxTokens:    cfg.MaxTokens,
		systemPrompt: cfg.SystemPrompt,
		maxSteps:     maxSteps,
		sessionID:    cfg.SessionID,
		history:      make([]providers.Message, 0),
	}
}

type RunResult struct {
	Response    string `json:"response"`
	ToolCalls   int    `json:"tool_calls"`
	TotalTokens int    `json:"total_tokens"`
	Steps       int    `json:"steps"`
	Model       string `json:"model"`
	Blocked     bool   `json:"blocked"`
	BlockReason string `json:"block_reason,omitempty"`
}

func (a *Agent) Run(ctx context.Context, userInput string) (*RunResult, error) {
	for _, scanner := range a.scanners {
		result, err := scanner.Scan(userInput)
		if err != nil {
			return nil, fmt.Errorf("agent: security scan failed: %w", err)
		}
		if result.Blocked {
			return &RunResult{
				Blocked:     true,
				BlockReason: fmt.Sprintf("blocked by %s: %s", scanner.Name(), result.Findings[0].Description),
				Model:       a.model,
			}, nil
		}
	}

	a.mu.Lock()
	a.history = append(a.history, providers.Message{
		Role:    "user",
		Content: userInput,
	})
	a.mu.Unlock()

	result := &RunResult{
		Model: a.model,
	}

	for step := 0; step < a.maxSteps; step++ {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("agent: context cancelled: %w", ctx.Err())
		default:
		}

		stepResult, err := a.step(ctx)
		if err != nil {
			if a.hooks != nil {
				a.hooks.Fire(ctx, hooks.OnError, hooks.Event{
					Error:     err,
					SessionID: a.sessionID,
				})
			}
			return nil, fmt.Errorf("agent: step %d: %w", step+1, err)
		}

		result.Steps++
		result.ToolCalls += len(stepResult.ToolCalls)
		result.TotalTokens += stepResult.Tokens.InputTokens + stepResult.Tokens.OutputTokens

		if stepResult.Done {
			result.Response = stepResult.Thought
			return result, nil
		}
	}

	return nil, fmt.Errorf("agent: exceeded max steps (%d)", a.maxSteps)
}

func (a *Agent) History() []providers.Message {
	a.mu.RLock()
	defer a.mu.RUnlock()

	cp := make([]providers.Message, len(a.history))
	copy(cp, a.history)
	return cp
}

func (a *Agent) SetHistory(messages []providers.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.history = make([]providers.Message, len(messages))
	copy(a.history, messages)
}

func (a *Agent) ClearHistory() {
	a.mu.Lock()
	defer a.mu.Unlock()

	a.history = make([]providers.Message, 0)
}

func (a *Agent) buildRequest() providers.Request {
	a.mu.RLock()
	msgs := make([]providers.Message, len(a.history))
	copy(msgs, a.history)
	a.mu.RUnlock()

	var toolDefs []providers.Tool
	if a.toolRegistry != nil {
		for _, name := range a.toolRegistry.List() {
			t, _ := a.toolRegistry.Get(name)
			toolDefs = append(toolDefs, providers.Tool{
				Name:        t.Name(),
				Description: "Execute tool: " + t.Name(),
			})
		}
	}

	return providers.Request{
		Model:        a.model,
		Messages:     msgs,
		Tools:        toolDefs,
		MaxTokens:    a.maxTokens,
		SystemPrompt: BuildSystemPrompt(a.systemPrompt, a.toolRegistry),
	}
}

func (a *Agent) SetModel(modelID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.model = modelID
}

func (a *Agent) Model() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.model
}

func (a *Agent) IsBusy() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return len(a.history) > 0
}

func (a *Agent) SessionID() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sessionID
}

func (a *Agent) appendMessage(msg providers.Message) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.history = append(a.history, msg)
}

func (a *Agent) appendToolResultMessage(toolCallID, toolName string, output json.RawMessage, toolErr error) {
	content := string(output)
	if toolErr != nil {
		content = fmt.Sprintf("error: %s", toolErr.Error())
	}

	a.appendMessage(providers.Message{
		Role:       "tool",
		Content:    content,
		ToolCallID: toolCallID,
	})
}
