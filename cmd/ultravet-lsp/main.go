// ultravet-lsp serves ultrastack wiring diagnostics over LSP (stdio) —
// run it beside gopls; editors merge diagnostics from both.
package main

import (
	"log"
	"os"

	"github.com/bronystylecrazy/ultravet/lsp"
)

func main() {
	log.SetOutput(os.Stderr)
	if err := lsp.New(os.Stdin, os.Stdout).Run(); err != nil {
		log.Fatal(err)
	}
}
