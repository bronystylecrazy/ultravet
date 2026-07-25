package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// client is a minimal LSP client for the test conversation.
type client struct {
	t   *testing.T
	in  *bufio.Reader
	out io.Writer
	id  int
}

func (c *client) send(method string, params any, isRequest bool) *json.RawMessage {
	body := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	var id *json.RawMessage
	if isRequest {
		c.id++
		raw := json.RawMessage(fmt.Sprintf("%d", c.id))
		id = &raw
		body["id"] = c.id
	}
	b, _ := json.Marshal(body)
	fmt.Fprintf(c.out, "Content-Length: %d\r\n\r\n%s", len(b), b)
	return id
}

func (c *client) recv() rpcMessage {
	c.t.Helper()
	length := 0
	for {
		line, err := c.in.ReadString('\n')
		if err != nil {
			c.t.Fatalf("recv: %v", err)
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		fmt.Sscanf(line, "Content-Length: %d", &length)
	}
	buf := make([]byte, length)
	io.ReadFull(c.in, buf)
	var msg rpcMessage
	json.Unmarshal(buf, &msg)
	return msg
}

// brokenModule writes a product with a missing provider, replaced onto the
// local framework checkout.
func brokenModule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	root, _ := filepath.Abs("../..")
	os.WriteFile(filepath.Join(dir, "go.mod"), []byte(fmt.Sprintf(
		"module lspdemo\n\ngo 1.26\n\nrequire github.com/bronystylecrazy/ultrastack v0.0.0\n\nreplace github.com/bronystylecrazy/ultrastack => %s\n", root)), 0o644)
	writeMain(t, dir)
	tidy := exec.Command("go", "mod", "tidy")
	tidy.Dir = dir
	if out, err := tidy.CombinedOutput(); err != nil {
		t.Fatalf("tidy: %v\n%s", err, out)
	}
	return dir
}

func writeMain(t *testing.T, dir string) {
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(`package main

import (
	"github.com/bronystylecrazy/ultrastack/di"
	"github.com/bronystylecrazy/ultrastack/stack"
)

type Config struct{}
type Server struct{}

func NewConfig() *Config           { return &Config{} }
func NewServer(cfg *Config) *Server { return &Server{} }

func main() {
	stack.Run(
		di.Provide(NewServer),
	)
}
`), 0o644)
}

func TestLSPConversation(t *testing.T) {
	if testing.Short() {
		t.Skip("loads real packages")
	}
	dir := brokenModule(t)
	mainURI := "file://" + filepath.Join(dir, "main.go")

	clientIn, serverOut := io.Pipe()
	serverIn, clientOut := io.Pipe()
	srv := New(serverIn, serverOut)
	done := make(chan error, 1)
	go func() { done <- srv.Run() }()
	c := &client{t: t, in: bufio.NewReader(clientIn), out: clientOut}

	// initialize → capabilities advertise diagnostics + quickfix.
	c.send("initialize", map[string]any{"rootUri": "file://" + dir}, true)
	init := c.recv()
	raw, _ := json.Marshal(init.Result)
	if !strings.Contains(string(raw), "codeActionProvider") {
		t.Fatalf("capabilities: %s", raw)
	}
	c.send("initialized", map[string]any{}, false)

	// didOpen triggers analysis → publishDiagnostics with DI0001.
	c.send("textDocument/didOpen", map[string]any{
		"textDocument": map[string]any{"uri": mainURI},
	}, false)
	pub := c.recv()
	if pub.Method != "textDocument/publishDiagnostics" {
		t.Fatalf("expected publish, got %s", pub.Method)
	}
	var params struct {
		URI         string          `json:"uri"`
		Diagnostics []lspDiagnostic `json:"diagnostics"`
	}
	json.Unmarshal(pub.Params, &params)
	if params.URI != mainURI || len(params.Diagnostics) != 1 {
		t.Fatalf("diagnostics: %+v", params)
	}
	d := params.Diagnostics[0]
	if d.Code != "DI0001" || d.Source != "ultravet" || d.Severity != 1 ||
		!strings.Contains(d.Message, "no provider for *lspdemo.Config") {
		t.Fatalf("diagnostic: %+v", d)
	}
	// The related span is the parameter that created the need — the
	// `cfg *Config` in NewServer's signature on 0-based line 11, chars
	// 15..26 — not the function name.
	if len(d.Related) != 1 || !strings.Contains(d.Related[0].Message, "this parameter of NewServer") {
		t.Fatalf("related span missing: %+v", d.Related)
	}
	if r := d.Related[0].Location.Range; r.Start.Line != 11 || r.Start.Character != 15 ||
		r.End.Line != 11 || r.End.Character != 26 {
		t.Fatalf("related range: %+v", r)
	}
	// The range is the argument at fault — the NewServer inside
	// di.Provide(NewServer) on 0-based line 15, chars 13..22 — not the
	// whole stack.Run( line.
	if d.Range.Start.Line != 15 || d.Range.Start.Character != 13 ||
		d.Range.End.Line != 15 || d.Range.End.Character != 22 {
		t.Fatalf("range: %+v", d.Range)
	}

	// codeAction over the diagnostic returns the Register-NewConfig edit.
	c.send("textDocument/codeAction", map[string]any{
		"textDocument": map[string]any{"uri": mainURI},
		"range":        d.Range,
	}, true)
	act := c.recv()
	raw, _ = json.Marshal(act.Result)
	if !strings.Contains(string(raw), "Register NewConfig") ||
		!strings.Contains(string(raw), "di.Provide(NewConfig)") {
		t.Fatalf("code action: %s", raw)
	}

	// Fix the file on disk, save → diagnostics clear.
	src, _ := os.ReadFile(filepath.Join(dir, "main.go"))
	fixed := strings.Replace(string(src), "di.Provide(NewServer),", "di.Provide(NewConfig, NewServer),", 1)
	os.WriteFile(filepath.Join(dir, "main.go"), []byte(fixed), 0o644)
	c.send("textDocument/didSave", map[string]any{
		"textDocument": map[string]any{"uri": mainURI},
	}, false)
	clear := c.recv()
	json.Unmarshal(clear.Params, &params)
	if clear.Method != "textDocument/publishDiagnostics" || len(params.Diagnostics) != 0 {
		t.Fatalf("expected cleared diagnostics: %s %+v", clear.Method, params)
	}

	// shutdown/exit ends the loop cleanly.
	c.send("shutdown", nil, true)
	c.recv()
	c.send("exit", nil, false)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not exit")
	}
}
