package permcheck

import (
	"context"

	"github.com/bronystylecrazy/ultrastack/contrib/api"
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

// Route-literal Require: a typo of a granted permission. The full rendered
// message (a golden of the did-you-mean form) is asserted end to end.
func routeTypo() {
	_ = stack.Route{Pattern: "GET /s", Require: "speed.raed"} // want `^warning\[UV0002\]: permission "speed.raed" is required but no role in \[auth.roles\] grants it — did you mean "speed.read"\? Otherwise grant it to a role in config.toml$`
}

// Route-literal Require: an entirely unknown permission (no near match). Full
// rendered message asserted (the no-match form ends with the grant-it fix).
func routeUnknown() {
	_ = stack.Route{Pattern: "DELETE /billing", Require: "billing.destroy"} // want `^warning\[UV0002\]: permission "billing.destroy" is required but no role in \[auth.roles\] grants it — add it to a role's grants in config.toml \[auth.roles\], or fix the requirement$`
}

// Field assignment form.
func routeAssign() {
	var rt stack.Route
	rt.Require = "zones.remove" // want `^warning\[UV0002\]: permission "zones.remove" is required but no role in \[auth.roles\] grants it — add it to a role's grants in config.toml \[auth.roles\], or fix the requirement$`
}

// contrib/api route options: an api.Require string literal in a Get/Post/…
// or Handle option list is a requirement at that call site; api.Public
// requires nothing and policy: refs stay the boot check's job.
func apiRoutes(r *api.Router) {
	api.Get(r, "/speed", h, api.Require("speed.read"))                       // granted
	api.Get(r, "/cams", h, api.Require("cameras.write"))                     // cameras.* covers it
	api.Get(r, "/pub", h, api.Public())                                      // public: fine
	api.Get(r, "/own", h, api.Require("policy:owner"))                       // policy ref: not our job
	_ = api.Handle("zones.list", "GET /zones", h, api.Require("zones.read")) // granted
	api.Post(r, "/audit", h, api.Require("audit.view"))                      // want `^warning\[UV0002\]: permission "audit.view" is required but no role in \[auth.roles\] grants it — add it to a role's grants in config.toml \[auth.roles\], or fix the requirement$`
	api.Get(r, "/zones", h, api.Require("zones.wrte"))                       // want `^warning\[UV0002\]: permission "zones.wrte" is required but no role in \[auth.roles\] grants it — did you mean "zones.write"\? Otherwise grant it to a role in config.toml$`
}

func h(ctx context.Context, in struct{}) (struct{}, error) { return struct{}{}, nil }

// Imperative auth.Require / auth.RequireAny with string literals.
func imperative(ctx context.Context) {
	_ = auth.Require(ctx, "zones.write")                  // granted
	_ = auth.Require(ctx, "reports.export")               // want `^warning\[UV0002\]: permission "reports.export" is required but no role in \[auth.roles\] grants it — add it to a role's grants in config.toml \[auth.roles\], or fix the requirement$`
	_ = auth.RequireAny(ctx, "speed.read", "audit.viewe") // want `^warning\[UV0002\]: permission "audit.viewe" is required but no role in \[auth.roles\] grants it — add it to a role's grants in config.toml \[auth.roles\], or fix the requirement$`
}
