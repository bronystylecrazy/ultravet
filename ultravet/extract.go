package ultravet

import (
	"fmt"
	"go/ast"
	"go/types"
	"strings"

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
			if x.provideOptionArg(out, arg) {
				continue // di.As[...]() / di.NonCritical: not a constructor
			}
			x.addConstructor(out, arg)
		}
	case "Bind":
		// di.Bind[Iface](ctor): provides the interface AND the concrete.
		iface, haveIface := typeArg(x.pass, call)
		if haveIface {
			out.Provides = append(out.Provides, typeString(iface))
		}
		for _, arg := range call.Args {
			if x.provideOptionArg(out, arg) {
				continue // Bind forwards trailing ProvideOptions to Provide
			}
			x.addConstructor(out, arg)
			if haveIface {
				x.checkImplements(arg, iface)
			}
		}
	case "Supply":
		for _, arg := range call.Args {
			if t := x.pass.TypesInfo.TypeOf(arg); t != nil {
				out.Provides = append(out.Provides, typeString(t))
			}
		}
	case "Module":
		modName := "module"
		if len(call.Args) > 0 {
			if lit, ok := ast.Unparen(call.Args[0]).(*ast.BasicLit); ok {
				modName = strings.Trim(lit.Value, `"`)
			}
		}
		inner := &regSummary{}
		exports := map[string]bool{}
		hasExports := false
		for _, arg := range call.Args[1:] { // args[0] is the name
			argCall, isCall := ast.Unparen(arg).(*ast.CallExpr)
			if isCall {
				if fn := calleeFunc(x.pass, argCall); fn != nil && fn.Pkg() != nil && fn.Pkg().Path() == diPath {
					switch fn.Name() {
					case "Export":
						hasExports = true
						if t, ok := typeArg(x.pass, argCall); ok {
							exports[typeString(t)] = true
						}
						continue
					case "Global":
						// Global escapes module privacy by design (cross-
						// cutting registries) — merge outside the wall.
						merge(out, x.summarizeCall(argCall))
						continue
					}
				}
			}
			merge(inner, x.summarizeExpr(arg))
		}
		if hasExports {
			// Privacy is opt-in: with exports declared, everything else
			// provided inside is invisible outside (DI0005 at the assembly).
			for i := range inner.Ctors {
				kept := inner.Ctors[i].Provides[:0]
				for _, p := range inner.Ctors[i].Provides {
					if exports[p] {
						kept = append(kept, p)
					} else {
						inner.Private = append(inner.Private, privateType{Type: p, Module: modName})
					}
				}
				inner.Ctors[i].Provides = kept
			}
			for i := range inner.Ctors {
				if inner.Ctors[i].Module == "" {
					inner.Ctors[i].Module = modName
				}
			}
			kept := inner.Provides[:0]
			for _, p := range inner.Provides {
				if exports[p] {
					kept = append(kept, p)
				} else {
					inner.Private = append(inner.Private, privateType{Type: p, Module: modName})
				}
			}
			inner.Provides = kept
		}
		merge(out, inner)
	case "Scoped":
		// Scoped ctors provide per-Enter values. Their needs may be seeds
		// (unprovided scope-struct fields), so we record only the provides
		// — consuming them from a SINGLETON is the captive bug (DI0101).
		for _, arg := range call.Args {
			out.Scoped = append(out.Scoped, x.resultTypes(arg)...)
		}
	case "Options", "Global", "Tolerate":
		// Tolerate wraps registrations that become NonCritical at runtime —
		// but criticality is a boot-time (Start-hook) property the wiring
		// checks do not model. For graph reconstruction its contents behave
		// exactly like Options: providers register normally and stay visible,
		// so a consumer of a Tolerate-wrapped provider is resolved, not
		// falsely flagged DI0001. (Tolerate is NOT opaque.)
		for _, arg := range call.Args {
			merge(out, x.summarizeExpr(arg))
		}
	case "Export", "Decorate", "OnDemand", "StopTimeout", "NonCritical":
		// No provides/needs of their own (Decorate wraps existing types).
	case "Members":
		// Family members exist only between Spawn and Stop; consuming one
		// bare from a singleton is DI0106.
		for _, arg := range call.Args {
			out.Member = append(out.Member, x.resultTypes(arg)...)
		}
	case "PerKey":
		// Keyed instances are consumed via di.Keyed (kernel-given) —
		// nothing bare to check.
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

