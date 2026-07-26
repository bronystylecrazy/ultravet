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
	"go/token"
	"go/types"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis"

	"github.com/bronystylecrazy/ultrastack/di/graphcheck"
)

var Analyzer = &analysis.Analyzer{
	Name:      "ultravet",
	Doc:       "static wiring checks for ultrastack dependency graphs (DI0001 missing providers, DI0004 ambiguity, DI0003 cycles, DI0005 module privacy, DI0007 bad binds, DI0010 bad constructors, DI0101 captive scoped deps, DI0106 family members outside; UV0001 constructors that dial, UV0002 required permissions no configured role grants, UV0003 layer-prefixed file names, UV0004 feature importing feature, UV0005 infra importing app, UV0006 util importing internal — before boot)",
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
	// Defaults is the multiset of types provided from inside a di.Default
	// registration — a SUBSET of allProvides(), not extra providers. It is
	// what lets candidateCount model the kernel's back-off (see
	// di/resolve.go filterDefaults). Kept as counts, not a set, because two
	// defaults for one type stay ambiguous exactly as they do at runtime.
	Defaults []string
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
	// Pos/End bound the registration argument at fault (the constructor
	// expression inside di.Provide/Bind/...) — column-precise reporting.
	// Only meaningful within the pass that extracted them: facts exported
	// to other packages carry NoPos (a token.Pos never survives the trip).
	Pos token.Pos
	End token.Pos
	// DeclPos/DeclEnd bound the parameter of the constructor's declaration
	// that created this need (`cfg *Config` in NewDB's signature) — the
	// "declared here" secondary lands on the parameter itself. Same
	// pass-local rule as Pos/End; NoPos when the declaration is elsewhere.
	DeclPos token.Pos
	DeclEnd token.Pos
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
	x := &extractor{pass: pass, memo: map[types.Object]*regSummary{},
		varMemo: map[*types.Var]*regSummary{}, linted: map[*ast.FuncDecl]bool{}}

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
			// Positions are pass-local; strip them before the summary
			// crosses the package boundary (importers fall back to their
			// own assembly call site).
			fact.Funcs[fd.Name.Name] = stripPositions(*x.summarizeFunc(obj, fd))
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

	// 3. UV0002: required permissions vs. the product's configured roles.
	x.checkPermissions()

	// 4. UV0003: layer-prefixed file names — the closed-set doctrine.
	checkFileNames(pass)

	// 5. UV0004/UV0005/UV0006: the product tree's illegal import edges.
	checkDoctrine(pass)
	return nil, nil
}

// layerPrefixed matches the banned file shapes: handler_*.go / service_*.go
// are horizontal layering smuggled back through names. The doctrine's
// closed set is <pkg>.go, handler.go, types.go, deps.go, errors.go plus
// plain-noun files; a file that outgrows a page splits the PACKAGE.
var layerPrefixed = regexp.MustCompile(`^(handler|service)_.+\.go$`)

// checkFileNames emits UV0003 for layer-prefixed file names — only in
// packages on the platform (importing ultrastack), so foreign code in the
// same build is never policed.
func checkFileNames(pass *analysis.Pass) {
	onPlatform := false
	for _, imp := range pass.Pkg.Imports() {
		if strings.HasPrefix(imp.Path(), ultrastackPath) {
			onPlatform = true
			break
		}
	}
	if !onPlatform {
		return
	}
	for _, file := range pass.Files {
		pos := pass.Fset.Position(file.Pos())
		base := filepath.Base(pos.Filename)
		if !layerPrefixed.MatchString(base) {
			continue
		}
		pass.Reportf(file.Pos(),
			"warning[UV0003]: %s is a layer-prefixed file — split the domain into its own package, not the file into shards (the closed set: <pkg>.go, handler.go, types.go, deps.go, errors.go, plus plain-noun files)",
			base)
	}
}

