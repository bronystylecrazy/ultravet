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
	"go/ast"
	"go/types"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis"
)

var Analyzer = &analysis.Analyzer{
	Name:      "ultravet",
	Doc:       "static wiring checks for ultrastack dependency graphs (DI0001 missing providers, DI0002 ambiguity, before boot)",
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
	Provides []string  // type strings provided
	Needs    []need    // constructor dependencies
	Opaque   bool      // something was not statically resolvable
}

type need struct {
	Type string
	By   string // constructor name, for the message
	Kind needKind
}

type needKind int

const (
	needHard needKind = iota // bare or Lazy: must exist
	needSoft                 // Optional / Many / variadic: absence is fine
)

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
	x := &extractor{pass: pass, memo: map[types.Object]*regSummary{}}

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
	for _, p := range total.Provides {
		provided[p]++
	}
	for _, k := range kernelGivens {
		provided[k]++
	}

	seen := map[string]bool{}
	for _, n := range total.Needs {
		count := provided[n.Type]
		switch {
		case count == 0 && n.Kind == needHard:
			key := "miss:" + n.Type + ":" + n.By
			if !seen[key] {
				seen[key] = true
				x.pass.Reportf(call.Pos(),
					"error[DI0001]: no provider for %s (needed by %s) — add a di.Provide/Supply for it, or take di.Optional[%s]",
					shortType(n.Type), n.By, shortType(n.Type))
			}
		case count > 1 && n.Kind == needHard:
			key := "amb:" + n.Type
			if !seen[key] {
				seen[key] = true
				x.pass.Reportf(call.Pos(),
					"error[DI0002]: %d providers for %s consumed bare by %s — collect them with di.Many[%s], or remove the extras",
					count, shortType(n.Type), n.By, shortType(n.Type))
			}
		}
	}
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
	dst.Needs = append(dst.Needs, src.Needs...)
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
