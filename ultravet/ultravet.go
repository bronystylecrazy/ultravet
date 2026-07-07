// Package ultravet is the static analyzer: the Rust-compiler experience at
// keystroke time. It reconstructs the dependency graph from source — the
// registration API is declarative, so di.Provide(NewDB) is analyzable — and
// runs wiring checks before anything compiles, let alone boots:
//
//	error[DI0001]: no provider for *pg.Config (needed by NewDB)
//
// reported at the assembly site, in the editor or CI, with the same codes
// `ultra explain` teaches.
//
// Honesty is the design constraint: when an assembly contains anything the
// analyzer cannot resolve statically (registrations built in loops, opaque
// helper calls), missing-provider checks for that assembly are suppressed
// rather than guessed — Validate() remains the runtime covenant; ultravet
// is the earlier, zero-false-positive net.
//
// Usage:
//
//	go run github.com/bronystylecrazy/ultrastack/analyzer/cmd/ultravet@latest ./...
//	go vet -vettool=$(which ultravet) ./...
package ultravet

import (
	"fmt"
	"go/ast"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis"

	"github.com/bronystylecrazy/ultrastack/di/graphcheck"
)

var Analyzer = &analysis.Analyzer{
	Name:      "ultravet",
	Doc:       "static wiring checks for ultrastack dependency graphs (DI0001 missing providers, DI0004 ambiguity, DI0003 cycles, DI0005 module privacy, DI0007 bad binds, DI0010 bad constructors, DI0101 captive scoped deps, DI0106 family members outside — before boot)",
	Run:       run,
	FactTypes: []analysis.Fact{new(regFuncsFact)},
}

const (
	diPath    = "github.com/bronystylecrazy/ultrastack/di"
	stackPath = "github.com/bronystylecrazy/ultrastack/stack"
	cliPath   = "github.com/bronystylecrazy/ultrastack/cli"
)

// regSummary is the statically-extracted effect of one registration
// expression or registration-returning function.
type regSummary struct {
	Ctors    []ctorInfo    // per-constructor granularity (cycle detection)
	Provides []string      // extra provided types (Supply, Bind[I] facets)
	Scoped   []string      // provided per-Enter by di.Scoped ctors
	Member   []string      // provided per-Spawn by di.Members ctors
	Private  []privateType // provided inside an exporting module, unexported
	Opaque   bool          // something was not statically resolvable
}

// privateType is a type trapped behind module privacy (DI0005).
type privateType struct {
	Type   string
	Module string
}

// ctorInfo is one constructor's contract.
type ctorInfo struct {
	Name     string
	Module   string // named module it lives in ("" = top level)
	Provides []string
	Needs    []need
}

type need struct {
	Type   string
	By     string // constructor name, for the message
	Module string // the consumer's module: insiders see private types
	Kind   needKind
	Lazy   bool // di.Lazy: still required, but breaks cycles
}

type needKind int

const (
	needHard needKind = iota // bare or Lazy: must exist
	needSoft                 // Optional / Many / variadic: absence is fine
)

func (s *regSummary) allProvides() []string {
	out := append([]string{}, s.Provides...)
	for _, c := range s.Ctors {
		out = append(out, c.Provides...)
	}
	return out
}

func (s *regSummary) allNeeds() []need {
	var out []need
	for _, c := range s.Ctors {
		for _, n := range c.Needs {
			n.Module = c.Module
			out = append(out, n)
		}
	}
	return out
}

// regFuncsFact carries, per package, the summaries of exported functions
// that return di.Registration — how preset knowledge crosses packages.
type regFuncsFact struct {
	Funcs map[string]regSummary
}

func (*regFuncsFact) AFact() {}

func (f *regFuncsFact) String() string {
	names := make([]string, 0, len(f.Funcs))
	for n := range f.Funcs {
		names = append(names, n)
	}
	sort.Strings(names)
	return "regfuncs(" + strings.Join(names, ",") + ")"
}

func run(pass *analysis.Pass) (any, error) {
	x := &extractor{pass: pass, memo: map[types.Object]*regSummary{}, linted: map[*ast.FuncDecl]bool{}}

	// 1. Summarize this package's exported registration-returning funcs
	//    and export the fact for downstream packages.
	fact := &regFuncsFact{Funcs: map[string]regSummary{}}
	for _, file := range pass.Files {
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || !fd.Name.IsExported() {
				continue
			}
			obj, _ := pass.TypesInfo.Defs[fd.Name].(*types.Func)
			if obj == nil || !returnsRegistration(obj) {
				continue
			}
			fact.Funcs[fd.Name.Name] = *x.summarizeFunc(obj, fd)
		}
	}
	if len(fact.Funcs) > 0 {
		pass.ExportPackageFact(fact)
	}

	// 2. Find assembly roots and check each one.
	for _, file := range pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if !isAssemblyRoot(pass, call) {
				return true
			}
			x.checkAssembly(call)
			return true
		})
	}
	return nil, nil
}

