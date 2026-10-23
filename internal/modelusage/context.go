// Package modelusage propagates actual model-call accounting across optional
// middleware without depending on the agent or changing provider contracts.
package modelusage

import (
	"context"

	"github.com/Sahaj-Tech-ltd/overkill/internal/providers"
)

type callbacks struct {
	before func(context.Context) error
	record func(context.Context, providers.Usage) error
}
type contextKey struct{}

func WithCallbacks(ctx context.Context, before func(context.Context) error, record func(context.Context, providers.Usage) error) context.Context {
	return context.WithValue(ctx, contextKey{}, callbacks{before: before, record: record})
}
func Before(ctx context.Context) error {
	if cb, ok := ctx.Value(contextKey{}).(callbacks); ok && cb.before != nil {
		return cb.before(ctx)
	}
	return CheckAccounting(ctx)
}

// Record accepts only a provider-reported usage value; estimates of prompt or
// output size are not billable usage and must never be supplied here.
func Record(ctx context.Context, usage providers.Usage) error {
	if cb, ok := ctx.Value(contextKey{}).(callbacks); ok && cb.record != nil {
		return cb.record(ctx, usage)
	}
	return RecordAccounting(ctx, usage)
}

// Accounting is independent of a session's goal. A parent task can therefore
// charge several agents, including agents without a standing session goal.
type Accounting struct {
	Before func(context.Context) error
	Record func(context.Context, string, providers.Usage, bool) error
}
type accountingKey struct{}
type sampleKey struct{}
type sampleInfo struct {
	model    string
	reported bool
}

func WithAccounting(ctx context.Context, accounting Accounting) context.Context {
	return context.WithValue(ctx, accountingKey{}, accounting)
}
func CheckAccounting(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return context.Cause(ctx)
	}
	if a, ok := ctx.Value(accountingKey{}).(Accounting); ok && a.Before != nil {
		return a.Before(ctx)
	}
	return nil
}
func RecordAccounting(ctx context.Context, usage providers.Usage) error {
	if a, ok := ctx.Value(accountingKey{}).(Accounting); ok && a.Record != nil {
		info, _ := ctx.Value(sampleKey{}).(sampleInfo)
		return a.Record(ctx, info.model, usage, info.reported)
	}
	return nil
}
func WithSample(ctx context.Context, model string, reported bool) context.Context {
	return context.WithValue(ctx, sampleKey{}, sampleInfo{model, reported})
}
func RecordSample(ctx context.Context, model string, usage providers.Usage, reported bool) error {
	return Record(WithSample(ctx, model, reported), usage)
}
