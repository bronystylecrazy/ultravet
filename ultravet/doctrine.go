package ultravet

// doctrine.go is product law #3 as a lint (references/product.md): the shape
// of the tree, checked statically.
//
//	<module>/internal/app/            the assembly — the ONE place that knows
//	                                  the feature list
//	<module>/internal/app/<feature>/  a feature — independently deletable
//	<module>/internal/kind/           optional shared vocabulary — a LEAF of
//	                                  values the features speak
//	<module>/internal/<infra>/        infra (db/, blob/, ...) — below app
//	<module>/internal/util/           leaf helpers — imports nothing internal
//
// The illegal edges are visible in the import PATHS alone — no type
// information, no guessing, no false positives:
//
//	UV0005  infra   → app       (infra is below the product, never above it)
//	UV0006  util    → internal  (a leaf has no internal edges at all)
//	UV0010  kind corrupted      (kind imports beyond stdlib, or a non-feature
//	                             package imports kind)
//
// Feature → feature is LEGAL one direction under handler-standard v3 (UV0004
// is retired as a ban): a would-be cycle cannot compile, and wanting one is a
// design signal with three exits — move the shared logic down, publish an
// event, or merge the features. The surface stays visible instead: each
// feature's sibling dependencies are listed as a note[UV0010], which never
// fails the run.
//
// Like UV0003, the checks only run inside modules on the platform, so a
// foreign package that happens to have an internal/app directory in the same
// build is never policed.

import (
	"go/ast"
	"go/token"
	"go/types"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/tools/go/analysis"
)

const (
	ultrastackPath = "github.com/bronystylecrazy/ultrastack"
	internalSeg    = "/internal/"
)

// checkDoctrine emits UV0004/UV0005/UV0006 for the illegal edges of the
// product tree.
func checkDoctrine(pass *analysis.Pass) {
	// External test packages carry a "_test" path suffix; without stripping
	// it, users_test importing users would read as a cross-feature edge. The
	// synthesized test-main package (".test") imports its package under test
	// the same way and is nobody's feature.
	if strings.HasSuffix(pass.Pkg.Path(), ".test") {
		return
	}
	self := strings.TrimSuffix(pass.Pkg.Path(), "_test")
	module, rest, ok := splitInternal(self)
	if !ok {
		return // not under a module's internal/ tree: no doctrine to apply
	}
	if !onPlatform(pass.Pkg) {
		return
	}
	prefix := module + internalSeg

	segs := strings.Split(rest, "/")
	// The assembly package (internal/app itself) is exempt from everything:
	// it is the one place that knows the feature list, and binding features
	// together is its whole job.
	if segs[0] == "app" && len(segs) == 1 {
		return
	}
	if segs[0] == "app" && len(segs) == 2 {
		checkModuleName(pass)
	}

	var siblings []string     // distinct sibling features this feature imports
	var firstSibling ast.Node // where the note lands
	seenSibling := map[string]bool{}
	for _, file := range pass.Files {
		for _, spec := range file.Imports {
			if spec.Path == nil {
				continue
			}
			path := strings.Trim(spec.Path.Value, `"`)
			if segs[0] == "kind" {
				// UV0010 — kind imports ONLY the standard library. Sub-packages
				// of kind stay inside the leaf.
				if isStdlib(path) || strings.HasPrefix(path, prefix+"kind") {
					continue
				}
				pass.Report(analysis.Diagnostic{
					Pos: spec.Pos(), End: spec.End(),
					Message: "error[UV0010]: internal/kind imports " + path +
						" — kind/ is the shared vocabulary LEAF: values only, stdlib imports only. Move the behavior to the package that owns it and keep the value types here",
				})
				continue
			}
			if !strings.HasPrefix(path, prefix) {
				continue // stdlib, third party, or another module: not ours
			}
			target := path[len(prefix):]
			imported := strings.Split(target, "/")
			switch {
			case segs[0] == "util":
				// UV0006 — util is a leaf. Splitting util into sub-packages
				// is fine; those edges stay inside the leaf.
				if imported[0] == "util" {
					continue
				}
				pass.Report(analysis.Diagnostic{
					Pos: spec.Pos(), End: spec.End(),
					Message: "warning[UV0006]: internal/" + rest + " imports internal/" + target +
						" — util/ is a leaf: it imports nothing internal, which is what lets every feature and every infra package use it without a cycle. Move the helper next to its only caller, or take the value as a parameter instead of importing the package that defines it",
				})
			case segs[0] == "app":
				// The v3 import law: sibling imports are LEGAL one direction —
				// listed as a note so the dependency surface stays visible,
				// never refused (a would-be cycle cannot compile).
				if imported[0] != "app" || len(imported) < 2 || imported[1] == segs[1] {
					continue
				}
				if !seenSibling[imported[1]] {
					seenSibling[imported[1]] = true
					siblings = append(siblings, imported[1])
				}
				if firstSibling == nil {
					firstSibling = spec
				}
			default:
				// UV0010 — kind is the FEATURES' vocabulary: an infra package
				// importing it means the type belongs one level further down.
				if imported[0] == "kind" {
					pass.Report(analysis.Diagnostic{
						Pos: spec.Pos(), End: spec.End(),
						Message: "error[UV0010]: infra package " + segs[0] + " imports internal/kind" +
							" — kind/ is the features' shared vocabulary, not the product's: move the type down into " + segs[0] + " (or util/) and let kind re-export it if the features still need the word",
					})
					continue
				}
				// UV0005 — infra reaching up into the product.
				if imported[0] != "app" {
					continue
				}
				pass.Report(analysis.Diagnostic{
					Pos: spec.Pos(), End: spec.End(),
					Message: "warning[UV0005]: infra package " + segs[0] + " imports internal/" + target +
						" — infra sits BELOW the product: features import db/, blob/, and their siblings, never the reverse. Declare the interface infra needs in " + segs[0] +
						" and bind the feature's type to it in internal/app/app.go with di.Alias, or move the shared type down into infra (or util/)",
				})
			}
		}
	}
	if len(siblings) > 0 {
		sort.Strings(siblings)
		pass.Report(analysis.Diagnostic{
			Pos: firstSibling.Pos(), End: firstSibling.End(),
			Message: "note[UV0010]: feature " + segs[1] + " depends on sibling " + plural("feature", siblings) +
				" — one direction is legal; a would-be cycle has three exits: move the shared logic down (internal/kind), publish an event, or merge the features",
		})
	}
}

