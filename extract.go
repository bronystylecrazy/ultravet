package ultravet

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// extractor turns registration expressions into regSummaries, resolving
// through local functions and imported-package facts.
type extractor struct {
	pass    *analysis.Pass
	memo    map[types.Object]*regSummary
	varMemo map[*types.Var]*regSummary
	linted  map[*ast.FuncDecl]bool
}

// summarizeExpr resolves one registration-valued expression.
func (x *extractor) summarizeExpr(e ast.Expr) *regSummary {
	switch e := ast.Unparen(e).(type) {
	case *ast.CallExpr:
		return x.summarizeCall(e)
	case *ast.Ident:
		// The canonical root form holds the assembly in a package-level
		// var: cli.Run(App). Resolvable without guessing when every
		// possible writer is in this package and there is exactly one.
		if s := x.summarizeVar(e); s != nil {
			return s
		}
		return &regSummary{Opaque: true}
	case *ast.SelectorExpr:
		// Another package's module var: app.Modules, users.Module.
		if s := x.summarizeImportedVar(e); s != nil {
			return s
		}
		return &regSummary{Opaque: true}
	default:
		return &regSummary{Opaque: true}
	}
}

// summarizeVar resolves a registration held in a package-level var of
// THIS package — the canonical root form, cli.Run(App).
func (x *extractor) summarizeVar(id *ast.Ident) *regSummary {
	obj, ok := x.pass.TypesInfo.Uses[id].(*types.Var)
	// Package scope only: locals have dataflow we do not model.
	if !ok || obj.Pkg() != x.pass.Pkg || obj.Parent() != x.pass.Pkg.Scope() {
		return nil
	}
	return x.summarizeVarObj(obj)
}

// summarizeVarObj resolves a package-level registration var of THIS package:
// only when its declaration carries an initializer and nothing in the package
// reassigns it or takes its address. Any doubt returns nil (opaque).
//
// The writer scan sees every writer in this package, which settles unexported
// vars and anything in package main outright. For an EXPORTED var in a library
// package a foreign package could also write it, and no single pass can see
// that — the guarantee there is UV0008, which flags such a write in the pass
// over the WRITER (writeonce.go). That contract is what makes the v0.9.31 var
// form as analyzable as the `func Use() di.Reg` it replaced.
func (x *extractor) summarizeVarObj(obj *types.Var) *regSummary {
	if s, done := x.varMemo[obj]; done {
		return s
	}
	x.varMemo[obj] = nil // recursion guard: a self-referential var stays opaque
	init := x.varInit(obj)
	if init == nil || x.varWritten(obj) {
		return nil
	}
	s := x.summarizeExpr(init)
	x.varMemo[obj] = s
	return s
}

// summarizeImportedVar resolves pkg.Module through the declaring package's
// regVarsFact. The fact carries only the vars that package proved write-once,
// so one that is reassigned or addressed there is simply absent and stays
// opaque here — the trust never has to be re-derived across the boundary.
func (x *extractor) summarizeImportedVar(sel *ast.SelectorExpr) *regSummary {
	obj, ok := x.pass.TypesInfo.Uses[sel.Sel].(*types.Var)
	if !ok || obj.Pkg() == nil || obj.Pkg() == x.pass.Pkg ||
		obj.Parent() != obj.Pkg().Scope() {
		return nil
	}
	var fact regVarsFact
	if !x.pass.ImportPackageFact(obj.Pkg(), &fact) {
		return nil
	}
	s, ok := fact.Vars[obj.Name()]
	if !ok {
		return nil
	}
	return &s
}

// varInit finds the single initializer expression of a package-level var
// declaration — nil for tuple assignments (var a, b = f()) and bare
// declarations.
func (x *extractor) varInit(obj *types.Var) ast.Expr {
	for _, file := range x.pass.Files {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.VAR {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok || len(vs.Names) != len(vs.Values) {
					continue
				}
				for i, name := range vs.Names {
					if x.pass.TypesInfo.Defs[name] == obj {
						return vs.Values[i]
					}
				}
			}
		}
	}
	return nil
}

