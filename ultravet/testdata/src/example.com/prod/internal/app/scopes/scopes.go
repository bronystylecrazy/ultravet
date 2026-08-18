// Package scopes pins UV0011: scopes are named constants on the contract
// page, never literals at the .Require call site.
package scopes

import "github.com/bronystylecrazy/ultrastack/web"

const ReadScope = web.Scope("scopes.read")

type API struct{}

func (a *API) Handle(router web.Router) {
	g := router.Group("/api/v1/scopes").Require(ReadScope) // the taught form: clean
	g.Get("/", nil).Require(web.Scope("scopes.write"))     // want `warning\[UV0011\]: inline scope literal "scopes.write"`
	g.Get("/x", nil).Require("scopes.admin")               // want `warning\[UV0011\]: inline scope literal "scopes.admin"`
}
