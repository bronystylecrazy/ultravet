// Package inner is a sub-package of the users feature: an edge INSIDE one
// feature is not a cross-feature edge; its own sibling import is listed as
// the note, under the parent feature's name.
package inner

import (
	"github.com/bronystylecrazy/di"

	"example.com/prod/internal/app/orders" // want `note\[UV0010\]: feature users depends on sibling feature orders`
)

func Count(o *orders.Orders) int { return o.N }

var _ = di.Provide
