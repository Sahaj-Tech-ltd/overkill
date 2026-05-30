// Package agent — L4 Self-Evaluate Loop (master plan §7.4, "Copilots to Colleagues" integration).
//
// SelfEvaluateLoop ties three existing modules into the closed-loop
// self-revision pattern that Deli Chen's agent used to write a 45-page
// RL survey paper in 6 iterations with <2 hours of human time:
//
//   Reflector        → "What went wrong and why?"
//   ErrorRecovery     → "What's the fault chain? How do we fix it?"
//   ConfidenceAssessment → "Is this good enough to ship?"
//
// The loop executes a task/phase, reflects on the output, assesses
// confidence, and auto-iterates with recovery context when confidence
// is below threshold. Learning is recorded on successful recovery so
// future runs benefit from past corrections.
//
// Architecture:
//   1. Execute task/phase via agent.Run()
//   2. Reflect on output (Reflector interface — existing)
//   3. Assess confidence (ConfidenceAssessment — existing)
//   4. If below threshold: inject recovery context (ErrorRecovery — existing), revise, retry
//   5. Repeat up to MaxIterations (default 6, matching Deli's agent)
//   6. On final failure after exhaustion: escalate with structured report
//   7. On recovery success: record learning via LearningRecorder — existing)

package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"

	"github.com/rs/zerolog/log"
)

// IterationOutcome classifies what happened after one self-evaluate attempt.
type IterationOutcome int

const (
	IterAccepted IterationOutcome = iota // confidence met threshold — done
	IterRevised                          // revised and retried (next iteration)
	IterDeferred                         // exhausted iterations — escalate
)

func (o IterationOutcome) String() string {
	switch o {
	case IterAccepted:
		return "accepted"
	case IterRevised:
		return "revised"
	case IterDeferred:
		return "deferred"
	default:
		return "unknown"
	}
}

// SelfEvalResult is the final result from a self-evaluate loop.
type SelfEvalResult struct {
	Accepted   bool        // true if confidence met threshold
	Iterations int         // total iterations consumed
	FinalNote  string      // human-readable summary
	Reflection *Reflection // last reflection (nil if accepted on first try)
	FaultChain []string    // from ErrorRecovery (nil if no faults)
}

// LearningCapture records metadata about a self-revision recovery event.
// Feeds into the learning pipeline via LearningRecorder.
type LearningCapture struct {
	ErrorClass   string
	Iterations   int
	WasRecovered bool
}

// SelfEvaluateLoop wires Reflector + ErrorRecovery + ConfidenceAssessment
// into the L4 closed-loop self-revision pattern. Zero-value is usable after
// calling NewSelfEvaluateLoop or setting fields manually.
type SelfEvaluateLoop struct {
	mu sync.RWMutex

	// Required components (set via constructor or setters).
	reflector  Reflector
	recovery   *ErrorRecovery
	learning   LearningRecorder
	redTeam    *RedTeamTestGen // §6.5 adversarial test generation

	// Confidence function. If nil, uses AssessConfidence from confidence.go.
	// Customizable so wiring layers can plug in LLM-based confidence scoring.
	confidenceFn ConfidenceFunc

	// MaxIterations caps self-revision attempts per phase/task.
	// Default 6 (matching the Deli Chen paper's 6-iteration experiment).
	MaxIterations int

	// MinConfidence is the float threshold for acceptance.
	// Default 0.7 (matches ConfidenceHigh in confidence.go).
	MinConfidence float64

	// MaxRevisionDepth caps how many prior iterations of recovery context
	// we inject into subsequent prompts. Prevents context bloat.
	MaxRevisionDepth int

	// Stats (atomic for hot-path reads).
	accepted  atomic.Int64
	revised   atomic.Int64
	deferred  atomic.Int64
}

// ConfidenceFunc produces a confidence assessment for a completed task.
// Matches the signature of AssessConfidence in confidence.go.
type ConfidenceFunc func(taskType string, history []providers.Message, model string) *ConfidenceAssessment

