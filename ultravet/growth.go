package ultravet

// growth.go is the feature ladder's rung 3 as a lint (UV0009) plus the scope
// vocabulary law (UV0011) — both from handler-standard v3.
//
// UV0009 — a feature package has outgrown one package. Two triggers:
//   - more than 8 non-test .go files, or
//   - a SECOND distinct route-group prefix (two prefixes is two resources).
//
// The exit is a child package, one level deep, wired by the parent's own
// di.Module — and one level is the cap, so a nesting deeper than that fires
// the same code.
//
// UV0011 — an inline scope literal in .Require. Scopes are contract surface:
// they live as named constants beside the error codes, not as literals at
// call sites.

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
)

const webPath = "github.com/bronystylecrazy/ultrastack/web"

// growthLimit is the file budget of one feature package: past it, the growth
// law says a child package (references/product.md, the feature ladder).
const growthLimit = 8

// checkGrowth emits UV0009 for feature packages under internal/app that have
// outgrown one package, and for nesting past the one-level cap.
func checkGrowth(pass *analysis.Pass) {
	if strings.HasSuffix(pass.Pkg.Path(), ".test") {
		return // the synthesized test-main package is nobody's feature
	}
	self := strings.TrimSuffix(pass.Pkg.Path(), "_test")
	_, rest, ok := splitInternal(self)
	if !ok || !onPlatform(pass.Pkg) {
		return
	}
	segs := strings.Split(rest, "/")
	if segs[0] != "app" || len(segs) < 2 {
		return
	}
	feature := strings.Join(segs[1:], "/")
	// Deterministic anchor: the lexicographically first file, so the finding
	// lands on <pkg>.go territory rather than wherever the loader started.
	at := pass.Files[0].Pos()
	first := pass.Fset.Position(at).Filename
	for _, f := range pass.Files[1:] {
		if name := pass.Fset.Position(f.Pos()).Filename; name < first {
			first, at = name, f.Pos()
		}
	}

	// One level max: internal/app/<feature>/<child> is the last rung.
	if len(segs) > 3 {
		pass.Reportf(at,
			"warning[UV0009]: %s nests %d levels under internal/app — child packages go ONE level deep. A child growing its own child is a feature trying to be born: promote it to internal/app/<name> and give it its line in app.go",
			self, len(segs)-1)
		return
	}

	files := 0
	for _, f := range pass.Files {
		name := filepath.Base(pass.Fset.Position(f.Pos()).Filename)
		if !strings.HasSuffix(name, "_test.go") {
			files++
		}
	}
	prefixes := groupPrefixes(pass)
	switch {
	case files > growthLimit:
		pass.Reportf(at,
			"warning[UV0009]: feature %s holds %d non-test files (the budget is %d) — grow a CHILD package: internal/app/%s/<child> with its own di.Module, wired by the parent's Module (one level max), never a line in app.go",
			feature, files, growthLimit, feature)
	case len(prefixes) > 1:
		pass.Reportf(at,
			"warning[UV0009]: feature %s declares %d route-group prefixes (%s) — two prefixes is two resources; grow a CHILD package: internal/app/%s/<child> with its own di.Module, wired by the parent's Module (one level max)",
			feature, len(prefixes), strings.Join(prefixes, ", "), feature)
	}
}

// groupPrefixes collects the distinct string-literal prefixes passed to
// Group(...) calls on the platform's routers (web.Router, api's Router).
// Literals only — a computed prefix is not evidence either way.
func groupPrefixes(pass *analysis.Pass) []string {
	seen := map[string]bool{}
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			fn := calleeFunc(pass, call)
			if fn == nil || fn.Name() != "Group" || fn.Pkg() == nil {
				return true
			}
			if p := fn.Pkg().Path(); p != webPath && !strings.HasPrefix(p, ultrastackPath+"/contrib/api") {
				return true
			}
			if lit, ok := call.Args[0].(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s, err := strconv.Unquote(lit.Value); err == nil {
					seen[s] = true
				}
			}
			return true
		})
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, strconv.Quote(p))
	}
	sort.Strings(out)
	return out
}

// checkScopeLiterals emits UV0011 for an inline scope literal in a web
// .Require call — a bare string, or a web.Scope("...") conversion at the call
// site. A named constant (the taught form) is an identifier and passes.
func checkScopeLiterals(pass *analysis.Pass) {
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Require" {
				return true
			}
			fn := calleeFunc(pass, call)
			if fn == nil || fn.Pkg() == nil || fn.Pkg().Path() != webPath {
				return true
			}
			if lit := scopeLiteral(pass, call.Args[0]); lit != "" {
				pass.Report(analysis.Diagnostic{
					Pos: call.Args[0].Pos(), End: call.Args[0].End(),
					Message: fmt.Sprintf(
						"warning[UV0011]: inline scope literal %s in .Require — scopes are contract surface: declare `const XScope = web.Scope(%s)` beside the feature's error codes (errors.go) and pass the constant",
						lit, lit),
				})
			}
			return true
		})
	}
}

// scopeLiteral returns the quoted literal behind a .Require argument when it
// is written inline: a bare string literal, or a web.Scope("...") conversion.
func scopeLiteral(pass *analysis.Pass, arg ast.Expr) string {
	switch e := ast.Unparen(arg).(type) {
	case *ast.BasicLit:
		if e.Kind == token.STRING {
			return e.Value
		}
	case *ast.CallExpr:
		// A conversion to web.Scope with a literal inside.
		if len(e.Args) != 1 {
			return ""
		}
		tv, ok := pass.TypesInfo.Types[e.Fun]
		if !ok || !tv.IsType() {
			return ""
		}
		named, ok := tv.Type.(*types.Named)
		if !ok || named.Obj().Pkg() == nil || named.Obj().Pkg().Path() != webPath || named.Obj().Name() != "Scope" {
			return ""
		}
		if lit, ok := ast.Unparen(e.Args[0]).(*ast.BasicLit); ok && lit.Kind == token.STRING {
			return lit.Value
		}
	}
	return ""
}
