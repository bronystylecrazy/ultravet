package main

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

// TestFixRewritesFiles is the end-to-end covenant of -fix: over a COPY of the
// testdata GOPATH it rewrites the fixable findings on disk, leaves the advisory
// ones alone, and reports honestly what it did.
func TestFixRewritesFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the analyzer binary")
	}
	src, err := filepath.Abs(filepath.Join("..", "..", "ultravet", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	gopath := filepath.Join(t.TempDir(), "gopath")
	if err := copyTree(src, gopath); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "ultravet")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build ultravet: %v\n%s", err, out)
	}

	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Env = append(os.Environ(), "GOPATH="+gopath, "GO111MODULE=off")
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			if _, ok := err.(*exec.ExitError); !ok {
				t.Fatalf("run ultravet %v: %v\nstderr: %s", args, err, stderr.String())
			}
		}
		return stdout.String()
	}

	// A plain run names the fixable subset and touches nothing.
	fixFile := filepath.Join(gopath, "src", "fix", "fix.go")
	before, err := os.ReadFile(fixFile)
	if err != nil {
		t.Fatal(err)
	}
	report := run("fix")
	if !strings.Contains(report, "[fixable]") {
		t.Errorf("report must mark fixable findings:\n%s", report)
	}
	if !strings.Contains(report, "2 fixable — re-run with -fix to apply") {
		t.Errorf("report must count the fixable findings:\n%s", report)
	}
	if now, _ := os.ReadFile(fixFile); !bytes.Equal(now, before) {
		t.Fatal("a run without -fix must not write files")
	}

	// -fix repairs both DI0001s, to the byte the golden file expects.
	out := run("-fix", "fix")
	after, err := os.ReadFile(fixFile)
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile(filepath.Join(src, "src", "fix", "fix.go.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(golden) {
		t.Errorf("-fix did not reproduce the golden file:\n--- got ---\n%s\n--- want ---\n%s", after, golden)
	}
	if !strings.Contains(out, "fixed 2 issues in 1 file") {
		t.Errorf("summary must report what changed on disk:\n%s", out)
	}
	if !strings.Contains(out, "0 findings remaining") {
		t.Errorf("summary must report what is left:\n%s", out)
	}

	// The DI0010 rename lands too.
	supply := filepath.Join(gopath, "src", "supplyfix", "supplyfix.go")
	run("-fix", "supplyfix")
	if b, _ := os.ReadFile(supply); !strings.Contains(string(b), "di.Supply(&Config{") {
		t.Errorf("DI0010 fix did not rename Provide to Supply:\n%s", b)
	}

	// An advisory-only package (UV0001) is reported, never rewritten.
	dialFile := filepath.Join(gopath, "src", "dial", "dial.go")
	dialBefore, _ := os.ReadFile(dialFile)
	dialOut := run("-fix", "dial")
	if b, _ := os.ReadFile(dialFile); !bytes.Equal(b, dialBefore) {
		t.Error("UV0001 is advisory: -fix must not rewrite the file")
	}
	if !strings.Contains(dialOut, "fixed 0 issues in 0 files") {
		t.Errorf("an advisory-only run must say it fixed nothing:\n%s", dialOut)
	}
}

// copyTree copies a directory tree (files only) so a -fix run can rewrite it
// without disturbing the checked-in fixtures.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}