// NewSelfEvaluateLoop creates a ready-to-use loop. All parameters may be nil;
// the loop degrades gracefully — without a reflector it skips reflection,
// without recovery it skips fault-chain injection.
func NewSelfEvaluateLoop(reflector Reflector, recovery *ErrorRecovery, learning LearningRecorder) *SelfEvaluateLoop {
	return &SelfEvaluateLoop{
		reflector:        reflector,
		recovery:         recovery,
		learning:         learning,
		confidenceFn:     nil, // defaults to AssessConfidence
		MaxIterations:    6,
		MinConfidence:    0.7,
		MaxRevisionDepth: 3,
	}
}

// SetConfidenceFn overrides the default heuristic confidence scorer.
// Pass nil to restore the default.
func (s *SelfEvaluateLoop) SetConfidenceFn(fn ConfidenceFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.confidenceFn = fn
}

// SetReflector replaces the reflector at runtime.
func (s *SelfEvaluateLoop) SetReflector(r Reflector) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reflector = r
}

// SetLearningRecorder replaces the learning recorder at runtime.
func (s *SelfEvaluateLoop) SetLearningRecorder(lr LearningRecorder) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.learning = lr
}

// SetRedTeam wires the red-team test generator. Pass nil to disable
// adversarial test generation for this loop.
func (s *SelfEvaluateLoop) SetRedTeam(rt *RedTeamTestGen) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.redTeam = rt
}

// Stats returns accepted/revised/deferred counters.
func (s *SelfEvaluateLoop) Stats() (accepted, revised, deferred int64) {
	return s.accepted.Load(), s.revised.Load(), s.deferred.Load()
}

// Evaluate runs the self-evaluate loop for a completed task. Call this
// AFTER agent.Run() returns successfully — it evaluates the output and
// returns a result. If the result is not Accepted, the caller should
// inject the revision context and re-invoke agent.Run().
//
// taskType is a short label for the task (e.g., "compile", "refactor",
// "test") — used by confidence assessment. history is the conversation
// after agent.Run() completed (user message + assistant response +
// tool results). model is the model name for confidence scoring.
//
// Returns IterAccepted when confidence >= threshold. Returns IterRevised
// when iterations remain and revision is warranted. Returns IterDeferred
// when MaxIterations is exhausted.
func (s *SelfEvaluateLoop) Evaluate(
	taskType string,
	history []providers.Message,
	model string,
	iteration int,
) (IterationOutcome, *Reflection, []string) {
	s.mu.RLock()
	reflector := s.reflector
	recovery := s.recovery
	confidenceFn := s.confidenceFn
	maxIter := s.MaxIterations
	minConf := s.MinConfidence
	s.mu.RUnlock()

	if maxIter <= 0 {
		maxIter = 6
	}
	if minConf < 0 {
		minConf = 0.7
	}

	// 1. Assess confidence on current output.
	conf := s.assessConfidence(confidenceFn, taskType, history, model)

	log.Debug().
		Str("task_type", taskType).
		Float64("confidence", conf.Score).
		Int("iteration", iteration).
		Int("max_iter", maxIter).
		Msg("self-evaluate: confidence assessment")

	// 2. If confidence meets threshold, accept.
	if conf.Score >= minConf {
		s.accepted.Add(1)
		return IterAccepted, nil, nil
	}

	// 3. If no reflector available, check iterations — if exhausted, defer.
	if reflector == nil {
		if iteration >= maxIter {
			s.deferred.Add(1)
			return IterDeferred, nil, nil
		}
		s.revised.Add(1)
		return IterRevised, nil, nil
	}

	// 4. Use Reflector to understand the gap.
	failure := Failure{
		ToolName: taskType,
		Input:    fmt.Sprintf("confidence %.2f below threshold %.2f", conf.Score, minConf),
		Output:   conf.Reasoning,
		Error:    fmt.Sprintf("confidence gap: %.2f < %.2f", conf.Score, minConf),
	}
	reflection := reflector.Reflect(failure)

	// 5. Build fault chain from ErrorRecovery if available.
	var faultChain []string
	if recovery != nil && len(history) > 0 {
		// Build a synthetic error from the reflection for recovery analysis.
		synthErr := fmt.Errorf("self-evaluation: %s (root: %s)", reflection.Hypothesis, reflection.RootCause)
		report := recovery.Analyze(synthErr, history)
		faultChain = report.FaultChain
	}

	// 6. Check iterations.
	if iteration >= maxIter {
		s.deferred.Add(1)
		return IterDeferred, &reflection, faultChain
	}

	s.revised.Add(1)
	return IterRevised, &reflection, faultChain
}

