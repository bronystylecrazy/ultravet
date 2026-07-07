package ultravet

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// extractor turns registration expressions into regSummaries, resolving
// through local functions and imported-package facts.
type extractor struct {
	pass *analysis.Pass
	memo map[types.Object]*regSummary
}

// summarizeExpr resolves one registration-valued expression.
func (x *extractor) summarizeExpr(e ast.Expr) *regSummary {
	switch e := ast.Unparen(e).(type) {
	case *ast.CallExpr:
		return x.summarizeCall(e)
	case *ast.Ident, *ast.SelectorExpr:
		// A registration held in a variable (regs...) or package var:
		// not resolved in v1 — opaque, never guess.
		return &regSummary{Opaque: true}
	default:
		return &regSummary{Opaque: true}
	}
}

// summarizeCall handles di.* combinators and registration-returning
// function calls (local or imported via fact).
func (x *extractor) summarizeCall(call *ast.CallExpr) *regSummary {
	fn := calleeFunc(x.pass, call)
	if fn == nil {
		return &regSummary{Opaque: true}
	}
	pkg := ""
	if fn.Pkg() != nil {
		pkg = fn.Pkg().Path()
	}

	if pkg == diPath {
		return x.summarizeDICall(fn.Name(), call)
	}

	// A registration-returning function from another package or this one.
	if returnsRegistration(fn) || returnsRegistrationSlice(fn) {
		// Same package: summarize its body (memoized).
		if fn.Pkg() == x.pass.Pkg {
			if decl := x.localDecl(fn); decl != nil {
				return x.summarizeFunc(fn, decl)
			}
			return &regSummary{Opaque: true}
		}
		// Imported: consult the exported fact.
		var fact regFuncsFact
		if x.pass.ImportPackageFact(fn.Pkg(), &fact) {
			if s, ok := fact.Funcs[fn.Name()]; ok {
				return &s
			}
		}
		return &regSummary{Opaque: true}
	}
	return &regSummary{Opaque: true}
}

// summarizeDICall interprets the kernel's combinators.
func (x *extractor) summarizeDICall(name string, call *ast.CallExpr) *regSummary {
	out := &regSummary{}
	switch name {
	case "Provide", "Default":
		for _, arg := range call.Args {
			x.addConstructor(out, arg)
		}
	case "Bind":
		// di.Bind[Iface](ctor): provides the interface AND the concrete.
		if idx, ok := typeArg(x.pass, call); ok {
			out.Provides = append(out.Provides, typeString(idx))
		}
		for _, arg := range call.Args {
			x.addConstructor(out, arg)
		}
	case "Supply":
		for _, arg := range call.Args {
			if t := x.pass.TypesInfo.TypeOf(arg); t != nil {
				out.Provides = append(out.Provides, typeString(t))
			}
		}
	case "Module":
		for _, arg := range call.Args[1:] { // args[0] is the name
			merge(out, x.summarizeExpr(arg))
		}
	case "Options", "Global":
		for _, arg := range call.Args {
			merge(out, x.summarizeExpr(arg))
		}
	case "Export", "Decorate", "OnDemand", "StopTimeout", "NonCritical":
		// No provides/needs of their own (Decorate wraps existing types).
	case "PerKey", "Members":
		// Family/keyed constructors: their instances are keyed, consumed
		// via di.Keyed/di.Family (kernel-given) — nothing bare to check.
	case "Swap":
		// Test-only override: provides the swapped type.
		if idx, ok := typeArg(x.pass, call); ok {
			out.Provides = append(out.Provides, typeString(idx))
		}
	default:
		out.Opaque = true
	}
	return out
}

// addConstructor records a constructor's provides and needs.
func (x *extractor) addConstructor(out *regSummary, arg ast.Expr) {
	t := x.pass.TypesInfo.TypeOf(ast.Unparen(arg))
	sig, ok := t.(*types.Signature)
	if !ok {
		out.Opaque = true
		return
	}
	ctorName := "constructor"
	if fn := exprFunc(x.pass, arg); fn != nil {
		ctorName = fn.Name()
	}

	res := sig.Results()
	for i := 0; i < res.Len(); i++ {
		rt := res.At(i).Type()
		if isErrorType(rt) {
			continue
		}
		out.Provides = append(out.Provides, typeString(rt))
	}

	params := sig.Params()
	for i := 0; i < params.Len(); i++ {
		if sig.Variadic() && i == params.Len()-1 {
			continue // options: absence is fine
		}
		pt := params.At(i).Type()
		typ, kind, given := classifyDep(pt)
		if given {
			continue
		}
		out.Needs = append(out.Needs, need{Type: typ, By: ctorName, Kind: kind})
	}
}