// isAssemblyRoot recognizes di.New / stack.Run / stack.New /
// stack.Validate / cli.Main / cli.Test — the places a whole graph is
// declared.
func isAssemblyRoot(pass *analysis.Pass, call *ast.CallExpr) bool {
	fn := calleeFunc(pass, call)
	if fn == nil || fn.Pkg() == nil {
		return false
	}
	switch fn.Pkg().Path() {
	case diPath:
		return fn.Name() == "New"
	case stackPath:
		return fn.Name() == "Run" || fn.Name() == "New" || fn.Name() == "Validate"
	case cliPath:
		return fn.Name() == "Main" || fn.Name() == "Test"
	}
	return false
}

// checkAssembly resolves every registration argument, unions the
// summaries, and reports wiring errors — unless anything was opaque.
func (x *extractor) checkAssembly(call *ast.CallExpr) {
	total := regSummary{}
	args := call.Args
	// cli.Test(args, regs...) has a leading non-registration arg.
	if fn := calleeFunc(x.pass, call); fn != nil && fn.Name() == "Test" && len(args) > 0 {
		args = args[1:]
	}
	for _, arg := range args {
		s := x.summarizeExpr(arg)
		merge(&total, s)
	}
	if total.Opaque {
		return // Validate() covers what we cannot see; never guess
	}

	provided := map[string]int{}
	for _, p := range total.allProvides() {
		provided[p]++
	}
	for _, k := range kernelGivens {
		provided[k]++
	}

	private := map[string]string{} // type → module holding it captive
	for _, p := range total.Private {
		private[p.Type] = p.Module
	}
	scoped := map[string]bool{}
	for _, s := range total.Scoped {
		scoped[s] = true
	}
	member := map[string]bool{}
	for _, m := range total.Member {
		member[m] = true
	}

	seen := map[string]bool{}
	for _, n := range total.allNeeds() {
		// Discovery is ours (visibility, pools); MEANING is shared with
		// the runtime resolver via graphcheck — parity by construction.
		kind := graphcheck.Bare
		if n.Kind == needSoft {
			kind = graphcheck.Many // soft: Many/Optional/variadic all tolerate absence
		} else if n.Lazy {
			kind = graphcheck.Lazy
		}
		cands := graphcheck.Candidates{Singleton: provided[n.Type]}
		if mod, trapped := private[n.Type]; trapped {
			if mod == n.Module {
				cands.Singleton++ // insiders see their module's private types
			} else {
				cands.Unexported++
			}
		}
		if scoped[n.Type] {
			cands.Scoped++
		}
		if member[n.Type] {
			cands.Member++
		}
		verdict := graphcheck.Resolve(kind, cands)
		if verdict == graphcheck.OK {
			continue
		}
		key := verdict.Code() + ":" + n.Type + ":" + n.By
		if verdict == graphcheck.Ambiguous {
			key = verdict.Code() + ":" + n.Type
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		switch verdict {
		case graphcheck.Missing:
			x.reportMissing(call, n)
		case graphcheck.Ambiguous:
			x.pass.Reportf(call.Pos(),
				"error[DI0004]: %d providers for %s consumed bare by %s — collect them with di.Many[%s], or remove the extras",
				cands.Singleton, shortType(n.Type), n.By, shortType(n.Type))
		case graphcheck.NotExported:
			x.reportAt(call, n, fmt.Sprintf(
				"error[DI0005]: %s is provided inside module %q but not exported — %s cannot see it; add di.Export[%s]() to that module",
				shortType(n.Type), private[n.Type], n.By, shortType(n.Type)))
		case graphcheck.Captive:
			x.reportAt(call, n, fmt.Sprintf(
				"error[DI0101]: %s (singleton) depends on %s (scoped) — a singleton would capture one scope's instance forever; hold di.Scope[YourScope] and Enter per operation",
				n.By, shortType(n.Type)))
		case graphcheck.MemberOutside:
			x.reportAt(call, n, fmt.Sprintf(
				"error[DI0106]: %s consumes family member %s directly — members exist only between Spawn and Stop; take di.Family[S] and act per key",
				n.By, shortType(n.Type)))
		}
	}

	x.checkCycles(call, &total)
}

// reportAt emits a graph error with the consumer's declaration as a
// related span — the rustc-style "declared here" secondary.
func (x *extractor) reportAt(call *ast.CallExpr, n need, msg string) {
	d := analysis.Diagnostic{Pos: call.Pos(), Message: msg}
	if decl := x.declByName(n.By); decl != nil {
		d.Related = append(d.Related, analysis.RelatedInformation{
			Pos:     decl.Name.Pos(),
			Message: fmt.Sprintf("needed by %s, declared here", n.By),
		})
	}
	x.pass.Report(d)
}

// reportMissing emits DI0001 — with a one-click fix when an unregistered
// constructor for the missing type exists in this package.
func (x *extractor) reportMissing(call *ast.CallExpr, n need) {
	d := analysis.Diagnostic{
		Pos: call.Pos(),
		Message: fmt.Sprintf(
			"error[DI0001]: no provider for %s (needed by %s) — add a di.Provide/Supply for it, or take di.Optional[%s]",
			shortType(n.Type), n.By, shortType(n.Type)),
	}
	// Point at the consumer too — "needed by NewServer, declared here".
	if decl := x.declByName(n.By); decl != nil {
		d.Related = append(d.Related, analysis.RelatedInformation{
			Pos:     decl.Name.Pos(),
			Message: fmt.Sprintf("needed by %s, declared here", n.By),
		})
	}
	if ctor, qual := x.findLocalConstructor(call, n.Type); ctor != "" {
		d.SuggestedFixes = []analysis.SuggestedFix{{
			Message: fmt.Sprintf("Register %s, which provides %s", ctor, shortType(n.Type)),
			TextEdits: []analysis.TextEdit{{
				Pos: call.Lparen + 1, End: call.Lparen + 1,
				NewText: []byte(fmt.Sprintf("\n\t\t%s.Provide(%s),", qual, ctor)),
			}},
		}}
	}
	x.pass.Report(d)
}

// checkCycles finds hard dependency cycles among the assembly's
// constructors (di.Lazy edges break them — that IS the documented fix).
func (x *extractor) checkCycles(call *ast.CallExpr, total *regSummary) {
	nodes := make([]graphcheck.Node, len(total.Ctors))
	for i, c := range total.Ctors {
		nodes[i].Provides = c.Provides
		for _, n := range c.Needs {
			nodes[i].Needs = append(nodes[i].Needs,
				graphcheck.Edge{Type: n.Type, Soft: n.Lazy || n.Kind == needSoft})
		}
	}
	cycle := graphcheck.FindCycle(nodes)
	if cycle == nil {
		return
	}
	names := make([]string, 0, len(cycle)+1)
	for _, idx := range cycle {
		names = append(names, total.Ctors[idx].Name)
	}
	names = append(names, total.Ctors[cycle[0]].Name)
	x.pass.Reportf(call.Pos(),
		"error[DI0003]: dependency cycle: %s — break it by taking di.Lazy[T] at one edge",
		strings.Join(names, " → "))
}

// declByName finds a package-level function declaration by name.
func (x *extractor) declByName(name string) *ast.FuncDecl {
	for _, f := range x.pass.Files {
		for _, decl := range f.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok && fd.Recv == nil && fd.Name.Name == name {
				return fd
			}
		}
	}
	return nil
}

