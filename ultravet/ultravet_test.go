package ultravet_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bronystylecrazy/ultrastack/analyzer/ultravet"
)

func TestUltravet(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ultravet.Analyzer,
		"basic", "vocab", "presetlib", "presetuse", "opaque", "cycle", "dial", "privacy", "shape", "lifetimes", "permcheck", "mqttperm", "tolerate", "provideopts", "defaults", "filenames")
}

// TestDoctrine runs the product-structure laws (UV0004 feature→feature,
// UV0005 infra→app, UV0006 util→internal) over a doctrine-shaped tree:
// example.com/prod is on the platform, example.com/foreign is not.
func TestDoctrine(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ultravet.Analyzer,
		"example.com/prod/internal/app",
		"example.com/prod/internal/app/users",
		"example.com/prod/internal/app/users/inner",
		"example.com/prod/internal/app/orders",
		"example.com/prod/internal/db",
		"example.com/prod/internal/blob",
		"example.com/prod/internal/util",
		"example.com/foreign/internal/app/a",
		"example.com/foreign/internal/app/b",
	)
}
