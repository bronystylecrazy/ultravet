// Package aliasfeat is the feature that owns the concrete. // want package:`regvars\(Module\)`
// It knows nothing about
// the interface a sibling feature declares — features never import features —
// so the seam cannot be attached here with di.As or di.Bind. It is bound where
// the two meet: app.go, with di.Alias.
package aliasfeat

import "github.com/bronystylecrazy/di"

type Config struct{ Dir string }

// Store is the concrete a consumer reaches through its own interface.
type Store struct{}

func (s *Store) Record(kmh float64) {}

// Gauge is registered by nobody — the target of the negative alias.
type Gauge struct{}

func (g *Gauge) Level() int { return 0 }

// NewStore needs a *Config nothing provides: the gap an opaque assembly
// swallowed, and the proof that resolution now reaches past the alias.
func NewStore(c *Config) *Store { return &Store{} }

var Module = di.Module("aliasfeat", di.Provide(NewStore))
