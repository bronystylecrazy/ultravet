// ultravet is the ultrastack static analyzer. Run standalone for the
// rustc-style rendering; -fix/-json and `go vet -vettool` dispatch to the
// standard flat drivers.
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/analysis/singlechecker"
	"golang.org/x/tools/go/packages"

	"github.com/bronystylecrazy/ultrastack/analyzer/ultravet"
)

func main() {
	args := os.Args[1:]
	for _, a := range args {
		// go vet -vettool protocol, -fix application, or -json output:
		// the standard driver handles all three.
		if strings.HasSuffix(a, ".cfg") || a == "-fix" || a == "-json" || a == "--fix" || a == "--json" {
			singlechecker.Main(ultravet.Analyzer)
			return
		}
	}
	if len(args) == 0 {
		args = []string{"./..."}
	}
	os.Exit(pretty(args))
}

func pretty(patterns []string) int {
	cfg := &packages.Config{Mode: packages.LoadAllSyntax}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	if packages.PrintErrors(pkgs) > 0 {
		return 2
	}
	graph, err := checker.Analyze([]*analysis.Analyzer{ultravet.Analyzer}, pkgs, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	type finding struct {
		pos  string
		text string
	}
	var findings []finding
	color := isTTY() && os.Getenv("NO_COLOR") == ""
	seen := map[string]bool{}
	for _, act := range graph.Roots {
		for _, d := range act.Diagnostics {
			pos := act.Package.Fset.Position(d.Pos).String()
			key := pos + d.Message
			if seen[key] {
				continue
			}
			seen[key] = true
			findings = append(findings, finding{
				pos:  pos,
				text: ultravet.RenderDiagnostic(act.Package.Fset, d, color),
			})
		}
	}
	if len(findings) == 0 {
		return 0
	}
	sort.Slice(findings, func(i, j int) bool { return findings[i].pos < findings[j].pos })
	for i, f := range findings {
		if i > 0 {
			fmt.Println()
		}
		fmt.Print(f.text)
	}
	plural := ""
	if len(findings) > 1 {
		plural = "s"
	}
	fmt.Printf("\nultravet: %d finding%s\n", len(findings), plural)
	return 1
}

func isTTY() bool {
	fi, err := os.Stdout.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}
