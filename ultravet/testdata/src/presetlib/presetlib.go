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

type Cache struct{}

func newMemCache() *Cache { return &Cache{} }

// DefaultCache is batteries-included auto-configuration: a product that
// provides its own *Cache wins and this one backs off. The default flag has
// to survive the exported fact for the importing package to know that.
func DefaultCache() di.Registration {
	return di.Default(di.Provide(newMemCache))
}

// Opaque returns registrations the analyzer cannot see through.
func Opaque(dynamic []di.Registration) di.Registration {
	return di.Options(dynamic...)
}
