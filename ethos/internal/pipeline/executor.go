package pipeline

import (
	"context"
	"fmt"
	"time"

	"github.com/Sahaj-Tech-ltd/ethos/internal/providers"
)

type Executor struct {
	provider   providers.Provider
	model      string
	maxRetries int
}

type Config struct {
	Provider   providers.Provider
	Model      string
	MaxRetries int
}

func NewExecutor(cfg Config) *Executor {
	retries := cfg.MaxRetries
	if retries <= 0 {
		retries = 2
	}
	return &Executor{
		provider:   cfg.Provider,
		model:      cfg.Model,
		maxRetries: retries,
	}
}

func (e *Executor) Run(ctx context.Context, request string) (*PipelineResult, error) {
	start := time.Now()

	stages := []struct {
		stage  Stage
		prompt string
	}{
		{StageSpec, specPrompt()},
		{StageTest, testPrompt()},
		{StageCode, codePrompt()},
		{StageRefactor, refactorPrompt()},
	}

	var results []StageResult
	input := request

	for _, s := range stages {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("pipeline: cancelled before %s stage: %w", s.stage, ctx.Err())
		default:
		}

		var result *StageResult
		var err error

		for attempt := 0; attempt <= e.maxRetries; attempt++ {
			select {
			case <-ctx.Done():
				return nil, fmt.Errorf("pipeline: cancelled during %s stage: %w", s.stage, ctx.Err())
			default:
			}

			result, err = e.executeStage(ctx, s.stage, s.prompt, input)
			if err == nil {
				break
			}
			if attempt == e.maxRetries {
				return nil, fmt.Errorf("pipeline: %s stage failed after %d attempts: %w", s.stage, attempt+1, err)
			}
		}

		results = append(results, *result)
		input = result.Content
	}

	totalTime := time.Since(start)
	success := true
	for _, r := range results {
		if len(r.Errors) > 0 {
			success = false
			break
		}
	}

	finalFiles := make(map[string]string)
	lastStage := results[len(results)-1]
	if lastStage.Files != nil {
		finalFiles = lastStage.Files
	}

	return &PipelineResult{
		Stages:     results,
		TotalTime:  totalTime,
		Success:    success,
		FinalFiles: finalFiles,
	}, nil
}

func (e *Executor) RunStage(ctx context.Context, stage Stage, input string) (*StageResult, error) {
	prompt := stagePrompt(stage)
	if prompt == "" {
		return nil, fmt.Errorf("pipeline: unknown stage %d", stage)
	}

	var result *StageResult
	var err error

	for attempt := 0; attempt <= e.maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("pipeline: cancelled during %s stage: %w", stage, ctx.Err())
		default:
		}

		result, err = e.executeStage(ctx, stage, prompt, input)
		if err == nil {
			return result, nil
		}
		if attempt == e.maxRetries {
			return nil, fmt.Errorf("pipeline: %s stage failed after %d attempts: %w", stage, attempt+1, err)
		}
	}

	return nil, fmt.Errorf("pipeline: unreachable")
}

func (e *Executor) executeStage(ctx context.Context, stage Stage, systemPrompt, input string) (*StageResult, error) {
	stageStart := time.Now()

	req := providers.Request{
		Model: e.model,
		Messages: []providers.Message{
			{Role: "user", Content: input},
		},
		SystemPrompt: systemPrompt,
	}

	resp, err := e.provider.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("stage %s: llm call failed: %w", stage, err)
	}

	if resp.Content == "" {
		return nil, fmt.Errorf("stage %s: empty response from model", stage)
	}

	duration := time.Since(stageStart)

	return &StageResult{
		Stage:    stage,
		Content:  resp.Content,
		Passed:   true,
		Duration: duration,
	}, nil
}

func stagePrompt(s Stage) string {
	switch s {
	case StageSpec:
		return specPrompt()
	case StageTest:
		return testPrompt()
	case StageCode:
		return codePrompt()
	case StageRefactor:
		return refactorPrompt()
	default:
		return ""
	}
}
