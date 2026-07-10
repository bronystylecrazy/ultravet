package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestDiagJSONBrokenFixture builds ultravet and runs -diagjson over the
// `basic` wiring fixture (which has a missing provider), asserting the output
// is a valid structured-finding array carrying the code, message, position,
// and related span — the shape agents and IDEs consume.
func TestDiagJSONBrokenFixture(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the analyzer binary")
	}
	testdata, err := filepath.Abs(filepath.Join("..", "..", "ultravet", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "ultravet")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build ultravet: %v\n%s", err, out)
	}

	cmd := exec.Command(bin, "-diagjson", "basic")
	cmd.Env = append(os.Environ(), "GOPATH="+testdata, "GO111MODULE=off")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	// Findings are present, so a non-zero exit (1) is expected — not an error.
	if err := cmd.Run(); err != nil {
		if _, ok := err.(*exec.ExitError); !ok {
			t.Fatalf("run ultravet: %v\nstderr: %s", err, stderr.String())
		}
	}

	type finding struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		File    string `json:"file"`
		Line    int    `json:"line"`
		Col     int    `json:"col"`
		Related []struct {
			Message string `json:"message"`
		} `json:"related"`
		SuggestedFix *struct {
			Message string `json:"message"`
		} `json:"suggestedFix"`
	}
	var findings []finding
	if err := json.Unmarshal(stdout.Bytes(), &findings); err != nil {
		t.Fatalf("diagjson not a valid array: %v\n%s\nstderr: %s", err, stdout.String(), stderr.String())
	}
	if len(findings) == 0 {
		t.Fatalf("expected findings for the broken fixture, got none\nstderr: %s", stderr.String())
	}
	var di0001 *finding
	for i := range findings {
		if findings[i].Code == "DI0001" {
			di0001 = &findings[i]
		}
	}
	if di0001 == nil {
		t.Fatalf("expected a DI0001 finding, got: %s", stdout.String())
	}
	if di0001.Message == "" || di0001.File == "" || di0001.Line == 0 {
		t.Fatalf("DI0001 finding missing message/file/line: %+v", *di0001)
	}
	if len(di0001.Related) == 0 {
		t.Fatalf("DI0001 finding missing related span: %+v", *di0001)
	}
}

// TestDiagCode covers the bracketed-code parser directly.
func TestDiagCode(t *testing.T) {
	cases := map[string]string{
		"error[DI0001]: no provider for *x.Config":      "DI0001",
		"warning[UV0002]: permission \"x\" is required": "UV0002",
		"error[DI0106]: NewX consumes family member Y":  "DI0106",
		"no code here":        "",
		"trailing [only open": "",
	}
	for msg, want := range cases {
		if got := diagCode(msg); got != want {
			t.Errorf("diagCode(%q) = %q, want %q", msg, got, want)
		}
	}
}
