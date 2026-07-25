package lifetimes

import (
	"github.com/bronystylecrazy/ultrastack/di"
	"github.com/bronystylecrazy/ultrastack/stack"
)

type Config struct{}
type Tx struct{}
type Server struct{}
type Feed struct{}
type Router struct{}
type CamScope struct{ Tx *Tx }

func NewConfig() *Config     { return &Config{} }
func NewTx(cfg *Config) *Tx  { return &Tx{} }
func NewFeed(k di.Key) *Feed { return &Feed{} }

// The classic captive bug: a singleton holding one scope's Tx forever.
func NewServer(tx *Tx) *Server { return &Server{} }

func captive() {
	stack.Run(
		di.Provide(NewConfig),
		di.Scoped(NewTx),
		di.Provide(NewServer), // want `error\[DI0101\]: NewServer \(singleton\) depends on \*lifetimes.Tx \(scoped\)`
	)
}

// The right shape: hold the scope factory, Enter per operation.
func NewServerScoped(scopes di.Scope[CamScope]) *Server { return &Server{} }

func scopedCorrectly() {
	stack.Run(
		di.Provide(NewConfig),
		di.Scoped(NewTx),
		di.Provide(NewServerScoped),
	)
}

// Family members exist only between Spawn and Stop.
func NewRouter(f *Feed) *Router { return &Router{} }

func memberOutside() {
	stack.Run(
		di.Members(NewFeed),
		di.Provide(NewRouter), // want `error\[DI0106\]: NewRouter consumes family member \*lifetimes.Feed directly`
	)
}

// The right shape: act per key through the family.
func NewRouterFam(fams di.Family[CamScope]) *Router { return &Router{} }

func familyCorrectly() {
	stack.Run(
		di.Members(NewFeed),
		di.Provide(NewRouterFam),
	)
}