// varWritten reports whether anything in the package reassigns the var or
// takes its address — either one makes the initializer an unsafe answer.
func (x *extractor) varWritten(obj *types.Var) bool {
	written := false
	for _, file := range x.pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.AssignStmt:
				for _, lhs := range n.Lhs {
					if id, ok := ast.Unparen(lhs).(*ast.Ident); ok &&
						x.pass.TypesInfo.Uses[id] == obj {
						written = true
					}
				}
			case *ast.UnaryExpr:
				if n.Op != token.AND {
					return true
				}
				if id, ok := ast.Unparen(n.X).(*ast.Ident); ok &&
					x.pass.TypesInfo.Uses[id] == obj {
					written = true
				}
			}
			return !written
		})
		if written {
			return true
		}
	}
	return false
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
	case "Provide":
		for _, arg := range call.Args {
			if x.provideOptionArg(out, arg) {
				continue // di.As[...]() / di.NonCritical: not a constructor
			}
			x.addConstructor(out, arg, call, name)
		}
	case "Default":
		// di.Default(regs ...Registration) wraps REGISTRATIONS, not
		// constructors — it composes like Options and then stamps everything
		// it wrapped as auto-configuration. Reading its arguments as
		// constructors (as this case once did, sharing Provide's branch) is
		// what made every real di.Default(di.Provide(...)) a bogus DI0010.
		//
		// The stamp is a re-derivation, not an append: a nested di.Default
		// already recorded its types, and everything inside an outer Default
		// is default anyway — so each provided type is counted exactly once.
		inner := &regSummary{}
		for _, arg := range call.Args {
			merge(inner, x.summarizeExpr(arg))
		}
		inner.Defaults = inner.allProvides()
		merge(out, inner)
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
			x.addConstructor(out, arg, call, name)
			if haveIface {
				x.checkImplements(arg, iface)
			}
		}
	case "Alias":
		// di.Alias[I, T]() exposes the ALREADY-provided T as interface I — the
		// sanctioned cross-feature seam (UV0004's escape), declared in app.go
		// where the two features meet. The kernel builds it as
		// Provide(func(v T) I { return any(v).(I) }) (di/wrap.go), so the graph
		// models exactly that: one constructor providing I and needing T. A
		// consumer of I resolves through to T's provider; a T nobody provides
		// is still DI0001, at the alias.
		//
		// Both type parameters are inferable from nothing, so they are always
		// written out — an *ast.IndexListExpr with two indices.
		iface, okI := typeArgAt(x.pass, call, 0)
		concrete, okT := typeArgAt(x.pass, call, 1)
		if !okI || !okT {
			out.Opaque = true
			break
		}
		ctor := ctorInfo{
			Name: fmt.Sprintf("di.Alias[%s, %s]",
				shortType(typeString(iface)), shortType(typeString(concrete))),
			Provides: []string{typeString(iface)},
		}
		ctor.Needs = []need{{
			Type: typeString(concrete), By: ctor.Name, Kind: needHard,
			Pos: call.Pos(), End: call.End(),
		}}
		out.Ctors = append(out.Ctors, ctor)
	case "Supply":
		for _, arg := range call.Args {
			if t := x.pass.TypesInfo.TypeOf(arg); t != nil {
				out.Provides = append(out.Provides, typeString(t))
			}
		}
	case "Module", "Pkg":
		// Module carries its name; Pkg is named after the calling package
		// — which, during extraction, is the package under analysis (facts
		// summarize a Pkg call in its declaring package's pass).
		modName := "module"
		regArgs := call.Args
		if name == "Pkg" {
			modName = x.pass.Pkg.Name()
		} else if len(call.Args) > 0 {
			if lit, ok := ast.Unparen(call.Args[0]).(*ast.BasicLit); ok {
				modName = strings.Trim(lit.Value, `"`)
			}
			regArgs = call.Args[1:] // args[0] is the name
		}
		inner := &regSummary{}
		exports := map[string]bool{}
		hasExports := false
		for _, arg := range regArgs {
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
	case "Options", "Group", "Global", "Tolerate":
		// Group IS Options (di/provide.go) — the spelling app.go uses for the
		// feature list. Missing it made `var Modules = di.Group(...)` opaque.
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

// paramRange maps the i-th parameter of a signature to its range in the
// declaration's field list: the whole field (`cfg *Config`) normally, just
// the name when one field declares several parameters (`a, b *Config`).
// NoPos when the declaration is unknown (other package, method value).
func paramRange(params *ast.FieldList, i int) (pos, end token.Pos) {
	if params == nil {
		return token.NoPos, token.NoPos
	}
	at := 0
	for _, f := range params.List {
		span := max(len(f.Names), 1) // an unnamed field is one parameter
		if i < at+span {
			if len(f.Names) > 1 {
				name := f.Names[i-at]
				return name.Pos(), name.End()
			}
			return f.Pos(), f.End()
		}
		at += span
	}
	return token.NoPos, token.NoPos
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
	x.pass.Report(analysis.Diagnostic{
		Pos: arg.Pos(), End: arg.End(),
		Message: fmt.Sprintf("error[DI0007]: %s does not implement %s%s",
			typeString(concrete), shortType(typeString(ifaceT)), detail),
	})
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
// its body for network calls (constructors never dial). site is the
// enclosing di.Provide/di.Bind call and siteName its function name — the
// DI0010 fix is a rewrite OF that call, not of the argument.
func (x *extractor) addConstructor(out *regSummary, arg ast.Expr, site *ast.CallExpr, siteName string) {
	t := x.pass.TypesInfo.TypeOf(ast.Unparen(arg))
	sig, ok := t.(*types.Signature)
	if !ok {
		// DI0010 at the offending argument: values are Supply's job.
		d := analysis.Diagnostic{
			Pos: arg.Pos(), End: arg.End(),
			Message: fmt.Sprintf(
				"error[DI0010]: di.Provide takes constructor functions, got %s — for a ready value use di.Supply",
				typeString(x.pass.TypesInfo.TypeOf(ast.Unparen(arg)))),
		}
		if fix, ok := supplyFix(site, siteName, arg); ok {
			d.SuggestedFixes = []analysis.SuggestedFix{fix}
		}
		x.pass.Report(d)
		out.Opaque = true
		return
	}
	ctor := ctorInfo{Name: "constructor"}
	fn := exprFunc(x.pass, arg)
	if fn != nil {
		ctor.Name = fn.Name()
	}
	// The declaration's parameter list, when it lives in this package —
	// lets each need carry the exact parameter that created it.
	var declParams *ast.FieldList
	if lit, ok := ast.Unparen(arg).(*ast.FuncLit); ok {
		declParams = lit.Type.Params
	} else if fn != nil && fn.Pkg() == x.pass.Pkg {
		if decl := x.localDecl(fn); decl != nil {
			declParams = decl.Type.Params
		}
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
		x.pass.Report(analysis.Diagnostic{
			Pos: arg.Pos(), End: arg.End(),
			Message: fmt.Sprintf(
				"error[DI0010]: constructor %s returns nothing to provide — a constructor must return at least one non-error value",
				ctor.Name),
		})
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
		// The registration argument itself is the reportable range: the
		// `NewDB` in di.Provide(NewDB) — column-precise for this package.
		dep.Pos, dep.End = arg.Pos(), arg.End()
		dep.DeclPos, dep.DeclEnd = paramRange(declParams, i)
		ctor.Needs = append(ctor.Needs, dep)
	}
	out.Ctors = append(out.Ctors, ctor)

	if fn != nil && fn.Pkg() == x.pass.Pkg {
		if decl := x.localDecl(fn); decl != nil {
			x.lintCtorBody(ctor.Name, decl)
		}
	}
}

// supplyFix turns di.Provide(value) into di.Supply(value) — the exact edit
// DI0010's message prescribes, and a purely mechanical one: Supply takes the
// same variadic values and registers them ready-made, so the rename is the
// whole change.
//
// It is offered ONLY when the offending value is the call's single argument.
// A Provide that mixes constructors with a value has to be SPLIT (which
// argument goes where is a judgement), and rewriting di.Bind[I](value) to
// Supply would silently drop the I facet — neither is a rename, so neither
// gets a fix. A forwarded slice (di.Provide(vals...)) is skipped for the same
// reason: the elements, not the slice, are what Supply would take.
func supplyFix(site *ast.CallExpr, siteName string, arg ast.Expr) (analysis.SuggestedFix, bool) {
	if site == nil || siteName != "Provide" || site.Ellipsis.IsValid() ||
		len(site.Args) != 1 || site.Args[0] != arg {
		return analysis.SuggestedFix{}, false
	}
	var id *ast.Ident
	switch fn := ast.Unparen(site.Fun).(type) {
	case *ast.SelectorExpr:
		id = fn.Sel
	case *ast.Ident:
		id = fn // dot-imported di
	}
	if id == nil || id.Name != "Provide" {
		return analysis.SuggestedFix{}, false
	}
	return analysis.SuggestedFix{
		Message: "Register the ready value with di.Supply",
		TextEdits: []analysis.TextEdit{{
			Pos: id.Pos(), End: id.End(), NewText: []byte("Supply"),
		}},
	}, true
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
	return typeArgAt(pass, call, 0)
}

// typeArgAt extracts the i-th explicit type argument — di.Alias[I, T]() is
// the two-parameter form, an *ast.IndexListExpr.
func typeArgAt(pass *analysis.Pass, call *ast.CallExpr, i int) (types.Type, bool) {
	switch f := ast.Unparen(call.Fun).(type) {
	case *ast.IndexExpr:
		if i != 0 {
			return nil, false
		}
		t := pass.TypesInfo.TypeOf(f.Index)
		return t, t != nil
	case *ast.IndexListExpr:
		if i < len(f.Indices) {
			t := pass.TypesInfo.TypeOf(f.Indices[i])
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
	// Unalias first: di.Registration is an alias of di.Reg, and modern Go
	// materializes aliases as their own type nodes.
	named, ok := types.Unalias(t).(*types.Named)
	return ok && named.Obj().Pkg() != nil &&
		named.Obj().Pkg().Path() == diPath && named.Obj().Name() == "Reg"
}

func isErrorType(t types.Type) bool {
	named, ok := t.(*types.Named)
	return ok && named.Obj().Pkg() == nil && named.Obj().Name() == "error"
}

func typeString(t types.Type) string {
	return types.TypeString(t, nil)
}