// isStdlib reports whether an import path is the standard library: its first
// segment carries no dot — the same heuristic the toolchain's own vendoring
// uses.
func isStdlib(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

// plural renders "feature x" / "features x, y" for the sibling note.
func plural(noun string, names []string) string {
	if len(names) == 1 {
		return noun + " " + names[0]
	}
	return noun + "s " + strings.Join(names, ", ")
}

// checkModuleName emits UV0007 for a feature whose di.Module name is not its
// package name. Only feature packages are checked: a preset naming its module
// for the path it owns ("contrib/fib") is deliberate. The explicit name is the
// taught form precisely because it reads at the call — the price is that a
// rename can leave it behind, and this is that price, paid statically.
func checkModuleName(pass *analysis.Pass) {
	want := pass.Pkg.Name()
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			fn := calleeFunc(pass, call)
			if fn == nil || fn.Pkg() == nil || fn.Pkg().Path() != diPath || fn.Name() != "Module" {
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			got, err := strconv.Unquote(lit.Value)
			if err != nil || got == want {
				return true
			}
			pass.Report(analysis.Diagnostic{
				Pos: lit.Pos(), End: lit.End(),
				Message: "warning[UV0007]: module name " + strconv.Quote(got) + " is not the package name " +
					strconv.Quote(want) + " — the name is what every diagnostic, ./app graph and Explain call this slice of the graph, so a disagreement sends every reader to a directory that does not exist. Write di.Module(" +
					strconv.Quote(want) + ", ...), or di.Pkg(...) to self-name and have no string to drift",
			})
			return true
		})
	}
}

// splitInternal cuts an import path at its first "/internal/" segment:
// "example.com/prod/internal/app/users" → module "example.com/prod",
// rest "app/users". Using the FIRST segment on both sides of a comparison
// keeps a module whose own path contains "internal" self-consistent (it
// simply never matches the doctrine roles, so nothing fires).
func splitInternal(pkgPath string) (module, rest string, ok bool) {
	i := strings.Index(pkgPath, internalSeg)
	if i < 0 {
		return "", "", false
	}
	rest = pkgPath[i+len(internalSeg):]
	if rest == "" {
		return "", "", false
	}
	return pkgPath[:i], rest, true
}

// onPlatform reports whether the package reaches ultrastack at all —
// transitively, because a util/ package's violation is often an import of
// something internal that is itself the one holding the di dependency.
//
// The cost of gating this way is a miss, never a false positive: a corner of
// a product with no platform edge anywhere below it (a util/ importing a
// pure-Go internal package that imports nothing) is left alone. That is the
// right trade — a lint that fires on someone else's tree is unusable.
func onPlatform(pkg *types.Package) bool {
	seen := map[*types.Package]bool{}
	var walk func(p *types.Package) bool
	walk = func(p *types.Package) bool {
		if seen[p] {
			return false
		}
		seen[p] = true
		for _, imp := range p.Imports() {
			if strings.HasPrefix(imp.Path(), ultrastackPath) || walk(imp) {
				return true
			}
		}
		return false
	}
	return walk(pkg)
}
