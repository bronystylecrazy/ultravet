package ultravet

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"golang.org/x/tools/go/analysis"
)

// authPath is the contrib/auth import path — the home of Require/RequireAny.
const authPath = "github.com/bronystylecrazy/ultrastack/contrib/auth"

// checkPermissions is the UV0002 pass. It statically collects the permission
// strings a package requires — stack.Route.Require literals (composite-literal
// fields and `rt.Require = "..."` assignments) and auth.Require/RequireAny
// string-literal arguments — and verifies each is granted by some role in the
// product's config.toml [auth.roles] map.
//
// Honesty is the constraint, exactly like the wiring checks: the lint is
// SILENT unless a config.toml with an [auth.roles] section is discoverable
// from the package (walking up to the module root). No config, no [auth.roles]
// — no opinion, so a product that grants permissions some other way is never
// nagged. Where a config IS present, a required permission no role grants is a
// typo or a missing grant, and we say which with a did-you-mean over the
// granted set.
func (x *extractor) checkPermissions() {
	granted, active := x.loadGrantedPerms()
	if !active {
		return
	}
	for _, req := range x.collectRequired() {
		if req.perm == "" || strings.HasPrefix(req.perm, "policy:") {
			continue // empty = any authenticated; policy refs are the boot check's job
		}
		if permGranted(granted, req.perm) {
			continue
		}
		x.reportUnknownPerm(req, granted)
	}
	x.checkMQTTGrants(granted)
}

// mqttPubPrefix and mqttSubPrefix mark contrib/mqtt permission grants whose
// remainder is an MQTT topic filter (contrib/mqtt's ACL matches devices against
// these). Both directions of the milestone-48 lint live here.
const (
	mqttPubPrefix = "mqtt.pub:"
	mqttSubPrefix = "mqtt.sub:"
)

// checkMQTTGrants validates that every mqtt.pub:/mqtt.sub: permission GRANTED by
// a role in config.toml is a well-formed MQTT topic filter after the prefix. A
// malformed filter is dead grant — it can never match a real topic — so it is a
// typo caught here rather than a device that mysteriously cannot subscribe.
//
// The check is the reverse of the required-permission direction and the only
// mqtt.* thing statically checkable: mqtt.On/ToHub patterns are topic filters,
// not permissions, and topics are a runtime value the route side never spells
// out. Grants have no Go position, so the diagnostic anchors at the package
// clause and names config.toml + the offending grant in the message.
func (x *extractor) checkMQTTGrants(granted map[string]bool) {
	if len(x.pass.Files) == 0 {
		return
	}
	anchor := x.pass.Files[0].Name.Pos()
	grants := make([]string, 0, len(granted))
	for g := range granted {
		grants = append(grants, g)
	}
	sort.Strings(grants) // deterministic report order
	for _, g := range grants {
		var filter string
		switch {
		case strings.HasPrefix(g, mqttPubPrefix):
			filter = g[len(mqttPubPrefix):]
		case strings.HasPrefix(g, mqttSubPrefix):
			filter = g[len(mqttSubPrefix):]
		default:
			continue
		}
		reason, fix := validateMQTTFilter(filter)
		if reason == "" {
			continue
		}
		x.pass.Reportf(anchor,
			"warning[UV0002]: permission %q in config.toml [auth.roles] is not a well-formed MQTT topic filter: %s — %s",
			g, reason, fix)
	}
}

// validateMQTTFilter returns a reason and a paste-able fix when filter is not a
// valid MQTT topic filter, or ("", "") when it is well-formed. It mirrors
// contrib/mqtt.ValidateFilter (the analyzer cannot import contrib).
func validateMQTTFilter(filter string) (reason, fix string) {
	if filter == "" {
		return "the filter is empty", "give it at least one level, e.g. \"telemetry/#\""
	}
	levels := strings.Split(filter, "/")
	for i, lvl := range levels {
		switch {
		case lvl == "+" || lvl == "" || lvl == "#":
			if lvl == "#" && i != len(levels)-1 {
				return `"#" is not the last level`, `move "#" to the final level (it matches everything beneath), e.g. "telemetry/#"`
			}
		case strings.Contains(lvl, "#"):
			return `"#" shares a level with other characters (in "` + lvl + `")`,
				`give "#" its own level: use "…/#", not "…` + lvl + `"`
		case strings.Contains(lvl, "+"):
			return `"+" shares a level with other characters (in "` + lvl + `")`,
				`give "+" its own level: use "…/+/…", not "…` + lvl + `…"`
		}
	}
	return "", ""
}

