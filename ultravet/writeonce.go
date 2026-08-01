package ultravet

// writeonce.go is UV0008: an exported package-level di.Reg var is written
// exactly ONCE — at its declaration — and its address is never taken.
//
// The v0.9.31 standard makes the graph a value all the way down:
//
//	var Module  = di.Module("zones", ...)      // one feature's whole wiring
//	var Modules = di.Group(zones.Module, ...)  // the assembly's feature list
//	var App     = ultra.New(app.Modules)       // the root
//
// Everything downstream reads the DECLARATION and believes it: ultravet's own
// resolution, `./app graph`, `ultra brief`, the route table. A second writer
// ends that — the value at assembly time is no longer the value at the
// declaration, so every static tool has to fall back to "opaque", and answers
// that used to be checked become runtime surprises instead.
//
// The lint is what makes the trust sound. summarizeVarObj resolves an exported
// var by scanning writers in the DECLARING package only; a foreign package
// could write it and that pass would never see it. UV0008 also flags writes to
// OTHER packages' exported Reg vars, so the violation is reported in the pass
// over the writer, and `ultra vet ./...` runs over both. The guarantee is
// build-wide even though no single pass can prove it alone.
//
// Scope: any EXPORTED package-level var of type di.Reg, whatever its name —
// the rationale is single-writer analyzability, and that is a property of the
// type and the export, not of the identifier `Module`. Unexported vars are
// deliberately NOT linted: every one of their writers is already visible to
// the pass, so summarizeVarObj simply goes opaque and nothing silently breaks.
// There is nothing to warn about that the analyzer cannot already see.

import (
	"go/ast"
	"go/token"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// checkWriteOnce emits UV0008 for a second write to, or the address of, an
// exported package-level registration var.
func checkWriteOnce(pass *analysis.Pass) {
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.AssignStmt:
				for _, lhs := range n.Lhs {
					name, ok := exportedRegVar(pass, lhs)
					if !ok {
						continue
					}
					pass.Report(analysis.Diagnostic{
						Pos: lhs.Pos(), End: lhs.End(),
						Message: "warning[UV0008]: " + name + " is assigned after its declaration — a module var is WRITE-ONCE: the assembly reads it as a VALUE, so a second writer makes ultravet, ./app graph and ultra brief treat this graph as opaque, and a route table that read as wired becomes a runtime surprise. Compose the extra registrations into a NEW var, or wrap them at the use site with di.Options(" + name + ", extra)",
					})
				}
			case *ast.UnaryExpr:
				if n.Op != token.AND {
					return true
				}
				name, ok := exportedRegVar(pass, n.X)
				if !ok {
					return true
				}
				pass.Report(analysis.Diagnostic{
					Pos: n.Pos(), End: n.End(),
					Message: "warning[UV0008]: &" + name + " takes the address of a module var — a *di.Reg is a handle to rewrite it later, so every static reader must assume the declaration is not what the assembly gets. Pass " + name + " by VALUE (a Reg included twice registers once, by identity); if the callee has registrations to add, have it RETURN a di.Reg and compose with di.Options(" + name + ", extra)",
				})
			}
			return true
		})
	}
}

// exportedRegVar returns the readable name of the exported package-level
// di.Reg var an expression denotes — `Module` here, `app.Modules` across
// packages. A struct field of type di.Reg has no package scope and a local
// named Module is not the package's value, so neither matches.
func exportedRegVar(pass *analysis.Pass, e ast.Expr) (string, bool) {
	var id *ast.Ident
	name := ""
	switch e := ast.Unparen(e).(type) {
	case *ast.Ident:
		id, name = e, e.Name
	case *ast.SelectorExpr:
		if x, ok := ast.Unparen(e.X).(*ast.Ident); ok {
			id, name = e.Sel, x.Name+"."+e.Sel.Name
		}
	}
	if id == nil {
		return "", false
	}
	v, _ := pass.TypesInfo.ObjectOf(id).(*types.Var)
	if v == nil || !v.Exported() || v.Pkg() == nil ||
		v.Parent() != v.Pkg().Scope() || !isRegistrationType(v.Type()) {
		return "", false
	}
	return name, true
}
