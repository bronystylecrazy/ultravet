// Package blob pins UV0005: infra reaching up into the product.
package blob

import (
	"github.com/bronystylecrazy/ultrastack/di"

	"example.com/prod/internal/app/orders" // want `warning\[UV0005\]: infra package blob imports internal/app/orders — infra sits BELOW the product`
	"example.com/prod/internal/db"
	"example.com/prod/internal/util"
)

type Store struct{ _ *db.DB }

func New(d *db.DB) *Store { return &Store{d} }

func (s *Store) Key(o *orders.Orders) string { return util.Slug("blob") }

var _ = di.Provide