// findLocalConstructor looks for an unregistered function in this package
// returning the missing type, and the di import qualifier of the file
// containing the assembly.
func (x *extractor) findLocalConstructor(call *ast.CallExpr, typ string) (ctor, qualifier string) {
	var file *ast.File
	for _, f := range x.pass.Files {
		if f.Pos() <= call.Pos() && call.End() <= f.End() {
			file = f
			break
		}
	}
	if file == nil {
		return "", ""
	}
	qual := ""
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if path == diPath {
			qual = "di"
			if imp.Name != nil {
				qual = imp.Name.Name
			}
		}
	}
	if qual == "" {
		return "", "" // di not imported here: no safe textual fix
	}
	for _, f := range x.pass.Files {
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Recv != nil || fd.Type.Results == nil {
				continue
			}
			fn, _ := x.pass.TypesInfo.Defs[fd.Name].(*types.Func)
			if fn == nil {
				continue
			}
			sig := fn.Type().(*types.Signature)
			for i := 0; i < sig.Results().Len(); i++ {
				if typeString(sig.Results().At(i).Type()) == typ {
					return fd.Name.Name, qual
				}
			}
		}
	}
	return "", ""
}

// kernelGivens are types the runtime injects without registration.
var kernelGivens = []string{
	"*" + diPath + ".App",
	diPath + ".Runner",
	diPath + ".Key",
	"*log/slog.Logger", // provided by stack.Base at every stack/cli root
}

func merge(dst *regSummary, src *regSummary) {
	dst.Provides = append(dst.Provides, src.Provides...)
	dst.Ctors = append(dst.Ctors, src.Ctors...)
	dst.Scoped = append(dst.Scoped, src.Scoped...)
	dst.Member = append(dst.Member, src.Member...)
	dst.Private = append(dst.Private, src.Private...)
	dst.Opaque = dst.Opaque || src.Opaque
}

func shortType(full string) string {
	// "*github.com/x/y/pg.Config" → "*pg.Config"
	star := ""
	s := full
	if strings.HasPrefix(s, "*") {
		star, s = "*", s[1:]
	}
	if i := strings.LastIndex(s, "/"); i >= 0 {
		s = s[i+1:]
	}
	return star + s
}
