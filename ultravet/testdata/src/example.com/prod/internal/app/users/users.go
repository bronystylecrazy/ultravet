// Package users pins the v3 import law: a sideways feature import is LEGAL
// one direction, and the dependency surface is a note[UV0010] — never a ban
// (UV0004 is retired).
package users

import (
	"github.com/bronystylecrazy/ultrastack/di"

	"example.com/prod/internal/app/orders" // want `note\[UV0010\]: feature users depends on sibling feature orders`
	"example.com/prod/internal/app/users/inner"
	"example.com/prod/internal/db"
	"example.com/prod/internal/kind" // the features' shared vocabulary: legal here
	"example.com/prod/internal/util"
)

// Orders is the OPTIONAL seam: a consumer-declared interface app.go binds
// with di.Alias, when compile-time decoupling is wanted.
type Orders interface {
	CountFor(id string) int
}

type Users struct{ _ *db.DB }

func New(d *db.DB) *Users { return &Users{d} }

func (u *Users) Slug(name string) string { return util.Slug(name) }

func (u *Users) Wire(name string) kind.WireID { return kind.WireID(name) }

func (u *Users) Legacy(o *orders.Orders) int { return inner.Count(o) }

var _ = di.Provide
