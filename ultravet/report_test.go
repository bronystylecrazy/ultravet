package ultravet

import (
	"encoding/json"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
)

// TestSplitHead covers the one head parser the renderer, the JSON emitter, the
// GitHub writer and the LSP all classify severity and code through.
func TestSplitHead(t *testing.T) {
	cases := []struct {
		msg      string
		severity string
		code     string
		rest     string
	}{
		{"error[DI0001]: no provider for *x.Config", "error", "DI0001", "no provider for *x.Config"},
		{`warning[UV0002]: permission "x" is required`, "warning", "UV0002", `permission "x" is required`},
		{"error[DI0106]: NewX consumes family member Y", "error", "DI0106", "NewX consumes family member Y"},
		{"no code here", "error", "", "no code here"},
		{"trailing [only open", "error", "", "trailing [only open"},
	}
	for _, c := range cases {
		sev, code, rest := SplitHead(c.msg)
		if sev != c.severity || code != c.code || rest != c.rest {
			t.Errorf("SplitHead(%q) = (%q, %q, %q), want (%q, %q, %q)",
				c.msg, sev, code, rest, c.severity, c.code, c.rest)
		}
	}
}

// TestPrimaryFixIsTheFixablePredicate pins the invariant every consumer leans
// on: a fix with no edits is not a fix, and Fixable is exactly "PrimaryFix
// exists".
func TestPrimaryFixIsTheFixablePredicate(t *testing.T) {
	empty := analysis.Diagnostic{SuggestedFixes: []analysis.SuggestedFix{{Message: "advice, no edit"}}}
	if Fixable(empty) {
		t.Error("a SuggestedFix with no TextEdits must not count as fixable")
	}
	real := analysis.Diagnostic{SuggestedFixes: []analysis.SuggestedFix{
		{Message: "advice, no edit"},
		{Message: "the real one", TextEdits: []analysis.TextEdit{{NewText: []byte("x")}}},
	}}
	fix, ok := PrimaryFix(real)
	if !ok || fix.Message != "the real one" {
		t.Errorf("PrimaryFix picked %q (ok=%v); want the first fix carrying edits", fix.Message, ok)
	}
	if !Fixable(real) {
		t.Error("Fixable must agree with PrimaryFix")
	}
}

// TestMarshalFindingsIsAlwaysAnArray is what lets a caller detect support by
// parsing: a clean run still prints a JSON array.
func TestMarshalFindingsIsAlwaysAnArray(t *testing.T) {
	for name, in := range map[string][]Finding{"nil": nil, "empty": {}} {
		out := MarshalFindings(in)
		if string(out) != "[]\n" {
			t.Errorf("%s: MarshalFindings = %q, want \"[]\\n\"", name, out)
		}
		var parsed []Finding
		if err := json.Unmarshal(out, &parsed); err != nil {
			t.Errorf("%s: not valid JSON: %v", name, err)
		}
	}
}

// TestSortFindingsIsTotal: two runs over the same source must emit
// byte-identical JSON, so the order cannot depend on traversal.
func TestSortFindingsIsTotal(t *testing.T) {
	in := []Finding{
		{File: "b.go", Line: 1, Col: 1, Message: "z"},
		{File: "a.go", Line: 9, Col: 1, Message: "a"},
		{File: "a.go", Line: 2, Col: 7, Message: "b"},
		{File: "a.go", Line: 2, Col: 3, Message: "c"},
		{File: "a.go", Line: 2, Col: 3, Message: "a"},
	}
	SortFindings(in)
	var got []string
	for _, f := range in {
		got = append(got, f.File+":"+f.Message)
	}
	want := []string{"a.go:a", "a.go:c", "a.go:b", "a.go:a", "b.go:z"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Errorf("SortFindings = %v, want %v", got, want)
	}
}

// TestGitHubAnnotations is the workflow-command contract: the level tracks
// severity, endColumn appears only on a single-line span, and the message is
// percent-escaped so a multi-line diagnostic stays ONE annotation.
func TestGitHubAnnotations(t *testing.T) {
	findings := []Finding{
		{
			Code: "DI0001", Severity: "error",
			Message: "error[DI0001]: no provider for *fix.Config",
			File:    "/repo/internal/app/app.go",
			Line:    16, Col: 14, EndLine: 16, EndCol: 19,
		},
		{
			Code: "UV0003", Severity: "warning",
			Message: "warning[UV0003]: handler_users.go is a layer-prefixed file",
			File:    "/repo/internal/app/users/handler_users.go",
			Line:    2, Col: 1, EndLine: 2, EndCol: 1,
		},
		{
			Code: "DI0003", Severity: "error",
			Message: "error[DI0003]: cycle:\nA → B → A — 100% avoidable, take di.Lazy",
			File:    "/repo/main.go",
			Line:    5, Col: 2, EndLine: 7, EndCol: 3,
		},
		{
			Code: "DI0002", Severity: "error",
			Message: "error[DI0002]: outside the checkout",
			File:    "/elsewhere/x.go",
			Line:    1, Col: 1, EndLine: 1, EndCol: 1,
		},
	}
	var b strings.Builder
	WriteGitHubAnnotations(&b, findings, "/repo")
	want := strings.Join([]string{
		"::error file=internal/app/app.go,line=16,col=14,endColumn=19,title=DI0001::error[DI0001]: no provider for *fix.Config",
		"::warning file=internal/app/users/handler_users.go,line=2,col=1,title=UV0003::warning[UV0003]: handler_users.go is a layer-prefixed file",
		"::error file=main.go,line=5,col=2,endLine=7,title=DI0003::error[DI0003]: cycle:%0AA → B → A — 100%25 avoidable, take di.Lazy",
		"::error file=/elsewhere/x.go,line=1,col=1,title=DI0002::error[DI0002]: outside the checkout",
		"",
	}, "\n")
	if b.String() != want {
		t.Errorf("annotations:\n--- got ---\n%s\n--- want ---\n%s", b.String(), want)
	}
}

// TestGitHubPropertyEscaping: a path with a comma or colon would otherwise end
// the property value early and corrupt every property after it.
func TestGitHubPropertyEscaping(t *testing.T) {
	var b strings.Builder
	WriteGitHubAnnotations(&b, []Finding{{
		Code: "DI0001", Severity: "error", Message: "boom",
		File: "weird,name:v2.go", Line: 1, Col: 1, EndLine: 1, EndCol: 1,
	}}, "/repo")
	if got, want := b.String(), "::error file=weird%2Cname%3Av2.go,line=1,col=1,title=DI0001::boom\n"; got != want {
		t.Errorf("escaping: got %q, want %q", got, want)
	}
}
