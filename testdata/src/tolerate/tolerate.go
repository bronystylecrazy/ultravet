package tolerate

import (
	"github.com/bronystylecrazy/di"
	"github.com/bronystylecrazy/ultrastack/stack"
)

type Config struct{ URL string }
type Sidecar struct{ cfg *Config }
type Server struct{ side *Sidecar }

func NewConfig() *Config              { return &Config{} }
func NewSidecar(cfg *Config) *Sidecar { return &Sidecar{cfg: cfg} }
func NewServer(side *Sidecar) *Server { return &Server{side: side} }

// A provider wrapped in di.Tolerate is still a provider: its result is
// visible to consumers. NewServer consumes *Sidecar, provided inside
// di.Tolerate, and must NOT be flagged DI0001 — Tolerate changes runtime
// criticality, not graph visibility.
func toleratedProviderIsVisible() {
	stack.Run(
		di.Provide(NewConfig),
		di.Tolerate(
			di.Provide(NewSidecar),
		),
		di.Provide(NewServer),
	)
}

// A whole module wrapped in Tolerate exports through the wall exactly as it
// would unwrapped: providers register normally, so a satisfied graph stays
// silent.
func toleratedModuleResolves() {
	stack.Run(
		di.Tolerate(
			di.Module("edge",
				di.Provide(NewConfig),
				di.Provide(NewSidecar),
			),
		),
		di.Provide(NewServer),
	)
}

// Tolerate does not hide missing providers: a real DI0001 inside a Tolerate
// is still reported (contents are not opaque). NewSidecar needs *Config,
// which nothing provides here.
func toleratedMissingStillReported() {
	stack.Run(
		di.Tolerate(
			di.Provide(NewSidecar), // want `error\[DI0001\]: no provider for \*tolerate.Config \(needed by NewSidecar\)`
		),
		di.Provide(NewServer),
	)
}
