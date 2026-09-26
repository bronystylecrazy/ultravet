// Package filenames pins UV0003.
package filenames // want `warning\[UV0003\]: handler_users.go is a layer-prefixed file`

import "github.com/bronystylecrazy/di"

type Users struct{}

func NewUsers() *Users { return &Users{} }

func Module() di.Reg {
	return di.Pkg(di.Provide(NewUsers))
}