// BuildRevisionContext constructs the prompt snippet to inject before
// the next agent.Run() call. Includes reflection notes and recovery
// context from prior failed iterations.
//
// revisionHistory is the accumulated reflections + fault chains from
// prior iterations in this loop. It's truncated to MaxRevisionDepth
// entries to prevent context bloat.
func (s *SelfEvaluateLoop) BuildRevisionContext(
	reflection *Reflection,
	faultChain []string,
	revisionHistory []RevisionEntry,
) string {
	s.mu.RLock()
	maxDepth := s.MaxRevisionDepth
	s.mu.RUnlock()

	if maxDepth <= 0 {
		maxDepth = 3
	}

	var b strings.Builder
	b.WriteString("\n## SELF-REVISION CONTEXT\n")
	b.WriteString("The previous attempt was below confidence threshold. ")
	b.WriteString("Review what went wrong and revise your approach.\n\n")

	// Latest reflection.
	if reflection != nil {
		b.WriteString("### Latest Reflection\n")
		if reflection.RootCause != "" {
			b.WriteString(fmt.Sprintf("- Root cause: %s\n", reflection.RootCause))
		}
		if reflection.Hypothesis != "" {
			b.WriteString(fmt.Sprintf("- Hypothesis: %s\n", reflection.Hypothesis))
		}
		if reflection.Confidence > 0 {
			b.WriteString(fmt.Sprintf("- Self-assessed confidence: %.0f%%\n", reflection.Confidence*100))
		}
		b.WriteString("\n")
	}

	// Fault chain from ErrorRecovery.
	if len(faultChain) > 0 {
		b.WriteString("### Fault Chain\n")
		for _, entry := range faultChain {
			b.WriteString(fmt.Sprintf("- %s\n", entry))
		}
		b.WriteString("\n")
	}

	// Prior revision history (truncated).
	if len(revisionHistory) > 0 {
		start := 0
		if len(revisionHistory) > maxDepth {
			start = len(revisionHistory) - maxDepth
		}
		b.WriteString("### Prior Revision Attempts\n")
		for i := start; i < len(revisionHistory); i++ {
			entry := revisionHistory[i]
			b.WriteString(fmt.Sprintf("- Attempt %d: %s (root: %s)\n",
				entry.Iteration, entry.Hypothesis, entry.RootCause))
		}
		b.WriteString("\n")
	}

	b.WriteString("Revise your approach based on these insights and retry the task.\n")
	b.WriteString("Do NOT repeat the same approach that failed.\n")

	return b.String()
}

// RevisionEntry records one self-revision attempt for the history chain.
type RevisionEntry struct {
	Iteration  int
	RootCause  string
	Hypothesis string
	Confidence float64
}

// RecordLearning captures a successful-recovery learning event.
// Called after a phase transitions from IterRevised → IterAccepted.
func (s *SelfEvaluateLoop) RecordLearning(errorClass string) {
	s.mu.RLock()
	learning := s.learning
	s.mu.RUnlock()

	if learning == nil || errorClass == "" {
		return
	}
	// Best-effort: never block the loop on learning recording.
	defer func() { _ = recover() }()
	learning.RecordSuccess(errorClass)
}

