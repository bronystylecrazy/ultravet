package ultravet_test

import (
	"testing"

	"golang.org/x/tools/go/analysis/analysistest"

	"github.com/bronystylecrazy/ultrastack/analyzer/ultravet"
)

func TestSuggestedFixes(t *testing.T) {
	analysistest.RunWithSuggestedFixes(t, analysistest.TestData(), ultravet.Analyzer, "fix")
}
