// Package nofix pins the BOUNDARY of `-fix`: every diagnostic here is real,
// and none of them carries a suggested fix, because no single edit is
// unambiguously correct. The absence of a nofix.go.golden IS the assertion —
// analysistest only reads a golden file for a package that produced edits.
package nofix

import (
	"github.com/bronystylecrazy/ultrastack/di"
	"github.com/bronystylecrazy/ultrastack/stack"
)

type Config struct{}
type DB struct{}
type Store interface{ Get() string }

// TWO constructors return *Config: registering either one compiles and boots a
// different graph, so DI0001 stays advisory here.
func NewConfig() *Config  { return &Config{} }
func LoadConfig() *Config { return &Config{} }

func NewDB(cfg *Config) *DB { return &DB{} }

func ambiguousConstructor() {
	stack.Run(
		di.Provide(NewDB), // want `error\[DI0001\]: no provider for \*nofix.Config \(needed by NewDB\)`
	)
}

// A Provide that MIXES constructors with a ready value has to be SPLIT, not
// renamed — which argument goes where is a judgement, so no fix.
func mixedProvide() {
	stack.Run(
		di.Provide(NewConfig, 42), // want `error\[DI0010\]: di.Provide takes constructor functions, got int`
	)
}

// Bind carries an interface facet that a rename to Supply would silently drop.
func bindValue() {
	stack.Run(
		di.Bind[Store](42), // want `error\[DI0010\]: di.Provide takes constructor functions, got int`
	)
}
