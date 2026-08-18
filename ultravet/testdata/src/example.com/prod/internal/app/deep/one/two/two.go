// Package two pins UV0009's nesting cap: child packages go ONE level deep.
package two // want `warning\[UV0009\]: example.com/prod/internal/app/deep/one/two nests 3 levels under internal/app`

import "github.com/bronystylecrazy/ultrastack/di"

var _ = di.Provide
