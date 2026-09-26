// Package spanctx pins UV0012: once a span is open, the span IS the context —
// a later use of the context it was opened from is a stale parent.
package spanctx

import (
	"context"
	"time"

	"github.com/bronystylecrazy/ultrastack/stack"
)

type store interface {
	MarkPaid(ctx context.Context, id string) error
}

type Service struct {
	trace stack.Tracer
	store store
}

// The taught form: the span is the ctx children get. Clean.
func (s *Service) Pay(ctx context.Context, id string) (err error) {
	span, end := s.trace.Span(ctx, "order.id", id)
	defer end(&err)
	span.Info("charging")
	return s.store.MarkPaid(span, id)
}

func (s *Service) Stale(ctx context.Context, id string) (err error) {
	span, end := s.trace.Span(ctx, "order.id", id)
	defer end(&err)
	span.Info("charging")
	return s.store.MarkPaid(ctx, id) // want `warning\[UV0012\]: ctx is used after span opened a span — pass span: it is the context, so the call nests under the span \(opened at spanctx.go:30\)`
}

// Any identifier, and the = form.
func (s *Service) Refund(c context.Context, id string) (err error) {
	var sp stack.Span
	var done func(*error)
	sp, done = s.trace.Span(c)
	defer done(&err)
	_ = sp
	return s.store.MarkPaid(c, id) // want `warning\[UV0012\]: c is used after sp opened a span`
}

// `_` discards the span on purpose: nothing to nest under.
func (s *Service) Discarded(ctx context.Context, id string) (err error) {
	_, end := s.trace.Span(ctx)
	defer end(&err)
	return s.store.MarkPaid(ctx, id)
}

// Deriving from the stale ctx is itself a use; after the write, ctx is new.
func (s *Service) Reassigned(ctx context.Context, id string) (err error) {
	span, end := s.trace.Span(ctx)
	defer end(&err)
	_ = span
	ctx, cancel := context.WithTimeout(ctx, time.Second) // want `warning\[UV0012\]: ctx is used after span opened a span`
	defer cancel()
	return s.store.MarkPaid(ctx, id)
}

func (s *Service) Rebound(ctx context.Context, id string) (err error) {
	span, end := s.trace.Span(ctx)
	defer end(&err)
	ctx = span
	return s.store.MarkPaid(ctx, id)
}

// A closure written before the span captured the ctx of its day; a goroutine
// started after it is handed the stale one.
func (s *Service) Closures(ctx context.Context, id string) (err error) {
	early := func() error { return s.store.MarkPaid(ctx, id) }
	span, end := s.trace.Span(ctx)
	defer end(&err)
	go func() {
		_ = s.store.MarkPaid(ctx, id) // want `warning\[UV0012\]: ctx is used after span opened a span`
	}()
	_ = span
	return early()
}

// A span opened inside a func literal is checked in that literal's body.
func (s *Service) Handler() func(ctx context.Context, id string) error {
	return func(ctx context.Context, id string) (err error) {
		span, end := s.trace.Span(ctx)
		defer end(&err)
		_ = span
		return s.store.MarkPaid(ctx, id) // want `warning\[UV0012\]: ctx is used after span opened a span`
	}
}

// Past the span's scope the ctx is the right parent again.
func (s *Service) Each(ctx context.Context, ids []string) error {
	for _, id := range ids {
		span, end := s.trace.Span(ctx, "order.id", id)
		err := s.store.MarkPaid(span, id)
		end(&err)
	}
	return s.store.MarkPaid(ctx, "")
}

// Nested: the parent span is still used on purpose; the original ctx is not.
func (s *Service) Nested(ctx context.Context, id string) (err error) {
	span, end := s.trace.Span(ctx)
	defer end(&err)
	child, done := s.trace.Span(span, "step", "mark")
	defer done(&err)
	span.Info("parent still logs")
	_ = s.store.MarkPaid(child, id)
	_ = s.store.MarkPaid(span, id)
	return s.store.MarkPaid(ctx, id) // want `warning\[UV0012\]: ctx is used after span opened a span`
}

// A foreign Span method is not a stack span: no flag.
type otel struct{}

func (otel) Span(ctx context.Context, attrs ...any) (context.Context, func(*error)) {
	return ctx, func(*error) {}
}

func (s *Service) Foreign(t otel, ctx context.Context, id string) (err error) {
	sctx, end := t.Span(ctx)
	defer end(&err)
	_ = sctx
	return s.store.MarkPaid(ctx, id)
}

// A concrete tracer with the Tracer signature opens a stack span all the same.
type fixed struct{}

func (fixed) Span(ctx context.Context, attrs ...any) (stack.Span, func(err *error)) {
	return stack.Span{Context: ctx}, func(*error) {}
}

func (s *Service) Concrete(t fixed, ctx context.Context, id string) (err error) {
	span, end := t.Span(ctx)
	defer end(&err)
	_ = span
	return s.store.MarkPaid(ctx, id) // want `warning\[UV0012\]: ctx is used after span opened a span`
}
