package ultravet

// The machine-readable half of the report. RenderDiagnostic (pretty.go) draws
// a finding for a human; this file turns the SAME diagnostic into structured
// data — once — for every consumer that is not a terminal:
//
//   - `ultra vet --json` / `ultravet -format json` — the JSON document below
//   - the mcp `vet` tool — byte-identical output, same code path
//   - `ultra vet --format github` — GitHub Actions workflow commands
//
// One Finding type, one NewFinding mapping, one Fixable predicate. A consumer
// that wants a different shape converts FROM Finding; nothing re-reads
// analysis.Diagnostic, so the CLI, the agent tool and CI cannot drift.

import (
	"encoding/json"
	"fmt"
	"go/token"
	"io"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// Finding is one ultravet diagnostic as structured data — the stable schema
// `ultra vet --json` emits and the mcp `vet` tool returns.
//
// The document is a top-level JSON ARRAY of these, always present: a clean run
// prints "[]", so a caller can detect support by parsing the output. Findings
// are sorted by file, line, column, message — two runs over the same source
// emit byte-identical JSON.
//
// Positions are 1-based line and column, byte columns, exactly as Go tooling
// reports them (LSP's 0-based offsets are the LSP layer's conversion, not
// this schema's). Paths are absolute — editors and agents resolve them without
// knowing the analyzer's working directory; only the GitHub annotation writer
// relativizes, because GitHub resolves file= against the checkout root.
//
// Schema stability: fields are only ever ADDED. `endLine`/`endCol` collapse to
// `line`/`col` when the analyzer knows no end. `fixable` is the single
// predicate — it is true exactly when `fix` is present, and exactly when
// `ultravet -fix` would rewrite this finding.
type Finding struct {
	Code      string      `json:"code"`     // "DI0001", "UV0003"; "" when the message has no bracketed code
	Severity  string      `json:"severity"` // "error" or "warning"
	Message   string      `json:"message"`  // the full one-line message, code prefix included
	File      string      `json:"file"`     // absolute path
	Line      int         `json:"line"`     // 1-based
	Col       int         `json:"col"`      // 1-based, bytes
	EndLine   int         `json:"endLine"`  // == Line when the span is a point
	EndCol    int         `json:"endCol"`   // == Col when the span is a point
	Fixable   bool        `json:"fixable"`  // -fix rewrites exactly these
	Secondary []Secondary `json:"secondary,omitempty"`
	Fix       *Fix        `json:"fix,omitempty"`
}

// Secondary is a related location — the rustc-style "declared here" span, e.g.
// the constructor parameter that created a missing dependency.
type Secondary struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Col     int    `json:"col"`
	EndLine int    `json:"endLine"`
	EndCol  int    `json:"endCol"`
	Message string `json:"message"`
}

// Fix is the machine-applicable edit set behind a fixable finding — what
// `ultravet -fix` writes and what the LSP offers as a quick-fix.
type Fix struct {
	Title string `json:"title"` // human sentence, e.g. "Register NewConfig, which provides *app.Config"
	Edits []Edit `json:"edits"`
}

// Edit is one text replacement. It is given BOTH ways so an applier can pick:
// byte offsets into the file (what a rewriter wants) and a line/column span
// (what an editor wants). An insertion has start == end.
type Edit struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	Col         int    `json:"col"`
	EndLine     int    `json:"endLine"`
	EndCol      int    `json:"endCol"`
	StartOffset int    `json:"startOffset"` // 0-based byte offset
	EndOffset   int    `json:"endOffset"`
	NewText     string `json:"newText"`
}

// PrimaryFix returns the diagnostic's machine-applicable fix: the first
// SuggestedFix that actually carries text edits.
//
// This is the ONE place the framework decides what "fixable" means. The
// [fixable] tag in the report, `ultravet -fix`, the JSON `fix` object and the
// LSP quick-fix all resolve through here, so the report's coverage claim, the
// rewrite, the agent's view and the editor's lightbulb cannot disagree.
func PrimaryFix(d analysis.Diagnostic) (analysis.SuggestedFix, bool) {
	for _, f := range d.SuggestedFixes {
		if len(f.TextEdits) > 0 {
			return f, true
		}
	}
	return analysis.SuggestedFix{}, false
}

// Fixable reports whether a diagnostic carries a machine-applicable edit.
func Fixable(d analysis.Diagnostic) bool {
	_, ok := PrimaryFix(d)
	return ok
}

// SplitHead parses a message head — "error[DI0001]: no provider for …" — into
// its severity, code and remainder. A message without one is an error with no
// code and itself as the remainder. The renderer, the JSON emitter, the GitHub
// writer and the LSP all classify severity through this one function.
func SplitHead(msg string) (severity, code, rest string) {
	if m := headRe.FindStringSubmatch(msg); m != nil {
		return m[1], m[2], m[3]
	}
	return "error", "", msg
}

