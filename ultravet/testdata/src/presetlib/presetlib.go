// presetlib plays the role of a contrib preset. // want package:"regfuncs"
// once and its effect crosses packages as a fact.
package presetlib

import "github.com/bronystylecrazy/ultrastack/di"

type Pool struct{}

func newPool() *Pool { return &Pool{} }

func Module() di.Registration {
	return di.Module("presetlib",
		di.Provide(newPool),
		di.Export[*Pool](),
	)
}

// Opaque returns registrations the analyzer cannot see through.
func Opaque(dynamic []di.Registration) di.Registration {
	return di.Options(dynamic...)
}
