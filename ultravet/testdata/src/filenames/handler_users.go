// Package filenames pins UV0003. // want package:"regfuncs"
package filenames // want `warning\[UV0003\]: handler_users.go is a layer-prefixed file`

import "github.com/bronystylecrazy/ultrastack/di"

type Users struct{}

func NewUsers() *Users { return &Users{} }

func Module() di.Reg {
	return di.Pkg(di.Provide(NewUsers))
}
