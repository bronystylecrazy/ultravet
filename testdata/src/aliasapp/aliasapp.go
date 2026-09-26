// Package aliasapp is the app.go shape. // want package:`regvars\(Modules\)`
// The feature list plus the cross-feature
// seams, which are di.Alias[I, T]() — a CONSUMER-declared interface bound to a
// concrete another feature provides. Until the analyzer modeled it, an Alias
// fell through to opaque and blanked the whole assembly: nothing below the
// feature list was checked at all, so a real gap surfaced only at boot.
package aliasapp

import (
	"github.com/bronystylecrazy/di"

	"aliasfeat"
)

// Recorder is declared HERE, by the consumer, and answered by a type this
// package never names a constructor for.
type Recorder interface{ Record(kmh float64) }

type Sink struct{}

// NewSink consumes the interface — resolved through the alias to
// *aliasfeat.Store, so no DI0001 for Recorder.
func NewSink(r Recorder) *Sink { return &Sink{} }

var Modules = di.Group(
	aliasfeat.Module,
	di.Provide(NewSink),
	di.Alias[Recorder, *aliasfeat.Store](),
)

// The assembly resolves: the alias answers Recorder, and the gap it lets the
// checks reach — *aliasfeat.Config — is reported instead of swallowed.
var _ = di.Validate(Modules) // want `error\[DI0001\]: no provider for \*aliasfeat.Config \(needed by NewStore\)`

// Meter is a second seam whose concrete NOBODY registers. The alias still
// provides the interface (so NewDial is not flagged), but the type it forwards
// to is a genuine gap and DI0001 names it at the alias: resolving an assembly
// never invents a provider.
type Meter interface{ Level() int }

type Dial struct{}

func NewDial(m Meter) *Dial { return &Dial{} }

var _ = di.Validate(
	di.Provide(NewDial),
	di.Alias[Meter, *aliasfeat.Gauge](), // want `error\[DI0001\]: no provider for \*aliasfeat.Gauge \(needed by di.Alias\[aliasapp.Meter, \*aliasfeat.Gauge\]\)`
)
