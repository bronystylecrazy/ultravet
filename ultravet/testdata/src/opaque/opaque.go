package opaque

import (
	"github.com/bronystylecrazy/ultrastack/di"
	"github.com/bronystylecrazy/ultrastack/stack"
)

type Config struct{}
type DB struct{}

func NewDB(cfg *Config) *DB { return &DB{} }

// Dynamic assembly: the analyzer must stay silent (no false positives) —
// Validate() covers this at test time.
func dynamicAssembly(extra []di.Registration) {
	regs := []di.Registration{di.Provide(NewDB)}
	regs = append(regs, extra...)
	stack.Run(regs...)
}

func helperVariable() {
	r := di.Provide(NewDB)
	stack.Run(r) // a registration in a variable: opaque, silent
}
