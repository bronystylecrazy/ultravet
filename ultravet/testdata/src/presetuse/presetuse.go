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
