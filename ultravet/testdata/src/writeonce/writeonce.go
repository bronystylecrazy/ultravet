// Package writeonce pins UV0008: an exported package-level di.Reg var is
// written once, at its declaration, and its address is never taken.
package writeonce

import "github.com/bronystylecrazy/ultrastack/di"

type Svc struct{}

func New() *Svc { return &Svc{} }

// The clean form: composed at the declaration, never written again.
var Module = di.Module("writeonce", di.Provide(New))

// The lint is about the TYPE and the export, not the identifier "Module".
var Extra = di.Options(Module)

// Unexported: every writer is already visible to the pass, so the analyzer
// simply goes opaque. Nothing to warn about — and nothing below flags.
var internalReg = di.Provide(New)

// Declared bare and filled in later: the declaration is not the value.
var Late di.Reg

func init() {
	Module = di.Options(Module, di.Provide(New)) // want `warning\[UV0008\]: Module is assigned after its declaration`
	Late = di.Provide(New)                       // want `warning\[UV0008\]: Late is assigned after its declaration`
	internalReg = di.Provide(New)
}

func Rewire(on bool) {
	if on {
		Extra = di.Options(Extra) // want `warning\[UV0008\]: Extra is assigned after its declaration`
	}
	// A LOCAL named Module is not the package's value: no flag.
	Module := di.Provide(New)
	_ = Module
	take(&Extra) // want `warning\[UV0008\]: &Extra takes the address of a module var`
}

// A *di.Reg parameter is the mutation-adjacent escape: the only way to reach
// one is &Module, which is why the address check covers it.
func take(r *di.Reg) { _ = r }

// A struct field of type di.Reg has no package scope: no flag.
type bundle struct{ Module di.Reg }

func (b *bundle) set() { b.Module = di.Provide(New) }
