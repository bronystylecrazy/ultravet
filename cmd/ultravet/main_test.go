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

// fixtureVet builds ultravet once and returns a runner over the checked-in
// testdata GOPATH — the analyzer's own broken fixtures, no network.
func fixtureVet(t *testing.T) (run func(env []string, args ...string) (stdout, stderr string, code int), gopath string) {
	t.Helper()
	if testing.Short() {
		t.Skip("builds and runs the analyzer binary")
	}
	gopath, err := filepath.Abs(filepath.Join("..", "..", "testdata"))
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "ultravet")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build ultravet: %v\n%s", err, out)
	}
	return func(env []string, args ...string) (string, string, int) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		// GITHUB_ACTIONS is cleared so the human format is the default even
		// when these tests run in Actions; a test that wants it passes it.
		cmd.Env = append(append(os.Environ(), "GOPATH="+gopath, "GO111MODULE=off", "GITHUB_ACTIONS="), env...)
		var out, errW bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errW
		code := 0
		if err := cmd.Run(); err != nil {
			exit, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("run ultravet %v: %v\nstderr: %s", args, err, errW.String())
			}
			code = exit.ExitCode()
		}
		return out.String(), errW.String(), code
	}, gopath
}

// jsonFinding mirrors the documented `ultra vet --json` schema. Decoding into
// a SEPARATE struct (not ultravet.Finding) is the point: it is the wire
// contract that must not drift, so the test spells every field out.
type jsonFinding struct {
	Code      string `json:"code"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	File      string `json:"file"`
	Line      int    `json:"line"`
	Col       int    `json:"col"`
	EndLine   int    `json:"endLine"`
	EndCol    int    `json:"endCol"`
	Fixable   bool   `json:"fixable"`
	Secondary []struct {
		File    string `json:"file"`
		Line    int    `json:"line"`
		Col     int    `json:"col"`
		EndLine int    `json:"endLine"`
		EndCol  int    `json:"endCol"`
		Message string `json:"message"`
	} `json:"secondary"`
	Fix *struct {
		Title string `json:"title"`
		Edits []struct {
			File        string `json:"file"`
			Line        int    `json:"line"`
			Col         int    `json:"col"`
			EndLine     int    `json:"endLine"`
			EndCol      int    `json:"endCol"`
			StartOffset int    `json:"startOffset"`
			EndOffset   int    `json:"endOffset"`
			NewText     string `json:"newText"`
		} `json:"edits"`
	} `json:"fix"`
}

// TestFormatJSON runs the structured emitter over the `fix` fixture (two
// missing providers, both machine-fixable) and asserts the whole contract:
// codes, severity, real column spans, the secondary "declared here" location,
// the fixable flag, and fix edits that match what -fix would write.
func TestFormatJSON(t *testing.T) {
	run, gopath := fixtureVet(t)
	stdout, stderr, code := run(nil, "-format", "json", "fix")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (findings present)\nstderr: %s", code, stderr)
	}
	var findings []jsonFinding
	if err := json.Unmarshal([]byte(stdout), &findings); err != nil {
		t.Fatalf("not a valid finding array: %v\n%s\nstderr: %s", err, stdout, stderr)
	}
	if len(findings) != 2 {
		t.Fatalf("want the fixture's 2 findings, got %d:\n%s", len(findings), stdout)
	}
	// Sorted by position: the block assembly (line 16) before the one-liner
	// (line 22).
	if findings[0].Line != 16 || findings[1].Line != 22 {
		t.Fatalf("findings are not position-sorted: %d then %d", findings[0].Line, findings[1].Line)
	}

	f := findings[0]
	src := filepath.Join(gopath, "src", "fix", "fix.go")
	if f.Code != "DI0001" || f.Severity != "error" {
		t.Errorf("code/severity = %q/%q, want DI0001/error", f.Code, f.Severity)
	}
	if !strings.Contains(f.Message, "no provider for *fix.Config") {
		t.Errorf("message = %q", f.Message)
	}
	if f.File != src {
		t.Errorf("file = %q, want the absolute %q", f.File, src)
	}
	// The span is the argument at fault — `NewDB` inside di.Provide(NewDB) —
	// with real columns, not the whole stack.Run( line.
	if f.Col != 14 || f.EndLine != 16 || f.EndCol != 19 {
		t.Errorf("span = %d:%d..%d:%d, want 16:14..16:19", f.Line, f.Col, f.EndLine, f.EndCol)
	}
	if len(f.Secondary) != 1 {
		t.Fatalf("want one secondary location, got %+v", f.Secondary)
	}
	if s := f.Secondary[0]; s.File != src || s.Line != 12 || s.Col != 12 || s.EndCol != 23 ||
		s.Message != "this parameter of NewDB created the need" {
		t.Errorf("secondary = %+v", s)
	}
	if !f.Fixable || f.Fix == nil {
		t.Fatalf("DI0001 with a unique local constructor must be fixable: %+v", f)
	}
	if f.Fix.Title != "Register NewConfig, which provides *fix.Config" {
		t.Errorf("fix title = %q", f.Fix.Title)
	}
	if len(f.Fix.Edits) != 1 {
		t.Fatalf("want one edit, got %+v", f.Fix.Edits)
	}
	e := f.Fix.Edits[0]
	if e.File != src || e.NewText != "\n\t\tdi.Provide(NewConfig)," {
		t.Errorf("edit = %+v", e)
	}
	if e.StartOffset != e.EndOffset || e.Line != e.EndLine || e.Col != e.EndCol {
		t.Errorf("an insertion must have start == end: %+v", e)
	}
	// The offsets are the contract an applier uses: splicing them into the
	// file must reproduce exactly what -fix writes.
	before, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if e.StartOffset < 0 || e.StartOffset > len(before) {
		t.Fatalf("edit offset %d out of range for a %d-byte file", e.StartOffset, len(before))
	}
	spliced := string(before[:e.StartOffset]) + e.NewText + string(before[e.StartOffset:])
	if !strings.Contains(spliced, "di.Provide(NewConfig),\n\t\tdi.Provide(NewDB),") {
		t.Errorf("splicing the edit did not register the provider:\n%s", spliced)
	}

	// The one-liner's fix is the inline form — the same [fixable] predicate,
	// a different edit.
	if g := findings[1]; !g.Fixable || g.Fix == nil || g.Fix.Edits[0].NewText != "di.Provide(NewConfig), " {
		t.Errorf("one-line assembly fix: %+v", g.Fix)
	}
}

// TestFormatJSONCleanIsAnEmptyArray: the document always parses as an array, so
// a caller can detect support without sniffing versions — and exits 0.
func TestFormatJSONCleanIsAnEmptyArray(t *testing.T) {
	run, _ := fixtureVet(t)
	stdout, stderr, code := run(nil, "--format=json", "opaque")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 for a clean package\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if strings.TrimSpace(stdout) != "[]" {
		t.Errorf("clean run printed %q, want \"[]\"", stdout)
	}
}

// TestFormatGitHub pins the workflow commands byte for byte: the levels, the
// repo-relative paths GitHub resolves, the real column span, the code as the
// annotation title — and no human report mixed in.
func TestFormatGitHub(t *testing.T) {
	run, gopath := fixtureVet(t)
	stdout, stderr, code := run([]string{"GITHUB_WORKSPACE=" + gopath}, "-format", "github", "fix", "filenames")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstderr: %s", code, stderr)
	}
	want := strings.Join([]string{
		"::warning file=src/filenames/handler_users.go,line=2,col=1,title=UV0003::warning[UV0003]: handler_users.go is a layer-prefixed file — split the domain into its own package, not the file into shards (the closed set: <pkg>.go, handler.go, types.go, deps.go, errors.go, plus plain-noun files)",
		"::error file=src/fix/fix.go,line=16,col=14,endColumn=19,title=DI0001::error[DI0001]: no provider for *fix.Config (needed by NewDB) — add a di.Provide/Supply for it, or take di.Optional[*fix.Config]",
		"::error file=src/fix/fix.go,line=22,col=23,endColumn=28,title=DI0001::error[DI0001]: no provider for *fix.Config (needed by NewDB) — add a di.Provide/Supply for it, or take di.Optional[*fix.Config]",
		"",
	}, "\n")
	if stdout != want {
		t.Errorf("annotations:\n--- got ---\n%s\n--- want ---\n%s", stdout, want)
	}
	if strings.Contains(stdout, "-->") || strings.Contains(stdout, "ultravet: ") {
		t.Errorf("the human report must be suppressed under a machine format:\n%s", stdout)
	}
}

// TestGitHubActionsAutoDetects is the zero-config promise: inside a workflow,
// a plain `ultravet ./...` annotates the pull request with no flag at all —
// and outside one, the same command still prints the rustc-style report.
func TestGitHubActionsAutoDetects(t *testing.T) {
	run, gopath := fixtureVet(t)

	auto, _, _ := run([]string{"GITHUB_ACTIONS=true", "GITHUB_WORKSPACE=" + gopath}, "filenames")
	if !strings.HasPrefix(auto, "::warning file=src/filenames/handler_users.go,line=2,col=1,title=UV0003::") {
		t.Errorf("GITHUB_ACTIONS=true must select the github format:\n%s", auto)
	}

	plain, _, _ := run(nil, "filenames")
	if strings.Contains(plain, "::warning") || !strings.Contains(plain, "warning[UV0003]:") {
		t.Errorf("outside Actions the report stays human:\n%s", plain)
	}

	// An explicit format always wins over the environment.
	explicit, _, _ := run([]string{"GITHUB_ACTIONS=true"}, "-format", "human", "filenames")
	if strings.Contains(explicit, "::warning") {
		t.Errorf("-format human must override GITHUB_ACTIONS:\n%s", explicit)
	}

	// -fix opts out: it is rewriting the checkout, and its summary is prose.
	fixed, _, _ := run([]string{"GITHUB_ACTIONS=true"}, "-fix", "filenames")
	if strings.Contains(fixed, "::warning") {
		t.Errorf("-fix must not be auto-switched into annotations:\n%s", fixed)
	}

	// Asking for both explicitly is a usage error, not a silent preference.
	_, stderr, code := run(nil, "-format", "github", "-fix", "filenames")
	if code != 2 || !strings.Contains(stderr, "cannot also emit -format github") {
		t.Errorf("-fix with -format github: exit %d, stderr %q", code, stderr)
	}
}

// TestDiagJSONAliasStillWorks: -diagjson was the flag's first name and older
// callers (an installed `ultra` from before this landing) still send it.
func TestDiagJSONAliasStillWorks(t *testing.T) {
	run, _ := fixtureVet(t)
	alias, _, aliasCode := run(nil, "-diagjson", "fix")
	canonical, _, canonCode := run(nil, "-format=json", "fix")
	if alias != canonical || aliasCode != canonCode {
		t.Errorf("-diagjson and -format=json must produce identical output\n--- alias ---\n%s\n--- canonical ---\n%s", alias, canonical)
	}
}

// TestFixRewritesFiles is the end-to-end covenant of -fix: over a COPY of the
// testdata GOPATH it rewrites the fixable findings on disk, leaves the advisory
// ones alone, and reports honestly what it did.
func TestFixRewritesFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the analyzer binary")
	}
	src, err := filepath.Abs(filepath.Join("..", "..", "testdata"))
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
		cmd.Env = append(os.Environ(), "GOPATH="+gopath, "GO111MODULE=off", "GITHUB_ACTIONS=")
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
