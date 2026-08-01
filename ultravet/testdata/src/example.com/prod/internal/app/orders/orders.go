// Package orders is a clean feature: infra and util only, no siblings.
package orders

import (
	"github.com/bronystylecrazy/ultrastack/di"

	"example.com/prod/internal/db"
	"example.com/prod/internal/util"
)

type Orders struct{ N int }

func New(d *db.DB) *Orders { return &Orders{} }

func (o *Orders) CountFor(id string) int { return len(util.Slug(id)) }

var _ = di.Provide

// The module name IS the package name — UV0007 when it drifts.
var Module = di.Module("order", di.Provide(New)) // want `warning\[UV0007\]: module name "order" is not the package name "orders"`
