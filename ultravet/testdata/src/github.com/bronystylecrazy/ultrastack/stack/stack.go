// Stub of the stack package for analysis tests.
package stack

import "github.com/bronystylecrazy/ultrastack/di"

func Run(regs ...di.Registration)                  {}
func New(regs ...di.Registration) (*di.App, error) { return nil, nil }
func Validate(regs ...di.Registration) error       { return nil }

// Route mirrors the real stack.Route enough for the permission lint: a
// Require permission/policy string and a Public flag.
type Route struct {
	Pattern string
	Handler func()
	Require string
	Public  bool
}
