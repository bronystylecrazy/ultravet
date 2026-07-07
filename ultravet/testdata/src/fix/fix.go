package fix

import (
	"github.com/bronystylecrazy/ultrastack/di"
	"github.com/bronystylecrazy/ultrastack/stack"
)

type Config struct{}
type DB struct{}

func NewConfig() *Config     { return &Config{} }
func NewDB(cfg *Config) *DB  { return &DB{} }

func assemble() {
	stack.Run( // want `error\[DI0001\]: no provider for \*fix.Config \(needed by NewDB\)`
		di.Provide(NewDB),
	)
}