// RunPhase executes one AutoMode phase within the self-evaluate loop.
// It calls agent.Run() with the phase prompt, evaluates the output,
// and auto-iterates up to MaxIterations times.
//
// The runner callback executes the actual agent.Run() call. Decoupled
// so SelfEvaluateLoop doesn't depend on Agent's internal Run signature.
// runner receives the prompt + any revision context and returns the
// resulting conversation history.
//
// Returns the final result and the accumulated history.
func (s *SelfEvaluateLoop) RunPhase(
	ctx context.Context,
	phase *PlanPhase,
	model string,
	runner func(ctx context.Context, prompt string, revisionContext string) ([]providers.Message, error),
) (*SelfEvalResult, []providers.Message, error) {
	s.mu.RLock()
	maxIter := s.MaxIterations
	s.mu.RUnlock()

	if maxIter <= 0 {
		maxIter = 6
	}

	var (
		allHistory      []providers.Message
		revisionHistory []RevisionEntry
		lastReflection  *Reflection
		lastFaultChain  []string
		taskType        = extractTaskType(phase)
	)

	startTime := time.Now()

	for iter := 1; iter <= maxIter; iter++ {
		// Build revision context for iterations 2+.
		var revCtx string
		if iter > 1 && lastReflection != nil {
			revCtx = s.BuildRevisionContext(lastReflection, lastFaultChain, revisionHistory)
		}

		// Execute the phase.
		history, err := runner(ctx, phase.Description, revCtx)
		if err != nil {
			return &SelfEvalResult{
				Accepted:  false,
				Iterations: iter,
				FinalNote:  fmt.Sprintf("phase execution failed: %v", err),
			}, allHistory, fmt.Errorf("self-evaluate: run phase: %w", err)
		}

		allHistory = append(allHistory, history...)

		// Evaluate the output.
		outcome, reflection, faultChain := s.Evaluate(taskType, allHistory, model, iter)

		switch outcome {
		case IterAccepted:
			// If we revised at least once, record the learning.
			if iter > 1 {
				s.RecordLearning("self_evaluate_recovered")
			}

			// §6.5 Red-team gate: run adversarial tests before acceptance.
			// If tests fail, inject failures as revision context and retry.
			s.mu.RLock()
			redTeam := s.redTeam
			s.mu.RUnlock()
			if redTeam != nil && redTeam.cfg.Enabled {
				rtResult, rtErr := redTeam.GenerateAndRun(ctx, allHistory, "", phase.Description)
				if rtErr != nil {
					log.Warn().Err(rtErr).Str("phase", phase.Title).Msg("self-evaluate: red-team check failed, continuing")
				} else if rtResult != nil && !rtResult.Passed {
					log.Warn().
						Str("phase", phase.Title).
						Int("tests_failed", rtResult.TestsFailed).
						Int("iteration", iter).
						Msg("self-evaluate: red-team tests failed, revising")

					// Build revision context from test failures.
					revCtx := redTeam.BuildRevisionContext(rtResult)
					if revCtx != "" && iter < maxIter {
						// Feed failures back into the revision loop.
						if reflection == nil {
							reflection = &Reflection{
								Mode:       "red_team",
								RootCause:  fmt.Sprintf("adversarial tests failed: %d/%d", rtResult.TestsFailed, rtResult.TestsRun),
								Hypothesis: "fix code to pass all adversarial tests",
							}
						}
						lastReflection = reflection
						lastFaultChain = rtResult.Failures
						revisionHistory = append(revisionHistory, RevisionEntry{
							Iteration:  iter,
							RootCause:  fmt.Sprintf("red-team: %d tests failed", rtResult.TestsFailed),
							Hypothesis: "fix code to pass adversarial tests",
						})
						continue // retry the loop
					}

					// Red-team tests failed on final iteration — do NOT accept.
					// Treat as deferred so the caller can escalate.
					if iter >= maxIter {
						s.deferred.Add(1)
						elapsed := time.Since(startTime)
						note := fmt.Sprintf("Deferred after %d iterations — red-team tests failed on final iteration.", iter)
						log.Warn().
							Str("phase", phase.Title).
							Int("iterations", iter).
							Int("tests_failed", rtResult.TestsFailed).
							Dur("elapsed", elapsed).
							Msg("self-evaluate: deferred — red-team gate failed on final iteration")
						return &SelfEvalResult{
							Accepted:   false,
							Iterations: iter,
							FinalNote:  note,
							Reflection: &Reflection{
								Mode:       "red_team",
								RootCause:  fmt.Sprintf("adversarial tests failed: %d/%d", rtResult.TestsFailed, rtResult.TestsRun),
								Hypothesis: "fix code to pass all adversarial tests",
							},
							FaultChain: rtResult.Failures,
						}, allHistory, nil
					}
				}
			}

			elapsed := time.Since(startTime)
			log.Info().
				Str("phase", phase.Title).
				Int("iterations", iter).
				Dur("elapsed", elapsed).
				Msg("self-evaluate: accepted")

			return &SelfEvalResult{
				Accepted:   true,
				Iterations: iter,
				FinalNote:  fmt.Sprintf("Accepted after %d iteration(s) in %s", iter, elapsed.Round(time.Second)),
				Reflection: reflection,
				FaultChain: faultChain,
			}, allHistory, nil

		case IterRevised:
			lastReflection = reflection
			lastFaultChain = faultChain

			if reflection != nil {
				revisionHistory = append(revisionHistory, RevisionEntry{
					Iteration:  iter,
					RootCause:  reflection.RootCause,
					Hypothesis: reflection.Hypothesis,
					Confidence: reflection.Confidence,
				})
			}

			log.Debug().
				Str("phase", phase.Title).
				Int("iteration", iter).
				Int("max_iter", maxIter).
				Msg("self-evaluate: revising")

		case IterDeferred:
			elapsed := time.Since(startTime)
			note := fmt.Sprintf("Deferred after %d iterations in %s.", iter, elapsed.Round(time.Second))
			if lastReflection != nil {
				note += fmt.Sprintf(" Last reflection: %s (root: %s)",
					lastReflection.Hypothesis, lastReflection.RootCause)
			}

			log.Warn().
				Str("phase", phase.Title).
				Int("iterations", iter).
				Dur("elapsed", elapsed).
				Msg("self-evaluate: deferred — escalation needed")

			return &SelfEvalResult{
				Accepted:   false,
				Iterations: iter,
				FinalNote:  note,
				Reflection: lastReflection,
				FaultChain: lastFaultChain,
			}, allHistory, nil
		}
	}

	// Shouldn't reach here (loop above handles all outcomes), but be safe.
	return &SelfEvalResult{
		Accepted:   false,
		Iterations: maxIter,
		FinalNote:  "Unexpected: loop exhausted without explicit outcome.",
	}, allHistory, nil
}