// requiredPerm is one statically-known permission requirement and the literal
// node that produced it (for the position and the typo-fix).
type requiredPerm struct {
	perm string
	lit  *ast.BasicLit
}

// loadGrantedPerms finds config.toml and flattens [auth.roles] into the set of
// granted permission strings (wildcards included). active is false when there
// is no config or no [auth.roles] — the silence rule.
func (x *extractor) loadGrantedPerms() (granted map[string]bool, active bool) {
	dir := x.packageDir()
	if dir == "" {
		return nil, false
	}
	path, ok := findConfigToml(dir)
	if !ok {
		return nil, false
	}
	var cfg struct {
		Auth struct {
			Roles map[string][]string `toml:"roles"`
		} `toml:"auth"`
	}
	md, err := toml.DecodeFile(path, &cfg)
	if err != nil || !md.IsDefined("auth", "roles") {
		return nil, false // unreadable or no role→permission map: stay silent
	}
	granted = map[string]bool{}
	for _, perms := range cfg.Auth.Roles {
		for _, p := range perms {
			granted[p] = true
		}
	}
	return granted, true
}

// packageDir is the directory of the package's first source file.
func (x *extractor) packageDir() string {
	for _, f := range x.pass.Files {
		if p := x.pass.Fset.Position(f.Pos()).Filename; p != "" {
			return filepath.Dir(p)
		}
	}
	return ""
}

// findConfigToml walks up from startDir looking for config.toml, stopping at
// (and including) the module root (a directory with go.mod) or a GOPATH "src"
// boundary — so the search never escapes the project into unrelated configs.
func findConfigToml(startDir string) (string, bool) {
	dir := startDir
	for {
		if p := filepath.Join(dir, "config.toml"); statExists(p) {
			return p, true
		}
		if statExists(filepath.Join(dir, "go.mod")) {
			return "", false // module root reached, no config here or below
		}
		if filepath.Base(dir) == "src" {
			return "", false // GOPATH/testdata boundary
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func statExists(p string) bool { _, err := os.Stat(p); return err == nil }

// collectRequired scans the package AST for every statically-known permission
// requirement.
func (x *extractor) collectRequired() []requiredPerm {
	var out []requiredPerm
	for _, file := range x.pass.Files {
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CompositeLit:
				if x.isStackRoute(x.pass.TypesInfo.TypeOf(node)) {
					for _, elt := range node.Elts {
						kv, ok := elt.(*ast.KeyValueExpr)
						if !ok {
							continue
						}
						if key, ok := kv.Key.(*ast.Ident); ok && key.Name == "Require" {
							if lit, s, ok := stringLit(kv.Value); ok {
								out = append(out, requiredPerm{perm: s, lit: lit})
							}
						}
					}
				}
			case *ast.AssignStmt:
				for i, lhs := range node.Lhs {
					sel, ok := lhs.(*ast.SelectorExpr)
					if !ok || sel.Sel.Name != "Require" {
						continue
					}
					if !x.isStackRoute(deref(x.pass.TypesInfo.TypeOf(sel.X))) {
						continue
					}
					if i < len(node.Rhs) {
						if lit, s, ok := stringLit(node.Rhs[i]); ok {
							out = append(out, requiredPerm{perm: s, lit: lit})
						}
					}
				}
			case *ast.CallExpr:
				if fn := calleeFunc(x.pass, node); fn != nil && fn.Pkg() != nil &&
					fn.Pkg().Path() == authPath && (fn.Name() == "Require" || fn.Name() == "RequireAny") {
					for _, arg := range node.Args {
						if lit, s, ok := stringLit(arg); ok {
							out = append(out, requiredPerm{perm: s, lit: lit})
						}
					}
				}
			}
			return true
		})
	}
	return out
}

