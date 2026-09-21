// Package app is the assembly — the ONE place that knows the feature list,
// so it is exempt from UV0004/UV0005/UV0006. Importing every feature AND the
// infra it binds them to is its whole job.
package app

import (
	"github.com/bronystylecrazy/di"

	"example.com/prod/internal/app/orders"
	"example.com/prod/internal/app/users"
	"example.com/prod/internal/blob"
	"example.com/prod/internal/db"
)

var Modules = []di.Reg{
	di.Provide(db.New),
	di.Provide(blob.New),
	di.Provide(users.New),
	di.Provide(orders.New),
	// the cross-feature seam: the consumer's interface, the other feature's
	// concrete type — joined here and nowhere else.
	di.Bind[users.Orders](orders.New),
}
