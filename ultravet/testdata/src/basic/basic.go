package basic

import (
	"github.com/bronystylecrazy/ultrastack/di"
	"github.com/bronystylecrazy/ultrastack/stack"
)

type Config struct{ URL string }
type DB struct{ cfg *Config }
type Server struct{ db *DB }

func NewDB(cfg *Config) *DB          { return &DB{cfg: cfg} }
func NewServer(db *DB) *Server       { return &Server{db: db} }
func NewConfig() *Config             { return &Config{} }

func missingProvider() {
	stack.Run( // want `error\[DI0001\]: no provider for \*basic.Config \(needed by NewDB\)`
		di.Provide(NewDB, NewServer),
	)
}

func satisfied() {
	stack.Run(
		di.Provide(NewConfig, NewDB, NewServer),
	)
}

func viaModuleAndSupply() {
	stack.Run(
		di.Module("db",
			di.Supply(&Config{}),
			di.Provide(NewDB),
		),
		di.Provide(NewServer),
	)
}

func ambiguous() {
	_, _ = di.New( // want `error\[DI0004\]: 2 providers for \*basic.Config consumed bare by NewDB`
		di.Provide(NewConfig, NewConfig, NewDB),
	)
}
