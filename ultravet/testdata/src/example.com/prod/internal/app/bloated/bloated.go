// Package bloated pins UV0009's first trigger: past the 8-file budget, the
// growth law says a child package. Test files never count.
package bloated // want `warning\[UV0009\]: feature bloated holds 9 non-test files`

import "github.com/bronystylecrazy/ultrastack/di"

var _ = di.Provide
