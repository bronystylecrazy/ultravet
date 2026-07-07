// ultravet is the ultrastack static analyzer as a standalone binary —
// run directly, or as `go vet -vettool=$(which ultravet) ./...`.
package main

import (
	"golang.org/x/tools/go/analysis/singlechecker"

	"github.com/bronystylecrazy/ultrastack/analyzer/ultravet"
)

func main() { singlechecker.Main(ultravet.Analyzer) }
