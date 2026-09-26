// ultravet is the ultrastack static analyzer.
//
//	ultravet ./...                    rustc-style report (the default)
//	ultravet -fix ./...               apply the edits marked [fixable]
//	ultravet -format json ./...       the structured finding document
//	ultravet -format github ./...     GitHub Actions annotations
//
// -format github is selected AUTOMATICALLY when GITHUB_ACTIONS=true and no
// -format was given, so a workflow that runs the analyzer annotates the pull
// request with zero configuration. -fix suppresses the auto-selection (its
// output is a rewrite log, not an annotation stream) and is an error when a
// machine format is asked for explicitly.
//
// -json, -flags, -V and a unit .cfg belong to the `go vet -vettool` protocol
// and dispatch to the standard flat driver — gopls speaks it, so the name is
// not ours to reuse; the structured document rides -format json instead.
// -diagjson is the retired spelling of -format json, kept working.
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

	"github.com/bronystylecrazy/ultravet"
)

// output formats. human is the rustc-style report; json and github are the
// machine serializations, both marshaled by this module's one Finding
// mapping so they cannot drift from each other or from the mcp tool.
const (
	formatHuman  = "human"
	formatJSON   = "json"
	formatGitHub = "github"
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
	}

	format, fix, patterns, err := parseArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, "ultravet:", err)
		os.Exit(2)
	}
	if len(patterns) == 0 {
		patterns = []string{"./..."}
	}
	switch format {
	case formatJSON:
		os.Exit(reportJSON(patterns))
	case formatGitHub:
		os.Exit(reportGitHub(patterns))
	}
	// -fix stays on THIS driver rather than singlechecker's: handing it over
	// would trade the rustc-style report for the flat one, and the report is
	// what tells you which findings -fix could touch.
	os.Exit(pretty(patterns, fix))
}

// parseArgs splits the command line into the output format, the -fix flag and
// the load patterns. -format accepts both spellings (-format=json and -format
// json) and both dash counts; an unrecognized flag is left for the pattern
// list, where packages.Load reports it far better than we could.
func parseArgs(args []string) (format string, fix bool, patterns []string, err error) {
	explicit := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-fix" || a == "--fix":
			fix = true
		case a == "-diagjson" || a == "--diagjson": // the retired spelling
			format, explicit = formatJSON, true
		case a == "-format" || a == "--format":
			if i+1 >= len(args) {
				return "", false, nil, fmt.Errorf("-format needs a value (%s|%s|%s)", formatHuman, formatJSON, formatGitHub)
			}
			i++
			format, explicit = args[i], true
		case strings.HasPrefix(a, "-format=") || strings.HasPrefix(a, "--format="):
			_, v, _ := strings.Cut(a, "=")
			format, explicit = v, true
		default:
			patterns = append(patterns, a)
		}
	}
	switch format {
	case "", formatHuman, formatJSON, formatGitHub:
	default:
		return "", false, nil, fmt.Errorf("unknown -format %q (want %s, %s or %s)", format, formatHuman, formatJSON, formatGitHub)
	}
	if explicit && fix && format != formatHuman {
		return "", false, nil, fmt.Errorf("-fix rewrites files and reports what changed; it cannot also emit -format %s", format)
	}
	if !explicit {
		format = autoFormat(fix)
	}
	return format, fix, patterns, nil
}

// autoFormat is the zero-config CI behaviour: inside GitHub Actions, findings
// become annotations on the pull request without anyone passing a flag. A -fix
// run opts out — it is rewriting the checkout, and its summary is prose.
func autoFormat(fix bool) string {
	if !fix && os.Getenv("GITHUB_ACTIONS") == "true" {
		return formatGitHub
	}
	return formatHuman
}

// load runs the analyzer over patterns, returning the result graph or a
// non-zero exit code (2) after reporting why it could not.
func load(patterns []string) (*checker.Graph, int) {
	cfg := &packages.Config{Mode: packages.LoadAllSyntax}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, 2
	}
	if packages.PrintErrors(pkgs) > 0 {
		return nil, 2
	}
	graph, err := checker.Analyze([]*analysis.Analyzer{ultravet.Analyzer}, pkgs, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return nil, 2
	}
	return graph, 0
}

