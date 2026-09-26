// Package grown pins UV0009's second trigger: a feature whose route table
// declares TWO distinct group prefixes — two resources in one package.
package grown // want `warning\[UV0009\]: feature grown declares 2 route-group prefixes \("/api/v1/exports", "/api/v1/grown"\)`

import "github.com/bronystylecrazy/ultrastack/web"

const ReadScope = web.Scope("grown.read")

type API struct{}

func (a *API) Handle(router web.Router) {
	g := router.Group("/api/v1/grown").Require(ReadScope)
	g.Get("/", nil)
	e := router.Group("/api/v1/exports").Require(ReadScope)
	e.Get("/", nil)
}