// NewFinding maps one go/analysis diagnostic into the structured shape.
func NewFinding(fset *token.FileSet, d analysis.Diagnostic) Finding {
	pos := fset.Position(d.Pos)
	end := pos
	if d.End.IsValid() {
		end = fset.Position(d.End)
	}
	sev, code, _ := SplitHead(d.Message)
	f := Finding{
		Code:     code,
		Severity: sev,
		Message:  d.Message,
		File:     pos.Filename,
		Line:     pos.Line,
		Col:      pos.Column,
		EndLine:  end.Line,
		EndCol:   end.Column,
		Fixable:  Fixable(d),
	}
	for _, r := range d.Related {
		rp := fset.Position(r.Pos)
		re := rp
		if r.End.IsValid() {
			re = fset.Position(r.End)
		}
		f.Secondary = append(f.Secondary, Secondary{
			File: rp.Filename, Line: rp.Line, Col: rp.Column,
			EndLine: re.Line, EndCol: re.Column, Message: r.Message,
		})
	}
	if fix, ok := PrimaryFix(d); ok {
		out := &Fix{Title: fix.Message}
		for _, e := range fix.TextEdits {
			ep := fset.Position(e.Pos)
			eend := ep
			if e.End.IsValid() {
				eend = fset.Position(e.End)
			}
			out.Edits = append(out.Edits, Edit{
				File: ep.Filename, Line: ep.Line, Col: ep.Column,
				EndLine: eend.Line, EndCol: eend.Column,
				StartOffset: ep.Offset, EndOffset: eend.Offset,
				NewText: string(e.NewText),
			})
		}
		f.Fix = out
	}
	return f
}

// SortFindings puts findings in the document's canonical order: file, line,
// column, then message — a total order, so the JSON is reproducible.
func SortFindings(f []Finding) {
	sort.SliceStable(f, func(i, j int) bool {
		a, b := f[i], f[j]
		switch {
		case a.File != b.File:
			return a.File < b.File
		case a.Line != b.Line:
			return a.Line < b.Line
		case a.Col != b.Col:
			return a.Col < b.Col
		}
		return a.Message < b.Message
	})
}

// MarshalFindings renders the JSON document: a top-level array, two-space
// indent, one trailing newline. A nil or empty slice marshals to "[]" rather
// than "null" — the document always parses as an array.
func MarshalFindings(findings []Finding) []byte {
	if findings == nil {
		findings = []Finding{}
	}
	b, err := json.MarshalIndent(findings, "", "  ")
	if err != nil { // unreachable: every field is a plain scalar/slice
		return []byte("[]\n")
	}
	return append(b, '\n')
}

// WriteGitHubAnnotations writes one GitHub Actions workflow command per
// finding — the form that makes findings appear inline on a pull request:
//
//	::error file=internal/app/app.go,line=16,col=14,endColumn=19,title=DI0001::error[DI0001]: no provider …
//
// Warning-tier codes emit ::warning, so a UV0003 advisory annotates without
// failing the eye test on an error. root is the directory GitHub resolves
// file= against (GITHUB_WORKSPACE, or the working directory locally); paths
// under it are emitted relative, everything else stays absolute.
func WriteGitHubAnnotations(w io.Writer, findings []Finding, root string) {
	for _, f := range findings {
		level := "error"
		if f.Severity == "warning" {
			level = "warning"
		}
		props := []string{
			"file=" + ghProperty(relativeTo(root, f.File)),
			fmt.Sprintf("line=%d", f.Line),
			fmt.Sprintf("col=%d", f.Col),
		}
		// endColumn only means anything on a single-line span; a span that
		// crosses lines says so with endLine instead.
		switch {
		case f.EndLine > f.Line:
			props = append(props, fmt.Sprintf("endLine=%d", f.EndLine))
		case f.EndLine == f.Line && f.EndCol > f.Col:
			props = append(props, fmt.Sprintf("endColumn=%d", f.EndCol))
		}
		if f.Code != "" {
			props = append(props, "title="+ghProperty(f.Code))
		}
		fmt.Fprintf(w, "::%s %s::%s\n", level, strings.Join(props, ","), ghData(f.Message))
	}
}

// ghData escapes a workflow command's message. The runner parses the stream
// line by line, so a literal newline would truncate the annotation — the spec's
// answer is percent-encoding, and % itself must go first.
func ghData(s string) string {
	s = strings.ReplaceAll(s, "%", "%25")
	s = strings.ReplaceAll(s, "\r", "%0D")
	s = strings.ReplaceAll(s, "\n", "%0A")
	return s
}

// ghProperty escapes a property value: the data escapes plus the two
// characters that would otherwise end the value or the property list.
func ghProperty(s string) string {
	s = ghData(s)
	s = strings.ReplaceAll(s, ":", "%3A")
	s = strings.ReplaceAll(s, ",", "%2C")
	return s
}

// relativeTo makes an absolute analyzer path repo-relative for an annotation —
// GitHub resolves file= against the checkout root, so an absolute path
// annotates nothing. A path outside root is left alone rather than dressed up
// with ../, which GitHub would not resolve either.
func relativeTo(root, file string) string {
	if root == "" || !filepath.IsAbs(file) {
		return file
	}
	rel, err := filepath.Rel(root, file)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return file
	}
	return filepath.ToSlash(rel)
}
