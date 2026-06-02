// Package sinks provides built-in Sink implementations for CompletionEvents.
package sinks

import (
	"context"
	"encoding/json"
	"fmt"
	"log"

	"github.com/Sahaj-Tech-ltd/overkill/internal/events"
)

// LogSink writes every CompletionEvent as a single JSON line to a log.Logger.
// It is the always-on fallback sink — construct it with log.Default() when no
// specialised sink is available.
type LogSink struct {
	logger *log.Logger
}

// NewLogSink returns a LogSink that writes to logger. logger must be non-nil.
func NewLogSink(logger *log.Logger) *LogSink {
	return &LogSink{logger: logger}
}

// Name implements events.Sink.
func (s *LogSink) Name() string { return "log" }

// Send serialises evt to JSON and writes it as a single log line.
func (s *LogSink) Send(_ context.Context, evt events.CompletionEvent) error {
	b, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("log sink: marshal: %w", err)
	}
	s.logger.Println(string(b))
	return nil
}
