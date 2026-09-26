package ultravet_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bronystylecrazy/ultravet"
)

func TestUltravet(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ultravet.Analyzer,
		"basic", "vocab", "presetlib", "presetuse", "opaque", "cycle", "dial", "privacy", "shape", "lifetimes", "permcheck", "mqttperm", "tolerate", "provideopts", "defaults", "filenames")
}

// TestWriteOnce runs UV0008 over both halves of the law: the declaring package
// (clean var, reassignment, address, boundaries) and a foreign writer, whose
// pass is the only one that can see the write it performs.
func TestWriteOnce(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ultravet.Analyzer, "writeonce", "writeoncefar")
}

// TestModuleVarResolution is the payoff: an exported module var resolves —
// within its package and across one — instead of making the assembly opaque.
func TestModuleVarResolution(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ultravet.Analyzer, "modvar", "modvaruse")
}

// TestAliasSeam covers the sanctioned cross-feature seam: di.Alias[I, T]()
// resolves as a provider of I needing T, so an app.go that uses it is checked
// end to end (it used to go opaque) — without papering over a T nothing
// provides.
func TestAliasSeam(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ultravet.Analyzer, "aliasfeat", "aliasapp")
}

// TestDoctrine runs the product-structure laws (the UV0010 sibling notes and
// kind leaf rules, UV0005 infra→app, UV0006 util→internal) over a
// doctrine-shaped tree: example.com/prod is on the platform,
// example.com/foreign is not.
func TestDoctrine(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ultravet.Analyzer,
		"example.com/prod/internal/app",
		"example.com/prod/internal/app/users",
		"example.com/prod/internal/app/users/inner",
		"example.com/prod/internal/app/orders",
		"example.com/prod/internal/db",
		"example.com/prod/internal/blob",
		"example.com/prod/internal/kind",
		"example.com/prod/internal/util",
		"example.com/foreign/internal/app/a",
		"example.com/foreign/internal/app/b",
	)
}

// TestGrowthAndScopes runs the v3 growth law (UV0009: the file budget, the
// second route prefix, the one-level nesting cap) and the scope-literal law
// (UV0011) over feature-shaped fixtures.
func TestGrowthAndScopes(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ultravet.Analyzer,
		"example.com/prod/internal/app/grown",
		"example.com/prod/internal/app/scopes",
		"example.com/prod/internal/app/bloated",
		"example.com/prod/internal/app/deep/one/two",
	)
}

// TestSpanContext runs UV0012: a context used after a stack span was opened
// from it — and the forms that are not stale (discarded span, rebound ctx,
// closures before, out of scope, foreign Span methods).
func TestSpanContext(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ultravet.Analyzer, "spanctx")
}
