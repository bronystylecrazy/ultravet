package presetuse

import (
	"presetlib"

	"github.com/bronystylecrazy/ultrastack/di"
	"github.com/bronystylecrazy/ultrastack/stack"
)

type Repo struct{ pool *presetlib.Pool }

func NewRepo(pool *presetlib.Pool) *Repo { return &Repo{pool: pool} }

func crossPackageSatisfied() {
	stack.Run(
		presetlib.Module(), // fact: provides *presetlib.Pool
		di.Provide(NewRepo),
	)
}

func crossPackageMissing() {
	stack.Run(
		di.Provide(NewRepo), // want `error\[DI0001\]: no provider for \*presetlib.Pool \(needed by NewRepo\)`
	)
}

type Pages struct{}

func NewPages(c *presetlib.Cache) *Pages { return &Pages{} }
func NewRedisCache() *presetlib.Cache    { return &presetlib.Cache{} }

// The preset's di.Default crossed the package boundary as a fact: the
// product's own *Cache overrides it instead of colliding with it.
func overridesPresetDefault() {
	stack.Run(
		presetlib.DefaultCache(),
		di.Provide(NewRedisCache, NewPages),
	)
}

func presetDefaultAlone() {
	stack.Run(
		presetlib.DefaultCache(),
		di.Provide(NewPages),
	)
}
