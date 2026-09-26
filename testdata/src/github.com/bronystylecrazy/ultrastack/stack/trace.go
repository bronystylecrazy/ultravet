package stack

import "context"

// Tracer opens spans; the shape the stale-context lint (UV0012) keys on.
type Tracer interface {
	Span(ctx context.Context, attrs ...any) (Span, func(err *error))
}

// Span is the context children get, and logs with trace ids.
type Span struct {
	context.Context
}

func (s Span) Debug(msg string, args ...any) {}
func (s Span) Info(msg string, args ...any)  {}
func (s Span) Warn(msg string, args ...any)  {}
func (s Span) Error(msg string, args ...any) {}
