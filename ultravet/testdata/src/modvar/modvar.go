// Package modvar is a feature in the taught v0.9.31 form. // want package:`regvars\(Module\)`
// Its whole wiring is
// ONE exported, write-once value. Nothing here flags — this is the clean side
// of UV0008, and the side an importer is allowed to resolve through.
package modvar

import "github.com/bronystylecrazy/ultrastack/di"

type Config struct{ DSN string }

type Svc struct{}

func NewSvc(c *Config) *Svc { return &Svc{} }

var Module = di.Module("modvar", di.Provide(NewSvc))
