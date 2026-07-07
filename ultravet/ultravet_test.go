package ultravet_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bronystylecrazy/ultrastack/analyzer/ultravet"
)

func TestUltravet(t *testing.T) {
	analysistest.Run(t, analysistest.TestData(), ultravet.Analyzer,
		"basic", "vocab", "presetlib", "presetuse", "opaque", "cycle", "dial")
}
