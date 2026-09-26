package cycle

import (
	"github.com/bronystylecrazy/di"
	"github.com/bronystylecrazy/ultrastack/stack"
)

type A struct{}
type B struct{}
type C struct{}

func NewA(b *B) *A { return &A{} }
func NewB(c *C) *B { return &B{} }
func NewC(a *A) *C { return &C{} }

func hardCycle() {
	stack.Run( // want `error\[DI0003\]: dependency cycle: NewA → NewB → NewC → NewA`
		di.Provide(NewA, NewB, NewC),
	)
}

// The documented fix: one Lazy edge breaks the cycle.
func NewCLazy(a di.Lazy[*A]) *C { return &C{} }

func lazyBreaksIt() {
	stack.Run(
		di.Provide(NewA, NewB, NewCLazy),
	)
}