// isAssemblyRoot recognizes di.New / di.Validate / stack.Run / stack.New /
// stack.Validate / cli.Run / cli.Main / cli.Test — the places a whole
// graph is declared.
func isAssemblyRoot(pass *analysis.Pass, call *ast.CallExpr) bool {
	fn := calleeFunc(pass, call)
	if fn == nil || fn.Pkg() == nil {
		return false
	}
	switch fn.Pkg().Path() {
	case diPath:
		return fn.Name() == "New" || fn.Name() == "Validate"
	case stackPath:
		return fn.Name() == "Run" || fn.Name() == "New" || fn.Name() == "Validate"
	case cliPath:
		return fn.Name() == "Run" || fn.Name() == "Main" || fn.Name() == "Test"
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
	defaults := map[string]int{}
	for _, p := range total.Defaults {
		defaults[p]++
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
		cands := graphcheck.Candidates{Singleton: candidateCount(provided, defaults, n.Type)}
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
			x.reportAt(call, n, fmt.Sprintf(
				"error[DI0004]: %d providers for %s consumed bare by %s — collect them with di.Many[%s], or remove the extras",
				cands.Singleton, shortType(n.Type), n.By, shortType(n.Type)))
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

// candidateCount is di.Default's back-off, statically: the number of
// providers of t that actually reach resolution.
//
// It mirrors the kernel's filterDefaults (di/resolve.go) exactly — wherever
// a type has BOTH default and non-default providers, the defaults drop out
// and only the real ones are candidates; a type provided only by defaults
// keeps every one of them, so two defaults for one type stay DI0004 just as
// they do at runtime ("Default never introduces last-wins semantics between
// real registrations" — di.Default's doc).
//
// Without this, a preset's di.Default(...) plus the product's own provider
// counted as two candidates and the analyzer reported an ambiguity the
// runtime does not have.
func candidateCount(provided, defaults map[string]int, t string) int {
	n, d := provided[t], defaults[t]
	if d > 0 && n > d {
		return n - d // real providers exist: the defaults back off
	}
	return n // only defaults (or none): they resolve normally
}

// needSpan picks the report range for a need: the registration argument at
// fault (column-precise, when the need was extracted from this package's
// own source) — or the whole assembly call as the fallback for needs that
// crossed a package boundary via a fact.
func needSpan(call *ast.CallExpr, n need) (pos, end token.Pos) {
	if n.Pos.IsValid() {
		return n.Pos, n.End
	}
	return call.Pos(), token.NoPos
}

// reportAt emits a graph error at the offending registration argument,
// with the consumer's declaration as a related span — the rustc-style
// "declared here" secondary.
func (x *extractor) reportAt(call *ast.CallExpr, n need, msg string) {
	pos, end := needSpan(call, n)
	d := analysis.Diagnostic{Pos: pos, End: end, Message: msg}
	d.Related = x.relatedDecl(n)
	x.pass.Report(d)
}

// relatedDecl builds the "declared here" secondary for a need: the exact
// parameter that created it when the declaration is in this package —
// falling back to the constructor's name when only that is known.
func (x *extractor) relatedDecl(n need) []analysis.RelatedInformation {
	if n.DeclPos.IsValid() {
		return []analysis.RelatedInformation{{
			Pos:     n.DeclPos,
			End:     n.DeclEnd,
			Message: fmt.Sprintf("this parameter of %s created the need", n.By),
		}}
	}
	if decl := x.declByName(n.By); decl != nil {
		return []analysis.RelatedInformation{{
			Pos:     decl.Name.Pos(),
			End:     decl.Name.End(),
			Message: fmt.Sprintf("needed by %s, declared here", n.By),
		}}
	}
	return nil
}

// reportMissing emits DI0001 — with a one-click fix when exactly one
// unregistered constructor for the missing type exists in this package.
func (x *extractor) reportMissing(call *ast.CallExpr, n need) {
	pos, end := needSpan(call, n)
	d := analysis.Diagnostic{
		Pos: pos,
		End: end,
		Message: fmt.Sprintf(
			"error[DI0001]: no provider for %s (needed by %s) — add a di.Provide/Supply for it, or take di.Optional[%s]",
			shortType(n.Type), n.By, shortType(n.Type)),
	}
	// Point at the consumer too — the parameter that created the need.
	d.Related = x.relatedDecl(n)
	if ctor, qual := x.findLocalConstructor(call, n.Type); ctor != "" {
		lead, tail := x.argIndent(call)
		d.SuggestedFixes = []analysis.SuggestedFix{{
			Message: fmt.Sprintf("Register %s, which provides %s", ctor, shortType(n.Type)),
			TextEdits: []analysis.TextEdit{{
				Pos: call.Lparen + 1, End: call.Lparen + 1,
				NewText: []byte(fmt.Sprintf("%s%s.Provide(%s),%s", lead, qual, ctor, tail)),
			}},
		}}
	}
	x.pass.Report(d)
}

// argIndent picks the whitespace that makes an inserted registration line up
// with the assembly's existing arguments: a new line at the first argument's
// own indentation when the list is spread over lines, a single space when the
// whole call is on one line. gofmt would repair either, but -fix writes files
// people read in a diff — the edit should land already formatted.
func (x *extractor) argIndent(call *ast.CallExpr) (lead, tail string) {
	if len(call.Args) == 0 {
		return "\n\t", ""
	}
	lp := x.pass.Fset.Position(call.Lparen)
	first := x.pass.Fset.Position(call.Args[0].Pos())
	if lp.Filename != first.Filename || first.Line == lp.Line {
		return "", " " // one-liner: di.New(di.Provide(NewConfig), di.Provide(NewDB))
	}
	indent := "\t\t"
	if x.pass.ReadFile != nil {
		if src, err := x.pass.ReadFile(first.Filename); err == nil {
			if line, ok := nthLine(src, first.Line); ok {
				indent = line[:len(line)-len(strings.TrimLeft(line, " \t"))]
			}
		}
	}
	return "\n" + indent, ""
}

// nthLine returns the 1-based nth line of src, without its terminator.
func nthLine(src []byte, n int) (string, bool) {
	lines := strings.Split(string(src), "\n")
	if n < 1 || n > len(lines) {
		return "", false
	}
	return lines[n-1], true
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
//
// It answers only when the candidate is UNIQUE. Two functions returning the
// missing type is a choice — registering the wrong one compiles and boots the
// wrong graph — so the diagnostic stays advisory rather than let -fix guess.
// Generic constructors are skipped for the same reason: they need type
// arguments no edit can invent.
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
	found := ""
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
			if sig.TypeParams().Len() > 0 {
				continue // di.Provide(Generic) does not compile without type args
			}
			for i := 0; i < sig.Results().Len(); i++ {
				if typeString(sig.Results().At(i).Type()) != typ {
					continue
				}
				if found != "" && found != fd.Name.Name {
					return "", "" // ambiguous: no machine-safe answer
				}
				found = fd.Name.Name
			}
		}
	}
	if found == "" {
		return "", ""
	}
	return found, qual
}

// kernelGivens are types the runtime injects without registration. They
// count as ordinary (non-default) providers — NOT as di.Default
// registrations that back off — because that is what the kernel does:
//
//   - *di.App, di.Runner and di.Key are injected by the resolver itself and
//     have no registration to override.
//   - *slog.Logger is stack.Base's `di.Provide(newLogger)` (stack/stack.go),
//     a plain provider, so a product that also supplies a *slog.Logger has
//     two providers and stack.Validate reports DI0004. The swappable seam is
//     one level down — Base registers the slog.Handler as di.Default, and
//     THAT is what a product or preset (contrib/zlog) overrides. Modeling
//     the logger as a default here would silence a real boot failure, so it
//     deliberately stays a plain given; see testdata/src/defaults.
var kernelGivens = []string{
	"*" + diPath + ".App",
	diPath + ".Runner",
	diPath + ".Key",
	"*log/slog.Logger", // provided by stack.Base at every stack/cli root
}

// stripPositions deep-copies a summary with every need's Pos/End cleared —
// facts are (de)serialized across packages, where a token.Pos from this
// pass's FileSet would point at arbitrary code.
func stripPositions(s regSummary) regSummary {
	ctors := make([]ctorInfo, len(s.Ctors))
	copy(ctors, s.Ctors)
	for i := range ctors {
		needs := make([]need, len(ctors[i].Needs))
		copy(needs, ctors[i].Needs)
		for j := range needs {
			needs[j].Pos, needs[j].End = token.NoPos, token.NoPos
			needs[j].DeclPos, needs[j].DeclEnd = token.NoPos, token.NoPos
		}
		ctors[i].Needs = needs
	}
	s.Ctors = ctors
	return s
}

func merge(dst *regSummary, src *regSummary) {
	dst.Provides = append(dst.Provides, src.Provides...)
	dst.Ctors = append(dst.Ctors, src.Ctors...)
	dst.Scoped = append(dst.Scoped, src.Scoped...)
	dst.Member = append(dst.Member, src.Member...)
	dst.Private = append(dst.Private, src.Private...)
	dst.Defaults = append(dst.Defaults, src.Defaults...)
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
