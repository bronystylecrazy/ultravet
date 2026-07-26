package ultravet

// doctrine.go is product law #3 as a lint (references/product.md): the shape
// of the tree, checked statically.
//
//	<module>/internal/app/            the assembly — the ONE place that knows
//	                                  the feature list
//	<module>/internal/app/<feature>/  a feature — independently deletable
//	<module>/internal/<infra>/        infra (db/, blob/, ...) — below app
//	<module>/internal/util/           leaf helpers — imports nothing internal
//
// Three edges are illegal and all three are visible in the import PATHS
// alone — no type information, no guessing, no false positives:
//
//	UV0004  feature → feature   (the consumer declares an interface instead)
//	UV0005  infra   → app       (infra is below the product, never above it)
//	UV0006  util    → internal  (a leaf has no internal edges at all)
//
// Like UV0003, the checks only run inside modules on the platform, so a
// foreign package that happens to have an internal/app directory in the same
// build is never policed.

import (
	"go/types"
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
	// it, users_test importing users would read as a cross-feature edge.
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

	for _, file := range pass.Files {
		for _, spec := range file.Imports {
			if spec.Path == nil {
				continue
			}
			path := strings.Trim(spec.Path.Value, `"`)
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
				// UV0004 — a feature (app/<feature>/...) reaching sideways.
				if imported[0] != "app" || len(imported) < 2 || imported[1] == segs[1] {
					continue
				}
				pass.Report(analysis.Diagnostic{
					Pos: spec.Pos(), End: spec.End(),
					Message: "warning[UV0004]: feature " + segs[1] + " imports feature " + imported[1] +
						" — features never import features, so any one of them stays independently deletable. Declare the interface you need in " + segs[1] +
						" (the CONSUMER owns the contract), let " + imported[1] + " keep its concrete type, and join them in internal/app/app.go with di.Alias[" + segs[1] + ".Iface, *" + imported[1] + ".Impl]()",
				})
			default:
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
