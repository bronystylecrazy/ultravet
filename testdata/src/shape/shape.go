package shape

import (
	"github.com/bronystylecrazy/di"
	"github.com/bronystylecrazy/ultrastack/stack"
)

type Config struct{}
type Store interface{ Get() string }
type FileStore struct{} // does NOT implement Store
type PgStore struct{}

func (p *PgStore) Get() string { return "" }

func doStuff()            {} // returns nothing
func NewFile() *FileStore { return &FileStore{} }
func NewPg() *PgStore     { return &PgStore{} }

func badProvideValue() {
	stack.Run(
		di.Provide(42), // want `error\[DI0010\]: di.Provide takes constructor functions, got int`
	)
}

func badCtorNoResults() {
	stack.Run(
		di.Provide(doStuff), // want `error\[DI0010\]: constructor doStuff returns nothing to provide`
	)
}

func badBind() {
	stack.Run(
		di.Bind[Store](NewFile), // want `error\[DI0007\]: \*shape.FileStore does not implement shape.Store — missing method Get`
	)
}

func goodBind() {
	stack.Run(
		di.Bind[Store](NewPg),
	)
}
