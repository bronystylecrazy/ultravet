// Stub of the stack package for analysis tests.
package stack

import "github.com/bronystylecrazy/ultrastack/di"

func Run(regs ...di.Registration)                    {}
func New(regs ...di.Registration) (*di.App, error)   { return nil, nil }
func Validate(regs ...di.Registration) error         { return nil }
