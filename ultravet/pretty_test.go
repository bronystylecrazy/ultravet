package ultravet

import (
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
)

func TestRenderDiagnosticRustStyle(t *testing.T) {
	dir := t.TempDir()
	src := "package p\n\nfunc NewServer(cfg *Config) {}\n\nfunc assemble() {\n\tRun(x)\n}\n"
	path := filepath.Join(dir, "p.go")
	os.WriteFile(path, []byte(src), 0o644)

	fset := token.NewFileSet()
	f := fset.AddFile(path, -1, len(src))
	f.SetLinesForContent([]byte(src))
	// Pos of "Run" on line 6 col 2; related at "NewServer" line 3 col 6.
	primary := f.LineStart(6) + token.Pos(1)
	related := f.LineStart(3) + token.Pos(5)

	out := RenderDiagnostic(fset, analysis.Diagnostic{
		Pos:     primary,
		Message: "error[DI0001]: no provider for *p.Config (needed by NewServer) — add a di.Provide/Supply for it",
		Related: []analysis.RelatedInformation{{Pos: related, Message: "needed by NewServer, declared here"}},
		SuggestedFixes: []analysis.SuggestedFix{{Message: "Register NewConfig"}},
	}, false)

	for _, want := range []string{
		"error[DI0001]: no provider for *p.Config",
		"--> " + path + ":6:2",
		"6 |     Run(x)",
		"^^^",
		"3 | func NewServer(cfg *Config) {}",
		"needed by NewServer, declared here",
		"help: add a di.Provide/Supply for it",
		"fix: Register NewConfig",
		"more: ultra explain DI0001",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
}
