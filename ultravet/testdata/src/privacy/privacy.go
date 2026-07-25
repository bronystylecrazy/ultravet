package privacy

import (
	"github.com/bronystylecrazy/ultrastack/di"
	"github.com/bronystylecrazy/ultrastack/stack"
)

type Config struct{}
type DB struct{}
type Repo struct{}
type Route struct{}

func NewConfig() *Config    { return &Config{} }
func NewDB(cfg *Config) *DB { return &DB{} }
func NewRepo(db *DB) *Repo  { return &Repo{} }

// Privacy is opt-in: this module exports *DB only; *Config stays inside.
func dbModule() di.Registration {
	return di.Module("database",
		di.Provide(NewConfig, NewDB),
		di.Export[*DB](),
	)
}

func consumerSeesExported() {
	stack.Run(
		dbModule(),
		di.Provide(NewRepo), // *DB is exported: fine
	)
}

type Auditor struct{}

func NewAuditor(cfg *Config) *Auditor { return &Auditor{} }

func consumerBlockedByPrivacy() {
	stack.Run(
		dbModule(),
		di.Provide(NewAuditor), // want `error\[DI0005\]: \*privacy.Config is provided inside module "database" but not exported — NewAuditor cannot see it`
	)
}

func newRoute() Route { return Route{} }

type Server struct{}

func NewServer(routes di.Many[Route]) *Server { return &Server{} }

// di.Global escapes module privacy — cross-cutting registries by design.
func globalEscapes() {
	stack.Run(
		di.Module("feature",
			di.Global(di.Provide(newRoute)),
			di.Provide(NewConfig),
			di.Export[*Config](),
		),
		di.Provide(NewServer),
	)
}
