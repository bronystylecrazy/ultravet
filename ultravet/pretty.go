package ultravet

import (
	"fmt"
	"go/token"
	"os"
	"regexp"
	"strings"

	"golang.org/x/tools/go/analysis"
)

// The rustc-style renderer: what `ultravet` prints when it owns the
// terminal. Same structure as the kernel's runtime diagnostics —
//
//	error[DI0001]: no provider for *app.Config (needed by NewServer)
//	  --> main.go:14:2
//	   |
//	14 |     stack.Run(
//	   |     ^^^^^^^^^ in this assembly
//	   |
//	12 | func NewServer(cfg *Config) *Server { return &Server{} }
//	   | ------------- needed by NewServer, declared here
//	   |
//	  help: add a di.Provide/Supply for it, or take di.Optional[*app.Config]
//	  more: ultra explain DI0001
//
// go-vet/gopls callers still get the flat single-line form; this renderer
// is the standalone binary's TTY output.

const (
	cReset  = "\x1b[0m"
	cBold   = "\x1b[1m"
	cRed    = "\x1b[31;1m"
	cYellow = "\x1b[33;1m"
	cBlue   = "\x1b[34;1m"
	cDim    = "\x1b[2m"
)

var headRe = regexp.MustCompile(`^(error|warning)\[([A-Z]+[0-9]+)\]: (.*)$`)

// RenderDiagnostic renders one diagnostic rustc-style. color toggles ANSI.
func RenderDiagnostic(fset *token.FileSet, d analysis.Diagnostic, color bool) string {
	paint := func(c, s string) string {
		if !color {
			return s
		}
		return c + s + cReset
	}

	sev, code, msg, help := "error", "", d.Message, ""
	if m := headRe.FindStringSubmatch(d.Message); m != nil {
		sev, code, msg = m[1], m[2], m[3]
	}
	// The fix half of the message becomes its own help line.
	if i := strings.Index(msg, " — "); i >= 0 {
		msg, help = msg[:i], msg[i+len(" — "):]
	}

	sevColor := cRed
	if sev == "warning" {
		sevColor = cYellow
	}

	var b strings.Builder
	head := sev
	if code != "" {
		head = fmt.Sprintf("%s[%s]", sev, code)
	}
	fmt.Fprintf(&b, "%s%s %s\n", paint(sevColor, head+":"), "", paint(cBold, msg))

	pos := fset.Position(d.Pos)
	fmt.Fprintf(&b, "  %s %s\n", paint(cBlue, "-->"), pos)

	gutter := len(fmt.Sprintf("%d", pos.Line))
	pad := strings.Repeat(" ", gutter)
	bar := paint(cBlue, pad+" |")

	writeSpan := func(p token.Position, underlineColor, label string) {
		line, ok := sourceLine(p.Filename, p.Line)
		if !ok {
			return
		}
		fmt.Fprintf(&b, "%s\n", bar)
		num := fmt.Sprintf("%*d", gutter, p.Line)
		fmt.Fprintf(&b, "%s %s\n", paint(cBlue, num+" |"), strings.ReplaceAll(line, "\t", "    "))
		col := visualCol(line, p.Column)
		width := spanWidth(line, p.Column)
		marker := strings.Repeat(" ", col-1) + strings.Repeat("^", width)
		if label != "" {
			marker += " " + label
		}
		fmt.Fprintf(&b, "%s %s\n", bar, paint(underlineColor, marker))
	}

	writeSpan(pos, sevColor, "")
	for _, rel := range d.Related {
		writeSpan(fset.Position(rel.Pos), cBlue, rel.Message)
	}

	if help != "" {
		fmt.Fprintf(&b, "%s\n  %s %s\n", bar, paint(cBold, "help:"), help)
	}
	for _, fix := range d.SuggestedFixes {
		fmt.Fprintf(&b, "  %s %s (ultravet -fix applies it)\n", paint(cBold, "fix:"), fix.Message)
	}
	if strings.HasPrefix(code, "DI") {
		fmt.Fprintf(&b, "  %s ultra explain %s\n", paint(cDim, "more:"), code)
	}
	return b.String()
}

// sourceLine reads one line of a file (1-based).
func sourceLine(path string, n int) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	lines := strings.Split(string(data), "\n")
	if n < 1 || n > len(lines) {
		return "", false
	}
	return lines[n-1], true
}

// visualCol maps a byte column to the rendered column after tab expansion.
func visualCol(line string, col int) int {
	if col < 1 {
		return 1
	}
	v := 0
	for i, r := range line {
		if i >= col-1 {
			break
		}
		if r == '\t' {
			v += 4
		} else {
			v++
		}
	}
	return v + 1
}

// spanWidth underlines the token starting at col: the identifier/selector
// run, minimum 1.
func spanWidth(line string, col int) int {
	if col < 1 || col > len(line) {
		return 1
	}
	w := 0
	for _, r := range line[col-1:] {
		if r == '(' || r == ' ' || r == '\t' || r == ',' || r == ')' {
			break
		}
		w++
	}
	if w == 0 {
		return 1
	}
	return w
}