func (x *extractor) reportUnknownPerm(req requiredPerm, granted map[string]bool) {
	msg := fmt.Sprintf(
		"warning[UV0002]: permission %q is required but no role in [auth.roles] grants it", req.perm)
	if near := closestPerm(req.perm, granted); near != "" {
		msg += fmt.Sprintf(" — did you mean %q? Otherwise grant it to a role in config.toml", near)
		x.pass.Report(analysis.Diagnostic{
			Pos:     req.lit.Pos(),
			Message: msg,
			SuggestedFixes: []analysis.SuggestedFix{{
				Message: fmt.Sprintf("Replace with %q", near),
				TextEdits: []analysis.TextEdit{{
					Pos: req.lit.Pos(), End: req.lit.End(),
					NewText: []byte(strconv.Quote(near)),
				}},
			}},
		})
		return
	}
	msg += " — add it to a role's grants in config.toml [auth.roles], or fix the requirement"
	x.pass.Report(analysis.Diagnostic{Pos: req.lit.Pos(), Message: msg})
}

// isStackRoute reports whether t is stack.Route.
func (x *extractor) isStackRoute(t types.Type) bool {
	named, ok := t.(*types.Named)
	return ok && named.Obj().Pkg() != nil &&
		named.Obj().Pkg().Path() == stackPath && named.Obj().Name() == "Route"
}

// deref unwraps a pointer type once (so both stack.Route and *stack.Route match).
func deref(t types.Type) types.Type {
	if ptr, ok := t.(*types.Pointer); ok {
		return ptr.Elem()
	}
	return t
}

// ---- permission-set semantics (mirrors auth.PermSet.Has; the analyzer cannot
// import contrib) ----

// permGranted reports whether perm is explicitly named by a role — as itself
// or via a segment wildcard ("cameras.*" covers "cameras.write").
//
// The bare "*" catch-all is DELIBERATELY not treated as coverage: it grants
// everything at runtime (an admin can do anything), so honoring it here would
// mask every typo and make the lint useless for the many apps that give an
// admin role "*". A required permission still has to be spelled like one your
// roles actually name — a good hygiene nudge, not a false positive.
func permGranted(granted map[string]bool, perm string) bool {
	if granted[perm] {
		return true
	}
	for i := len(perm) - 1; i > 0; i-- {
		if perm[i] == '.' && granted[perm[:i]+".*"] {
			return true
		}
	}
	return false
}

// closestPerm returns the nearest non-wildcard granted permission within a
// small edit distance, or "" when nothing is close enough.
func closestPerm(perm string, granted map[string]bool) string {
	best, bestD := "", 1<<30
	cands := make([]string, 0, len(granted))
	for g := range granted {
		if strings.Contains(g, "*") {
			continue // a wildcard is never a "did you mean this literal"
		}
		cands = append(cands, g)
	}
	sort.Strings(cands) // deterministic tie-break
	for _, g := range cands {
		if d := editDistance(perm, g); d < bestD {
			best, bestD = g, d
		}
	}
	// Only suggest when genuinely close: within a third of the length, cap 3.
	limit := len(perm)/3 + 1
	if limit > 3 {
		limit = 3
	}
	if bestD <= limit {
		return best
	}
	return ""
}

func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min3(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}

func min3(a, b, c int) int {
	if b < a {
		a = b
	}
	if c < a {
		a = c
	}
	return a
}

// ---- small AST/type helpers ----

func stringLit(e ast.Expr) (*ast.BasicLit, string, bool) {
	lit, ok := ast.Unparen(e).(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return nil, "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return nil, "", false
	}
	return lit, s, true
}