// diagKey identifies a diagnostic across the duplicate actions a package with
// tests produces — position plus message, the same identity -fix uses.
func diagKey(act *checker.Action, d analysis.Diagnostic) string {
	return act.Package.Fset.Position(d.Pos).String() + d.Message
}

// collect turns the run into the canonical, deduplicated, sorted finding list.
// Both machine formats start here — there is one traversal, one mapping, one
// order, so `--json` and `--format github` always describe the same findings.
func collect(graph *checker.Graph) []ultravet.Finding {
	out := []ultravet.Finding{}
	seen := map[string]bool{}
	for _, act := range graph.Roots {
		fset := act.Package.Fset
		for _, d := range act.Diagnostics {
			key := diagKey(act, d)
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, ultravet.NewFinding(fset, d))
		}
	}
	ultravet.SortFindings(out)
	return out
}

// reportJSON prints the structured finding document. Exit codes match every
// other format: 1 with gating findings, 0 clean, 2 on a load error.
func reportJSON(patterns []string) int {
	graph, code := load(patterns)
	if graph == nil {
		return code
	}
	findings := collect(graph)
	os.Stdout.Write(ultravet.MarshalFindings(findings))
	return exitFor(gating(findings))
}

// reportGitHub prints GitHub Actions workflow commands — one annotation per
// finding, landing inline on the pull request's diff.
func reportGitHub(patterns []string) int {
	graph, code := load(patterns)
	if graph == nil {
		return code
	}
	findings := collect(graph)
	ultravet.WriteGitHubAnnotations(os.Stdout, findings, workspaceRoot())
	return exitFor(gating(findings))
}

func exitFor(findings int) int {
	if findings > 0 {
		return 1
	}
	return 0
}

// gating counts the findings that fail the run. Notes (the informational
// tier — a feature's sibling-dependency listing) print but never gate.
func gating(findings []ultravet.Finding) int {
	n := 0
	for _, f := range findings {
		if f.Severity != "note" {
			n++
		}
	}
	return n
}

// workspaceRoot is what GitHub resolves annotation paths against: the checkout
// root the runner exports, or the working directory outside Actions.
func workspaceRoot() string {
	if w := os.Getenv("GITHUB_WORKSPACE"); w != "" {
		return w
	}
	wd, _ := os.Getwd()
	return wd
}

// pretty renders findings rustc-style. With fix set, the machine-safe ones are
// applied to the files first and then omitted from the report, which ends with
// what changed on disk.
//
// Exit codes are the same either way — 1 whenever the source had findings, 0
// when clean, 2 on a load error. -fix changes the files, not the verdict: the
// run that repaired them still reports that they needed repairing.
func pretty(patterns []string, fix bool) int {
	graph, code := load(patterns)
	if graph == nil {
		return code
	}

	type finding struct {
		pos     string
		key     string
		text    string
		fixable bool
		note    bool
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
			sev, _, _ := ultravet.SplitHead(d.Message)
			f := finding{
				pos:     act.Package.Fset.Position(d.Pos).String(),
				key:     key,
				text:    ultravet.RenderDiagnostic(act.Package.Fset, d, color),
				fixable: ultravet.Fixable(d),
				note:    sev == "note",
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
	printed, notes := 0, 0
	for _, f := range findings {
		if rep.fixed[f.key] {
			continue // repaired on disk; reporting it again would be noise
		}
		if printed > 0 {
			fmt.Println()
		}
		fmt.Print(f.text)
		printed++
		if f.note {
			notes++
		}
	}
	if printed > 0 {
		fmt.Println()
	}
	// Notes are informational — they print, they never gate the run.
	gates := printed - notes
	if fix {
		fmt.Printf("ultravet: fixed %s in %s; %s remaining\n",
			plural(len(rep.fixed), "issue"), plural(rep.files, "file"),
			plural(gates, "finding"))
		// -fix changes the files, not the verdict: a source that had gating
		// findings still exits 1 even when every one was repaired.
		total := 0
		for _, f := range findings {
			if !f.note {
				total++
			}
		}
		return exitFor(total)
	}
	if gates == 0 {
		fmt.Printf("ultravet: %s (informational — nothing gates)\n", plural(notes, "note"))
		return 0
	}
	fmt.Printf("ultravet: %s", plural(gates, "finding"))
	if notes > 0 {
		fmt.Printf(", %s", plural(notes, "note"))
	}
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