// classifyDep unwraps the consumer-type vocabulary.
func classifyDep(t types.Type) (typeStr string, kind needKind, kernelGiven bool) {
	if named, ok := t.(*types.Named); ok && named.Obj().Pkg() != nil &&
		named.Obj().Pkg().Path() == diPath {
		args := named.TypeArgs()
		switch named.Obj().Name() {
		case "Many", "Optional":
			if args.Len() == 1 {
				return typeString(args.At(0)), needSoft, false
			}
		case "Lazy":
			if args.Len() == 1 {
				return typeString(args.At(0)), needHard, false
			}
		case "Runner", "Key", "Keyed", "Family", "Scope":
			return "", 0, true // kernel-given / separately validated
		}
	}
	if ptr, ok := t.(*types.Pointer); ok {
		if named, ok := ptr.Elem().(*types.Named); ok && named.Obj().Pkg() != nil &&
			named.Obj().Pkg().Path() == diPath && named.Obj().Name() == "App" {
			return "", 0, true
		}
	}
	return typeString(t), needHard, false
}

// summarizeFunc summarizes the body of a registration-returning function:
// resolvable only when every return expression is itself resolvable.
func (x *extractor) summarizeFunc(fn *types.Func, decl *ast.FuncDecl) *regSummary {
	if s, ok := x.memo[fn]; ok {
		return s
	}
	out := &regSummary{}
	x.memo[fn] = out // pre-set: recursion guard

	if decl.Body == nil {
		out.Opaque = true
		return out
	}
	ast.Inspect(decl.Body, func(n ast.Node) bool {
		ret, ok := n.(*ast.ReturnStmt)
		if !ok {
			return true
		}
		for _, res := range ret.Results {
			if lit, ok := ast.Unparen(res).(*ast.CompositeLit); ok {
				// return []di.Registration{...}
				for _, elt := range lit.Elts {
					merge(out, x.summarizeExpr(elt))
				}
				continue
			}
			merge(out, x.summarizeExpr(res))
		}
		return false
	})
	return out
}

func (x *extractor) localDecl(fn *types.Func) *ast.FuncDecl {
	for _, file := range x.pass.Files {
		for _, decl := range file.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok && fd.Name != nil &&
				x.pass.TypesInfo.Defs[fd.Name] == fn {
				return fd
			}
		}
	}
	return nil
}

// ---- small type helpers ----

func calleeFunc(pass *analysis.Pass, call *ast.CallExpr) *types.Func {
	return exprFunc(pass, call.Fun)
}

func exprFunc(pass *analysis.Pass, e ast.Expr) *types.Func {
	switch e := ast.Unparen(e).(type) {
	case *ast.Ident:
		fn, _ := pass.TypesInfo.Uses[e].(*types.Func)
		return fn
	case *ast.SelectorExpr:
		fn, _ := pass.TypesInfo.Uses[e.Sel].(*types.Func)
		return fn
	case *ast.IndexExpr: // generic instantiation: di.Bind[T]
		return exprFunc(pass, e.X)
	case *ast.IndexListExpr:
		return exprFunc(pass, e.X)
	}
	return nil
}

// typeArg extracts the first explicit type argument of a generic call
// like di.Bind[OrderRepo](...).
func typeArg(pass *analysis.Pass, call *ast.CallExpr) (types.Type, bool) {
	switch f := ast.Unparen(call.Fun).(type) {
	case *ast.IndexExpr:
		t := pass.TypesInfo.TypeOf(f.Index)
		return t, t != nil
	case *ast.IndexListExpr:
		if len(f.Indices) > 0 {
			t := pass.TypesInfo.TypeOf(f.Indices[0])
			return t, t != nil
		}
	}
	return nil, false
}

func returnsRegistration(fn *types.Func) bool {
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Results().Len() != 1 {
		return false
	}
	return isRegistrationType(sig.Results().At(0).Type())
}

func returnsRegistrationSlice(fn *types.Func) bool {
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Results().Len() != 1 {
		return false
	}
	sl, ok := sig.Results().At(0).Type().(*types.Slice)
	return ok && isRegistrationType(sl.Elem())
}

func isRegistrationType(t types.Type) bool {
	named, ok := t.(*types.Named)
	return ok && named.Obj().Pkg() != nil &&
		named.Obj().Pkg().Path() == diPath && named.Obj().Name() == "Registration"
}

func isErrorType(t types.Type) bool {
	named, ok := t.(*types.Named)
	return ok && named.Obj().Pkg() == nil && named.Obj().Name() == "error"
}

func typeString(t types.Type) string {
	return types.TypeString(t, nil)
}
