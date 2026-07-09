package permcheck

import (
	"context"

	"github.com/bronystylecrazy/ultrastack/contrib/auth"
	"github.com/bronystylecrazy/ultrastack/stack"
)

// Granted permissions (from config.toml [auth.roles]): "*", "speed.read",
// "zones.read", "zones.write", "cameras.*". The lint flags requirements no
// role grants, wildcard-aware, with a did-you-mean.

// Route-literal Require: covered permissions produce nothing.
func routesOK() {
	_ = stack.Route{Pattern: "GET /speed", Require: "speed.read"}
	_ = stack.Route{Pattern: "GET /cam", Require: "cameras.write"} // cameras.* covers it
	_ = stack.Route{Pattern: "GET /pub", Public: true}             // empty Require: fine
	_ = stack.Route{Pattern: "GET /own", Require: "policy:owner"}  // policy ref: not our job
}

// Route-literal Require: a typo of a granted permission.
func routeTypo() {
	_ = stack.Route{Pattern: "GET /s", Require: "speed.raed"} // want `permission "speed.raed" is required but no role in \[auth.roles\] grants it — did you mean "speed.read"`
}

// Route-literal Require: an entirely unknown permission (no near match).
func routeUnknown() {
	_ = stack.Route{Pattern: "DELETE /billing", Require: "billing.destroy"} // want `permission "billing.destroy" is required but no role in \[auth.roles\] grants it`
}

// Field assignment form.
func routeAssign() {
	var rt stack.Route
	rt.Require = "zones.remove" // want `permission "zones.remove" is required but no role in \[auth.roles\] grants it`
}

// Imperative auth.Require / auth.RequireAny with string literals.
func imperative(ctx context.Context) {
	_ = auth.Require(ctx, "zones.write")                  // granted
	_ = auth.Require(ctx, "reports.export")               // want `permission "reports.export" is required but no role in \[auth.roles\] grants it`
	_ = auth.RequireAny(ctx, "speed.read", "audit.viewe") // want `permission "audit.viewe" is required but no role in \[auth.roles\] grants it`
}
