// Package web is the analyzer-test stand-in for the v3 handler standard's
// module: the same import path and the shapes the growth and scope lints key
// on (Router.Group, Require, the Scope type). Behavior is irrelevant here.
package web

// Scope is a permission string — the type the scope-literal lint recognizes.
type Scope string

// Router is the collector handed to a feature's Handle.
type Router struct{}

// Group opens a route group under a static prefix.
func (Router) Group(prefix string, mw ...any) *Group { return &Group{} }

// Group is a route group.
type Group struct{}

// Require sets the group's default scope.
func (g *Group) Require(s Scope) *Group { return g }

// Get declares a route; the last argument is the handler.
func (g *Group) Get(path string, h ...any) *Entry { return &Entry{} }

// Entry is one declared route.
type Entry struct{}

// Require replaces the group default for this route.
func (e *Entry) Require(s Scope) *Entry { return e }
