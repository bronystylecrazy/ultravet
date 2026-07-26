// Package users pins UV0004: a feature reaching sideways into a feature.
package users

import (
	"github.com/bronystylecrazy/ultrastack/di"

	"example.com/prod/internal/app/orders" // want `warning\[UV0004\]: feature users imports feature orders — features never import features`
	"example.com/prod/internal/app/users/inner"
	"example.com/prod/internal/db"
	"example.com/prod/internal/util"
)

// Orders is what the law wants instead of the import above: the CONSUMER
// declares the contract, app.go binds the other feature's type to it.
type Orders interface {
	CountFor(id string) int
}

type Users struct{ _ *db.DB }

func New(d *db.DB) *Users { return &Users{d} }

func (u *Users) Slug(name string) string { return util.Slug(name) }

func (u *Users) Legacy(o *orders.Orders) int { return inner.Count(o) }

var _ = di.Provide
