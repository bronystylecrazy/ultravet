// ultravet is the ultrastack static analyzer. Run standalone for the
// rustc-style rendering; -fix/-json and `go vet -vettool` dispatch to the
// standard flat drivers; -diagjson emits ultrastack-structured findings.
package main

import (
	"encoding/json"
	"fmt"
	"go/token"
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
		// -diagjson is ultravet's own structured emitter (see diagJSON): the
		// go/analysis flat driver has no place for our codes, related spans,
		// and fixes, so this is a separate path from -json (kept for gopls).
		if a == "-diagjson" || a == "--diagjson" {
			os.Exit(diagJSON(withoutFlag(args, "-diagjson", "--diagjson")))
		}
	}
	if len(args) == 0 {
		args = []string{"./..."}
	}
	os.Exit(pretty(args))
}

// withoutFlag drops the named flags from args, leaving the load patterns.
func withoutFlag(args []string, flags ...string) []string {
	drop := map[string]bool{}
	for _, f := range flags {
		drop[f] = true
	}
	out := args[:0:0]
	for _, a := range args {
		if !drop[a] {
			out = append(out, a)
		}
	}
	return out
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

// ---- -diagjson: ultrastack-structured findings ----

// diagFinding is one wiring diagnostic in the structured shape agents and
// IDEs consume — the same data RenderDiagnostic draws, minus the drawing.
type diagFinding struct {
	Code         string            `json:"code"`    // "DI0001", "UV0002", "" if unparsable
	Message      string            `json:"message"` // full one-line message (code prefix included)
	File         string            `json:"file"`
	Line         int               `json:"line"`
	Col          int               `json:"col"`
	Related      []diagRelated     `json:"related,omitempty"`
	SuggestedFix *diagSuggestedFix `json:"suggestedFix,omitempty"`
}

type diagRelated struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	Message string `json:"message"`
}

type diagSuggestedFix struct {
	Message string         `json:"message"`
	Edits   []diagTextEdit `json:"edits,omitempty"`
}

type diagTextEdit struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	EndLine int    `json:"endLine"`
	EndCol  int    `json:"endCol"`
	NewText string `json:"newText"`
}

// diagJSON runs the analyzer and prints its findings as a JSON array on
// stdout — always an array (even empty), so callers can detect support by
// parsing the output. Exit 1 with findings, 0 clean, 2 on a load error.
func diagJSON(patterns []string) int {
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
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

	findings := []diagFinding{} // non-nil: marshals to [] when empty
	seen := map[string]bool{}
	for _, act := range graph.Roots {
		fset := act.Package.Fset
		for _, d := range act.Diagnostics {
			pos := fset.Position(d.Pos)
			key := pos.String() + d.Message
			if seen[key] {
				continue
			}
			seen[key] = true
			findings = append(findings, toDiagFinding(fset, d))
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Col < b.Col
	})

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(findings)
	if len(findings) > 0 {
		return 1
	}
	return 0
}

// toDiagFinding flattens a go/analysis diagnostic into the structured shape.
func toDiagFinding(fset *token.FileSet, d analysis.Diagnostic) diagFinding {
	pos := fset.Position(d.Pos)
	f := diagFinding{
		Code:    diagCode(d.Message),
		Message: d.Message,
		File:    pos.Filename,
		Line:    pos.Line,
		Col:     pos.Column,
	}
	for _, r := range d.Related {
		rp := fset.Position(r.Pos)
		f.Related = append(f.Related, diagRelated{
			File: rp.Filename, Line: rp.Line, Col: rp.Column, Message: r.Message,
		})
	}
	if len(d.SuggestedFixes) > 0 {
		sf := d.SuggestedFixes[0] // the primary fix; the message names the rest
		fix := &diagSuggestedFix{Message: sf.Message}
		for _, e := range sf.TextEdits {
			ep, eend := fset.Position(e.Pos), fset.Position(e.End)
			fix.Edits = append(fix.Edits, diagTextEdit{
				File: ep.Filename, Line: ep.Line, Col: ep.Column,
				EndLine: eend.Line, EndCol: eend.Column, NewText: string(e.NewText),
			})
		}
		f.SuggestedFix = fix
	}
	return f
}

// diagCode extracts the bracketed code from a message like
// "error[DI0001]: …" or "warning[UV0002]: …"; "" when there is none.
func diagCode(msg string) string {
	i := strings.IndexByte(msg, '[')
	if i < 0 {
		return ""
	}
	j := strings.IndexByte(msg[i:], ']')
	if j < 0 {
		return ""
	}
	return msg[i+1 : i+j]
}
