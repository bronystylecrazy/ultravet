package basic

import (
	"github.com/bronystylecrazy/ultrastack/di"
	"github.com/bronystylecrazy/ultrastack/stack"
)

type Config struct{ URL string }
type DB struct{ cfg *Config }
type Server struct{ db *DB }

func NewDB(cfg *Config) *DB    { return &DB{cfg: cfg} }
func NewServer(db *DB) *Server { return &Server{db: db} }
func NewConfig() *Config       { return &Config{} }

func missingProvider() {
	stack.Run(
		di.Provide(NewDB, NewServer), // want `error\[DI0001\]: no provider for \*basic.Config \(needed by NewDB\)`
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
	_, _ = di.New(
		di.Provide(NewConfig, NewConfig, NewDB), // want `error\[DI0004\]: 2 providers for \*basic.Config consumed bare by NewDB`
	)
}

// di.Validate is the covenant's assembly root — same checks as di.New.
func validated() {
	_ = di.Validate(
		di.Provide(NewDB), // want `error\[DI0001\]: no provider for \*basic.Config \(needed by NewDB\)`
	)
}

// The canonical root form: the assembly held in a package-level var,
// assigned exactly once — resolved through the var, checks intact.
var appRegs = di.Options(
	di.Provide(NewDB), // want `error\[DI0001\]: no provider for \*basic.Config \(needed by NewDB\)`
)

func varHeld() {
	stack.Run(appRegs)
}

// A reassigned var has more than one possible value: opaque, never guess —
// the broken graph inside must NOT be reported.
var mutRegs = di.Options(di.Provide(NewServer))

func mutate() { mutRegs = di.Options() }

func mutatedHeld() {
	stack.Run(mutRegs)
}
