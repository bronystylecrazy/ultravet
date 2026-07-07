package ultravet

import (
	"go/ast"
	"go/types"

	"golang.org/x/tools/go/analysis"
)

// extractor turns registration expressions into regSummaries, resolving
// through local functions and imported-package facts.
type extractor struct {
	pass   *analysis.Pass
	memo   map[types.Object]*regSummary
	linted map[*ast.FuncDecl]bool
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

// addConstructor records a constructor's provides and needs, and lints
// its body for network calls (constructors never dial).
func (x *extractor) addConstructor(out *regSummary, arg ast.Expr) {
	t := x.pass.TypesInfo.TypeOf(ast.Unparen(arg))
	sig, ok := t.(*types.Signature)
	if !ok {
		out.Opaque = true
		return
	}
	ctor := ctorInfo{Name: "constructor"}
	fn := exprFunc(x.pass, arg)
	if fn != nil {
		ctor.Name = fn.Name()
	}

	res := sig.Results()
	for i := 0; i < res.Len(); i++ {
		rt := res.At(i).Type()
		if isErrorType(rt) {
			continue
		}
		ctor.Provides = append(ctor.Provides, typeString(rt))
	}

	params := sig.Params()
	for i := 0; i < params.Len(); i++ {
		if sig.Variadic() && i == params.Len()-1 {
			continue // options: absence is fine
		}
		pt := params.At(i).Type()
		dep, given := classifyDep(pt)
		if given {
			continue
		}
		dep.By = ctor.Name
		ctor.Needs = append(ctor.Needs, dep)
	}
	out.Ctors = append(out.Ctors, ctor)

	if fn != nil && fn.Pkg() == x.pass.Pkg {
		if decl := x.localDecl(fn); decl != nil {
			x.lintCtorBody(ctor.Name, decl)
		}
	}
}

// dialers are calls a constructor must not make — connection belongs in
// Start(ctx), where the boot timeout, parallelism, and health apply.
var dialers = map[string]map[string]bool{
	"net":      {"Dial": true, "DialTimeout": true, "DialTCP": true, "Listen": true, "ListenTCP": true},
	"net/http": {"Get": true, "Post": true, "PostForm": true, "Head": true},
	"github.com/jackc/pgx/v5/pgxpool": {"New": true, "NewWithConfig": true, "Connect": true},
	"google.golang.org/grpc":          {"Dial": true, "DialContext": true, "NewClient": true},
}

func (x *extractor) lintCtorBody(name string, decl *ast.FuncDecl) {
	if decl.Body == nil || x.linted[decl] {
		return
	}
	x.linted[decl] = true
	ast.Inspect(decl.Body, func(n ast.Node) bool {
		if _, isFn := n.(*ast.FuncLit); isFn {
			return false // closures run later (Runner, hooks): not a boot dial
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		fn := calleeFunc(x.pass, call)
		if fn == nil || fn.Pkg() == nil {
			return true
		}
		if names, ok := dialers[fn.Pkg().Path()]; ok && names[fn.Name()] {
			x.pass.Reportf(call.Pos(),
				"warning[UV0001]: constructor %s calls %s.%s — constructors never dial; connect in Start(ctx) so the boot timeout, parallel start, and health apply",
				name, fn.Pkg().Name(), fn.Name())
		}
		return true
	})
}

// classifyDep unwraps the consumer-type vocabulary.
func classifyDep(t types.Type) (dep need, kernelGiven bool) {
	if named, ok := t.(*types.Named); ok && named.Obj().Pkg() != nil &&
		named.Obj().Pkg().Path() == diPath {
		args := named.TypeArgs()
		switch named.Obj().Name() {
		case "Many", "Optional":
			if args.Len() == 1 {
				return need{Type: typeString(args.At(0)), Kind: needSoft}, false
			}
		case "Lazy":
			if args.Len() == 1 {
				// Still required — but the edge breaks cycles: that IS
				// the documented DI0003 fix.
				return need{Type: typeString(args.At(0)), Kind: needHard, Lazy: true}, false
			}
		case "Runner", "Key", "Keyed", "Family", "Scope":
			return need{}, true // kernel-given / separately validated
		}
	}
	if ptr, ok := t.(*types.Pointer); ok {
		if named, ok := ptr.Elem().(*types.Named); ok && named.Obj().Pkg() != nil &&
			named.Obj().Pkg().Path() == diPath && named.Obj().Name() == "App" {
			return need{}, true
		}
	}
	return need{Type: typeString(t), Kind: needHard}, false
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
