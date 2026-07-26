// ultravet is the ultrastack static analyzer. Run standalone for the
// rustc-style rendering, with -fix to apply the machine-safe edits the report
// marks [fixable]; -json and `go vet -vettool` dispatch to the standard flat
// driver; -diagjson emits ultrastack-structured findings.
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
		// The `go vet -vettool` protocol (-V=full, -flags, a unit .cfg) and
		// -json output are the standard flat driver's job.
		if strings.HasSuffix(a, ".cfg") || a == "-json" || a == "--json" ||
			a == "-flags" || a == "--flags" || strings.HasPrefix(a, "-V") {
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
	// -fix stays on THIS driver rather than singlechecker's: handing it over
	// would trade the rustc-style report for the flat one, and the report is
	// what tells you which findings -fix could touch.
	fix := false
	for _, a := range args {
		if a == "-fix" || a == "--fix" {
			fix = true
		}
	}
	patterns := withoutFlag(args, "-fix", "--fix")
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	os.Exit(pretty(patterns, fix))
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

// diagKey identifies a diagnostic across the duplicate actions a package with
// tests produces — position plus message, the same identity -fix uses.
func diagKey(act *checker.Action, d analysis.Diagnostic) string {
	return act.Package.Fset.Position(d.Pos).String() + d.Message
}

// pretty renders findings rustc-style. With fix set, the machine-safe ones are
// applied to the files first and then omitted from the report, which ends with
// what changed on disk.
//
// Exit codes are the same either way — 1 whenever the source had findings, 0
// when clean, 2 on a load error. -fix changes the files, not the verdict: the
// run that repaired them still reports that they needed repairing.
func pretty(patterns []string, fix bool) int {
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
		pos     string
		key     string
		text    string
		fixable bool
	}
	var findings []finding
	color := isTTY() && os.Getenv("NO_COLOR") == ""
	fixable := 0
	seen := map[string]bool{}
	// Render BEFORE any rewrite: the renderer quotes source lines from disk,
	// and applying a fix moves them.
	for _, act := range graph.Roots {
		for _, d := range act.Diagnostics {
			key := diagKey(act, d)
			if seen[key] {
				continue
			}
			seen[key] = true
			f := finding{
				pos:     act.Package.Fset.Position(d.Pos).String(),
				key:     key,
				text:    ultravet.RenderDiagnostic(act.Package.Fset, d, color),
				fixable: ultravet.Fixable(d),
			}
			if f.fixable {
				fixable++
			}
			findings = append(findings, f)
		}
	}
	if len(findings) == 0 {
		if fix {
			fmt.Println("ultravet: nothing to fix")
		}
		return 0
	}

	rep := fixReport{fixed: map[string]bool{}}
	if fix {
		rep = applyFixes(graph, diagKey)
		for _, err := range rep.errs {
			fmt.Fprintf(os.Stderr, "ultravet -fix: %v\n", err)
		}
	}

	sort.Slice(findings, func(i, j int) bool { return findings[i].pos < findings[j].pos })
	printed := 0
	for _, f := range findings {
		if rep.fixed[f.key] {
			continue // repaired on disk; reporting it again would be noise
		}
		if printed > 0 {
			fmt.Println()
		}
		fmt.Print(f.text)
		printed++
	}
	if printed > 0 {
		fmt.Println()
	}
	if fix {
		fmt.Printf("ultravet: fixed %s in %s; %s remaining\n",
			plural(len(rep.fixed), "issue"), plural(rep.files, "file"),
			plural(printed, "finding"))
		return 1
	}
	fmt.Printf("ultravet: %s", plural(printed, "finding"))
	if fixable > 0 {
		fmt.Printf(" (%d fixable — re-run with -fix to apply)", fixable)
	}
	fmt.Println()
	return 1
}

// plural renders "1 finding" / "3 findings".
func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
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
