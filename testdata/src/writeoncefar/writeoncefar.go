// Package writeoncefar is the cross-package half of UV0008: the write that
// the DECLARING package's pass can never see is reported here, in the pass
// over the writer. That is what makes summarizeVarObj's trust in an exported
// var sound build-wide — `ultra vet ./...` runs over both packages.
package writeoncefar

import (
	"github.com/bronystylecrazy/di"

	"writeonce"
)

func Hijack() {
	writeonce.Module = di.Options() // want `warning\[UV0008\]: writeonce.Module is assigned after its declaration`
	_ = &writeonce.Extra            // want `warning\[UV0008\]: &writeonce.Extra takes the address of a module var`
}
