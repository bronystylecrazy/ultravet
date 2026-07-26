// Package lsp is ultravet as a language server: wiring diagnostics in the
// editor, beside gopls, with zero upstream involvement — LSP is an open
// protocol and editors merge diagnostics from multiple servers.
//
// The server is deliberately tiny: it answers initialize, re-analyzes a
// file's package on open/save (when wiring actually changes), publishes
// diagnostics with related "declared here" spans, and serves the
// SuggestedFixes as code actions — the "Register NewConfig" lightbulb.
// Everything else (completion, nav, formatting) belongs to gopls.
//
//	// Neovim
//	vim.lsp.config('ultravet', { cmd = {'ultravet-lsp'}, filetypes = {'go'} })
//	vim.lsp.enable('ultravet')
//
//	// Helix (languages.toml)
//	[language-server.ultravet]
//	command = "ultravet-lsp"
package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"go/token"
	"io"
	"strings"
	"sync"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/checker"
	"golang.org/x/tools/go/packages"

	"github.com/bronystylecrazy/ultrastack/analyzer/ultravet"
)

// Server speaks LSP over one connection (stdio in production, pipes in
// tests).
type Server struct {
	in  *bufio.Reader
	out io.Writer

	mu        sync.Mutex // guards out and state
	rootDir   string
	published map[string]bool       // URIs with active diagnostics
	fixes     map[string][]fixEntry // URI → available quick-fixes
	analyzeMu sync.Mutex            // one analysis at a time
}

type fixEntry struct {
	title string
	diag  lspDiagnostic
	edit  workspaceEdit
}

func New(in io.Reader, out io.Writer) *Server {
	return &Server{
		in:        bufio.NewReader(in),
		out:       out,
		published: map[string]bool{},
		fixes:     map[string][]fixEntry{},
	}
}

// ---- wire types (the slice of LSP we speak) ----

type rpcMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  any              `json:"result,omitempty"`
	Error   *rpcError        `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type lspRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type location struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

type lspDiagnostic struct {
	Range    lspRange  `json:"range"`
	Severity int       `json:"severity"` // 1 error, 2 warning
	Code     string    `json:"code,omitempty"`
	Source   string    `json:"source"`
	Message  string    `json:"message"`
	Related  []related `json:"relatedInformation,omitempty"`
}

type related struct {
	Location location `json:"location"`
	Message  string   `json:"message"`
}

type textEdit struct {
	Range   lspRange `json:"range"`
	NewText string   `json:"newText"`
}

type workspaceEdit struct {
	Changes map[string][]textEdit `json:"changes"`
}

type codeAction struct {
	Title       string          `json:"title"`
	Kind        string          `json:"kind"`
	Diagnostics []lspDiagnostic `json:"diagnostics,omitempty"`
	Edit        workspaceEdit   `json:"edit"`
}

// ---- main loop ----

// Run serves until the connection closes or `exit` arrives.
func (s *Server) Run() error {
	for {
		msg, err := s.read()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		switch msg.Method {
		case "initialize":
			var p struct {
				RootURI string `json:"rootUri"`
			}
			json.Unmarshal(msg.Params, &p)
			s.rootDir = uriToPath(p.RootURI)
			s.reply(msg.ID, map[string]any{
				"capabilities": map[string]any{
					"textDocumentSync": map[string]any{"openClose": true, "save": true},
					"codeActionProvider": map[string]any{
						"codeActionKinds": []string{"quickfix"},
					},
				},
				"serverInfo": map[string]any{"name": "ultravet-lsp"},
			})
		case "initialized", "textDocument/didChange", "textDocument/didClose",
			"$/setTrace", "$/cancelRequest", "workspace/didChangeConfiguration":
			// Notifications we deliberately ignore: analysis runs on save.
		case "textDocument/didOpen", "textDocument/didSave":
			var p struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
			}
			json.Unmarshal(msg.Params, &p)
			s.analyze(uriToPath(p.TextDocument.URI))
		case "textDocument/codeAction":
			var p struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
				Range lspRange `json:"range"`
			}
			json.Unmarshal(msg.Params, &p)
			s.reply(msg.ID, s.actionsFor(p.TextDocument.URI, p.Range))
		case "shutdown":
			s.reply(msg.ID, nil)
		case "exit":
			return nil
		default:
			if msg.ID != nil { // unknown request: answer, don't wedge the client
				s.replyErr(msg.ID, -32601, "method not supported: "+msg.Method)
			}
		}
	}
}

// ---- analysis ----

// analyze reloads the file's package and republishes diagnostics for every
// file that had or has findings.
func (s *Server) analyze(path string) {
	s.analyzeMu.Lock()
	defer s.analyzeMu.Unlock()

	cfg := &packages.Config{Mode: packages.LoadAllSyntax, Dir: s.rootDir}
	pkgs, err := packages.Load(cfg, "file="+path)
	if err != nil || len(pkgs) == 0 {
		return // not a loadable Go file: stay silent, gopls owns syntax errors
	}
	graph, err := checker.Analyze([]*analysis.Analyzer{ultravet.Analyzer}, pkgs, nil)
	if err != nil {
		return
	}

	diags := map[string][]lspDiagnostic{} // URI → diagnostics
	fixes := map[string][]fixEntry{}
	for _, act := range graph.Roots {
		fset := act.Package.Fset
		for _, d := range act.Diagnostics {
			uri, ld := toLSP(fset, d)
			diags[uri] = append(diags[uri], ld)
			// One fixable predicate, one fix: ultravet.PrimaryFix is what
			// `ultravet -fix` applies and what the JSON report calls
			// `fix` — the lightbulb offers exactly that edit set, never a
			// second implementation of it.
			if fix, ok := ultravet.PrimaryFix(d); ok {
				fixes[uri] = append(fixes[uri], fixEntry{
					title: fix.Message,
					diag:  ld,
					edit:  toWorkspaceEdit(fset, fix),
				})
			}
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	// Clear files that were dirty last round and are clean now.
	for uri := range s.published {
		if _, still := diags[uri]; !still {
			s.notifyLocked("textDocument/publishDiagnostics", map[string]any{
				"uri": uri, "diagnostics": []lspDiagnostic{},
			})
			delete(s.published, uri)
		}
	}
	for uri, ds := range diags {
		s.notifyLocked("textDocument/publishDiagnostics", map[string]any{
			"uri": uri, "diagnostics": ds,
		})
		s.published[uri] = true
	}
	s.fixes = fixes
}

func (s *Server) actionsFor(uri string, r lspRange) []codeAction {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []codeAction
	for _, f := range s.fixes[uri] {
		if !overlaps(f.diag.Range, r) {
			continue
		}
		out = append(out, codeAction{
			Title:       "ultravet: " + f.title,
			Kind:        "quickfix",
			Diagnostics: []lspDiagnostic{f.diag},
			Edit:        f.edit,
		})
	}
	return out
}

// ---- mapping ----

func toLSP(fset *token.FileSet, d analysis.Diagnostic) (uri string, out lspDiagnostic) {
	pos := fset.Position(d.Pos)
	end := pos
	if d.End.IsValid() {
		end = fset.Position(d.End)
	}
	// One head parser for the whole framework (analyzer/ultravet): the
	// severity the editor paints is the severity the report and the CI
	// annotation use.
	severity, code, msg := ultravet.SplitHead(d.Message)
	sev := 1
	if severity == "warning" {
		sev = 2
	}
	out = lspDiagnostic{
		Range:    lspRange{Start: toPos(pos), End: toPos(end)},
		Severity: sev,
		Code:     code,
		Source:   "ultravet",
		Message:  msg,
	}
	for _, rel := range d.Related {
		rp := fset.Position(rel.Pos)
		re := rp
		if rel.End.IsValid() {
			re = fset.Position(rel.End)
		}
		out.Related = append(out.Related, related{
			Location: location{URI: pathToURI(rp.Filename),
				Range: lspRange{Start: toPos(rp), End: toPos(re)}},
			Message: rel.Message,
		})
	}
	return pathToURI(pos.Filename), out
}

// toWorkspaceEdit turns a SuggestedFix into the LSP edit. Each text edit is
// filed under ITS OWN document — a fix is free to touch a second file, and
// stapling every edit onto the diagnostic's URI would corrupt it.
func toWorkspaceEdit(fset *token.FileSet, fix analysis.SuggestedFix) workspaceEdit {
	changes := map[string][]textEdit{}
	for _, e := range fix.TextEdits {
		p := fset.Position(e.Pos)
		q := p
		if e.End.IsValid() && e.End != e.Pos {
			q = fset.Position(e.End)
		}
		uri := pathToURI(p.Filename)
		changes[uri] = append(changes[uri], textEdit{
			Range:   lspRange{Start: toPos(p), End: toPos(q)},
			NewText: string(e.NewText),
		})
	}
	return workspaceEdit{Changes: changes}
}

func toPos(p token.Position) position {
	return position{Line: p.Line - 1, Character: p.Column - 1} // LSP is 0-based
}

func overlaps(a, b lspRange) bool {
	if a.End.Line < b.Start.Line || b.End.Line < a.Start.Line {
		return false
	}
	return true // line overlap is enough for lightbulb placement
}

func uriToPath(uri string) string { return strings.TrimPrefix(uri, "file://") }
func pathToURI(path string) string {
	if strings.HasPrefix(path, "file://") {
		return path
	}
	return "file://" + path
}

// ---- transport ----

func (s *Server) read() (*rpcMessage, error) {
	length := 0
	for {
		line, err := s.in.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(line, "Content-Length: "); ok {
			fmt.Sscanf(v, "%d", &length)
		}
	}
	body := make([]byte, length)
	if _, err := io.ReadFull(s.in, body); err != nil {
		return nil, err
	}
	var msg rpcMessage
	if err := json.Unmarshal(body, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

func (s *Server) reply(id *json.RawMessage, result any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeLocked(rpcMessage{JSONRPC: "2.0", ID: id, Result: result})
}

func (s *Server) replyErr(id *json.RawMessage, code int, msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.writeLocked(rpcMessage{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}})
}

func (s *Server) notifyLocked(method string, params any) {
	raw, _ := json.Marshal(params)
	s.writeLocked(rpcMessage{JSONRPC: "2.0", Method: method, Params: raw})
}

func (s *Server) writeLocked(msg rpcMessage) {
	body, _ := json.Marshal(msg)
	fmt.Fprintf(s.out, "Content-Length: %d\r\n\r\n%s", len(body), body)
}