// assessConfidence calls the configured confidence function, falling back
// to the package-level AssessConfidence.
func (s *SelfEvaluateLoop) assessConfidence(
	fn ConfidenceFunc,
	taskType string,
	history []providers.Message,
	model string,
) *ConfidenceAssessment {
	if fn != nil {
		return fn(taskType, history, model)
	}
	return AssessConfidence(taskType, history, model)
}

// extractTaskType derives a task type label from a plan phase.
func extractTaskType(phase *PlanPhase) string {
	if phase == nil {
		return "unknown"
	}
	title := strings.ToLower(phase.Title)

	// Map phase titles to known confidence task types.
	taskTypeIndicators := []string{
		"test", "fix", "refactor", "rewrite", "migrate",
		"build", "deploy", "implement", "add", "remove",
		"update", "upgrade", "configure", "setup", "audit",
	}

	for _, indicator := range taskTypeIndicators {
		if strings.Contains(title, indicator) {
			return indicator
		}
	}

	return "unknown"
}

// RevisionPrompt builds a compact inline revision prompt for the agent's
// system message when in a self-revision loop. Less verbose than
// BuildRevisionContext — intended for injection into the system prompt
// alongside the phase description.
func (s *SelfEvaluateLoop) RevisionPrompt(
	iteration int,
	maxIter int,
	reflection *Reflection,
) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		"[SELF-REVISION %d/%d] Previous attempt was below confidence threshold. ",
		iteration, maxIter,
	))

	if reflection != nil {
		if reflection.RootCause != "" {
			b.WriteString(fmt.Sprintf("Root cause: %s. ", reflection.RootCause))
		}
		if reflection.Hypothesis != "" {
			b.WriteString(fmt.Sprintf("Fix: %s. ", reflection.Hypothesis))
		}
	}

	b.WriteString("Revise and retry with a DIFFERENT approach.")
	return b.String()
}