// resultTypes lists a constructor's non-error result type strings —
// for pools whose needs we deliberately do not model (scoped, member).
func (x *extractor) resultTypes(arg ast.Expr) []string {
	sig, ok := x.pass.TypesInfo.TypeOf(ast.Unparen(arg)).(*types.Signature)
	if !ok {
		return nil
	}
	var out []string
	for i := 0; i < sig.Results().Len(); i++ {
		if rt := sig.Results().At(i).Type(); !isErrorType(rt) {
			out = append(out, typeString(rt))
		}
	}
	return out
}

// checkImplements verifies a Bind[I] constructor's concrete result
// actually satisfies I — DI0007 with the missing method named.
func (x *extractor) checkImplements(arg ast.Expr, ifaceT types.Type) {
	iface, ok := ifaceT.Underlying().(*types.Interface)
	if !ok {
		return // concrete re-binds are the runtime's business
	}
	sig, ok := x.pass.TypesInfo.TypeOf(ast.Unparen(arg)).(*types.Signature)
	if !ok || sig.Results().Len() == 0 {
		return // DI0010 handles the shape
	}
	concrete := sig.Results().At(0).Type()
	if isErrorType(concrete) || types.Implements(concrete, iface) {
		return
	}
	detail := ""
	if m, _ := types.MissingMethod(concrete, iface, true); m != nil {
		detail = fmt.Sprintf(" — missing method %s", m.Name())
	}
	x.pass.Reportf(arg.Pos(),
		"error[DI0007]: %s does not implement %s%s",
		typeString(concrete), shortType(typeString(ifaceT)), detail)
}

// provideOptionArg reports whether arg is a ProvideOption VALUE mixed into a
// Provide/Bind variadic list — di.As[Iface]() or di.NonCritical — rather than
// a constructor. The real kernel accepts these interleaved with constructors
// (see di.Provide), so treating them as constructors would emit a false
// DI0010. When arg is di.As[Iface](), the facet interface is registered as
// provided exactly as di.Bind[Iface] does, so a consumer of Iface resolves;
// other options (di.NonCritical) carry runtime-only meaning the wiring checks
// do not model and are skipped. Returns true when arg must NOT be treated as a
// constructor.
func (x *extractor) provideOptionArg(out *regSummary, arg ast.Expr) bool {
	// di.As[Iface]() — model the facet the same way Bind[Iface] does.
	if call, ok := ast.Unparen(arg).(*ast.CallExpr); ok {
		if fn := calleeFunc(x.pass, call); fn != nil && fn.Pkg() != nil &&
			fn.Pkg().Path() == diPath && fn.Name() == "As" {
			if iface, ok := typeArg(x.pass, call); ok {
				out.Provides = append(out.Provides, typeString(iface))
			}
			return true
		}
	}
	// Any other value statically typed di.ProvideOption (e.g. di.NonCritical).
	return isProvideOptionType(x.pass.TypesInfo.TypeOf(ast.Unparen(arg)))
}

// isProvideOptionType reports whether t is the kernel's di.ProvideOption.
func isProvideOptionType(t types.Type) bool {
	named, ok := t.(*types.Named)
	return ok && named.Obj().Pkg() != nil &&
		named.Obj().Pkg().Path() == diPath && named.Obj().Name() == "ProvideOption"
}

// addConstructor records a constructor's provides and needs, and lints
// its body for network calls (constructors never dial).
func (x *extractor) addConstructor(out *regSummary, arg ast.Expr) {
	t := x.pass.TypesInfo.TypeOf(ast.Unparen(arg))
	sig, ok := t.(*types.Signature)
	if !ok {
		// DI0010 at the offending argument: values are Supply's job.
		x.pass.Reportf(arg.Pos(),
			"error[DI0010]: di.Provide takes constructor functions, got %s — for a ready value use di.Supply",
			typeString(x.pass.TypesInfo.TypeOf(ast.Unparen(arg))))
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
	if len(ctor.Provides) == 0 {
		x.pass.Reportf(arg.Pos(),
			"error[DI0010]: constructor %s returns nothing to provide — a constructor must return at least one non-error value",
			ctor.Name)
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
	"net":                             {"Dial": true, "DialTimeout": true, "DialTCP": true, "Listen": true, "ListenTCP": true},
	"net/http":                        {"Get": true, "Post": true, "PostForm": true, "Head": true},
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
		case "Runner", "Key", "Keyed", "Family", "Scope", "Dependent":
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
