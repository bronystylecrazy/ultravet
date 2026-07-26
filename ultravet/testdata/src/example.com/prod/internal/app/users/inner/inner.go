// Package inner is a sub-package of the users feature: an edge INSIDE one
// feature is not a cross-feature edge, so nothing fires here or on the
// import of it.
package inner

import (
	"github.com/bronystylecrazy/ultrastack/di"

	"example.com/prod/internal/app/orders" // want `warning\[UV0004\]: feature users imports feature orders`
)

func Count(o *orders.Orders) int { return o.N }

var _ = di.Provide
