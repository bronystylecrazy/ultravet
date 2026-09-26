package vocab

import (
	"log/slog"

	"github.com/bronystylecrazy/di"
	"github.com/bronystylecrazy/ultrastack/stack"
)

type Route struct{ Pattern string }
type Mailer struct{}
type Tracer struct{}
type Store interface{ Get() string }
type PgStore struct{}

func (p *PgStore) Get() string { return "" }

type Server struct{}

// The consumer-type vocabulary: Many collects (two providers fine),
// Optional tolerates absence, variadic options are soft, Runner and
// *slog.Logger are given, Lazy still requires a provider.
func NewServer(
	routes di.Many[Route],
	tracer di.Optional[*Tracer],
	mailer di.Lazy[*Mailer],
	lg *slog.Logger,
	run di.Runner,
	opts ...func(*Server),
) *Server {
	return &Server{}
}

func NewRouteA() Route   { return Route{} }
func NewRouteB() Route   { return Route{} }
func NewMailer() *Mailer { return &Mailer{} }
func NewPg() *PgStore    { return &PgStore{} }

type Handler struct{}

func NewHandler(s Store) *Handler { return &Handler{} }

func vocabSatisfied() {
	stack.Run(
		di.Provide(NewRouteA, NewRouteB, NewMailer, NewServer),
	)
}

func lazyStillRequired() {
	stack.Run(
		di.Provide(NewRouteA, NewServer), // want `error\[DI0001\]: no provider for \*vocab.Mailer \(needed by NewServer\)`
	)
}

func bindSatisfiesInterface() {
	stack.Run(
		di.Bind[Store](NewPg),
		di.Provide(NewHandler),
	)
}
