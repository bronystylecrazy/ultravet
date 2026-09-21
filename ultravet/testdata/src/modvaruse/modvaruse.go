// Package modvaruse is the payoff of the write-once law. // want package:`regvars\(Modules\)`
// An EXPORTED module
// var in a non-main package, and another package's module var reached through
// its fact, both resolve instead of turning the assembly opaque. Before that,
// the whole graph below the feature list went unchecked and the missing
// *modvar.Config surfaced only at boot.
package modvaruse

import (
	"github.com/bronystylecrazy/di"

	"modvar"
)

type Sink struct{}

// *modvar.Svc comes from modvar.Module — resolved across the package boundary.
func NewSink(s *modvar.Svc) *Sink { return &Sink{} }

var Modules = di.Group(modvar.Module, di.Provide(NewSink))

var _ = di.Validate(Modules) // want `error\[DI0001\]: no provider for \*modvar.Config \(needed by NewSvc\)`
