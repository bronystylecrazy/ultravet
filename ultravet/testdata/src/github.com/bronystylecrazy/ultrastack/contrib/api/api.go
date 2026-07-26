// Stub of contrib/api for analysis tests: only the route registrars and the
// options the UV0002 lint reads.
package api

import (
	"context"

	"github.com/bronystylecrazy/ultrastack/di"
)

type Operation struct {
	Require string
	Public  bool
}

type Option func(*Operation)

func Require(perm string) Option { return func(o *Operation) { o.Require = perm } }
func Public() Option             { return func(o *Operation) { o.Public = true } }

type Router struct{}

func Get[Req, Resp any](r *Router, path string, h func(context.Context, Req) (Resp, error), opts ...Option) {
}

func Post[Req, Resp any](r *Router, path string, h func(context.Context, Req) (Resp, error), opts ...Option) {
}

func Handle[Req, Resp any](name, methodAndPath string, h func(context.Context, Req) (Resp, error), opts ...Option) di.Reg {
	return di.Reg{}
}
