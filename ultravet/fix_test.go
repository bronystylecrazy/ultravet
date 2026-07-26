package ultravet_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bronystylecrazy/ultrastack/analyzer/ultravet"
)

// TestSuggestedFixes verifies every machine-safe fix against a .golden file:
// DI0001's "register the local constructor" (block and one-line assemblies),
// DI0010's di.Provide→di.Supply rename, UV0002's permission-typo rewrite.
//
// `nofix` is the negative half — diagnostics deliberately left advisory. It has
// NO golden file, which is exactly the assertion: analysistest reads a golden
// only for a file some fix wanted to edit, so a fix appearing there fails the
// test with "error reading nofix.go.golden".
func TestSuggestedFixes(t *testing.T) {
	analysistest.RunWithSuggestedFixes(t, analysistest.TestData(), ultravet.Analyzer,
		"fix", "supplyfix", "permfix", "nofix")
}
