package main

// -fix: applying the analyzer's machine-safe edits to the files on disk.
//
// go/analysis ships a fix applier, but only behind the flat singlechecker
// driver — taking it would cost the rustc-style report that IS this binary.
// The edits it applies are small, local and non-overlapping by construction
// (a rename, an inserted registration line), so the applier is a short one:
// resolve each analysis.TextEdit to byte offsets, drop any fix that overlaps
// one already accepted, splice from the end of the file backwards so earlier
// offsets stay valid, gofmt, write.
//
// A fix is applied at most once per diagnostic, and a diagnostic is only
// reported as fixed after its file was successfully written — so the "fixed N"
// summary never overstates what happened on disk, and anything skipped still
// prints as an ordinary finding.

import (
	"fmt"
	"go/format"
	"os"
	"sort"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"

	"github.com/bronystylecrazy/ultrastack/analyzer/ultravet"
)

// fileEdit is one analysis.TextEdit resolved to byte offsets in a named file.
type fileEdit struct {
	start, end int
	text       string
	key        string // the diagnostic this edit serves
}

// pendingFix is one diagnostic's chosen fix, grouped by file.
type pendingFix struct {
	key   string
	edits map[string][]fileEdit
}

// fixReport is what -fix did: which findings are now repaired on disk, and
// how many files changed.
type fixReport struct {
	fixed map[string]bool // diagnostic key → written to disk
	files int
	errs  []error
}

// applyFixes rewrites files with the first suggested fix of every diagnostic
// that carries one. It is the caller's job to render diagnostics BEFORE
// calling this: the renderer quotes source lines from disk, which this moves.
func applyFixes(graph *checker.Graph, key func(*checker.Action, analysis.Diagnostic) string) fixReport {
	rep := fixReport{fixed: map[string]bool{}}

	// 1. Collect one fix per distinct diagnostic. The same file is analyzed
	//    twice for a package with tests ("p" and "p [p.test]"), so identical
	//    findings arrive twice; the key dedupes them before their edits can
	//    collide with each other.
	var pending []pendingFix
	seen := map[string]bool{}
	for _, act := range graph.Roots {
		fset := act.Package.Fset
		for _, d := range act.Diagnostics {
			if !ultravet.Fixable(d) {
				continue
			}
			k := key(act, d)
			if seen[k] {
				continue
			}
			seen[k] = true
			p := pendingFix{key: k, edits: map[string][]fileEdit{}}
			ok := true
			for _, e := range d.SuggestedFixes[0].TextEdits {
				f := fset.File(e.Pos)
				if f == nil {
					ok = false
					break
				}
				end := e.End
				if !end.IsValid() {
					end = e.Pos
				}
				p.edits[f.Name()] = append(p.edits[f.Name()], fileEdit{
					start: f.Offset(e.Pos), end: f.Offset(end),
					text: string(e.NewText), key: k,
				})
			}
			if ok && len(p.edits) > 0 {
				pending = append(pending, p)
			}
		}
	}
	if len(pending) == 0 {
		return rep
	}

	// 2. Accept fixes that do not overlap an already-accepted one. A fix is
	//    all-or-nothing: half an edit set is worse than none.
	accepted := map[string][]fileEdit{}
	for _, p := range pending {
		if fixConflicts(accepted, p) {
			continue // stays a plain finding; re-running -fix picks it up
		}
		for name, es := range p.edits {
			accepted[name] = append(accepted[name], es...)
		}
	}

	// 3. Splice and write, in a deterministic file order.
	names := make([]string, 0, len(accepted))
	for name := range accepted {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		keys, err := rewrite(name, accepted[name])
		if err != nil {
			rep.errs = append(rep.errs, err)
			continue
		}
		rep.files++
		for _, k := range keys {
			rep.fixed[k] = true
		}
	}
	return rep
}

// fixConflicts reports whether any edit of p overlaps an accepted edit.
// Two insertions at the same offset count as a conflict: their order would
// decide the result, and no order is more correct than the other.
func fixConflicts(accepted map[string][]fileEdit, p pendingFix) bool {
	for name, es := range p.edits {
		for _, e := range es {
			for _, a := range accepted[name] {
				if e.start < a.end && a.start < e.end {
					return true
				}
				if e.start == e.end && a.start == a.end && e.start == a.start {
					return true
				}
			}
		}
	}
	return false
}

// rewrite applies edits to one file and writes it back, returning the
// diagnostic keys the write repaired. Editing from the end backwards keeps
// every remaining offset valid without recomputing anything.
func rewrite(name string, edits []fileEdit) ([]string, error) {
	src, err := os.ReadFile(name)
	if err != nil {
		return nil, err
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(name); err == nil {
		mode = fi.Mode().Perm()
	}
	sort.SliceStable(edits, func(i, j int) bool { return edits[i].start > edits[j].start })

	out := src
	var keys []string
	for _, e := range edits {
		if e.start < 0 || e.end > len(out) || e.start > e.end {
			return nil, fmt.Errorf("%s: fix edit out of range [%d,%d)", name, e.start, e.end)
		}
		next := make([]byte, 0, len(out)+len(e.text))
		next = append(next, out[:e.start]...)
		next = append(next, e.text...)
		next = append(next, out[e.end:]...)
		out = next
		keys = append(keys, e.key)
	}
	if formatted, err := format.Source(out); err == nil {
		out = formatted
	}
	if err := os.WriteFile(name, out, mode); err != nil {
		return nil, err
	}
	return keys, nil
}
