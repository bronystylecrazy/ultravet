package ultravet

// spanctx.go is UV0012: once a function opens a span, the span IS the
// context. A later use of the context it was opened from hands children the
// stale parent — their spans and logs land BESIDE the span instead of under
// it. It compiles fine, which is why it is a lint.
//
//	span, end := s.trace.Span(ctx, "order.id", id)
//	defer end(&err)
//	return s.store.MarkPaid(ctx, id)   // UV0012: pass span
//
// Positional, not flow-sensitive: a use after the Span call, inside the span
// variable's scope, before the first write back to the context variable. A
// `_` span was discarded on purpose and is never checked, and neither is a
// parent stack.Span a nested span was opened from.

import (
	"fmt"
	"go/ast"
	"go/types"
	"path/filepath"

	"golang.org/x/tools/go/analysis"
)

// checkSpanContext emits UV0012 for each use of a context variable after a
// stack span was opened from it in the same function.
func checkSpanContext(pass *analysis.Pass) {
	for _, file := range pass.Files {
		reported := map[*ast.Ident]bool{} // a use nested in two checked bodies reports once
		ast.Inspect(file, func(n ast.Node) bool {
			var body *ast.BlockStmt
			switch n := n.(type) {
			case *ast.FuncDecl:
				body = n.Body
			case *ast.FuncLit:
				body = n.Body
			}
			if body == nil {
				return true
			}
			// Opens in THIS body only; a nested literal is its own body.
			ast.Inspect(body, func(n ast.Node) bool {
				if _, ok := n.(*ast.FuncLit); ok {
					return false
				}
				as, ok := n.(*ast.AssignStmt)
				if !ok || len(as.Lhs) != 2 || len(as.Rhs) != 1 {
					return true
				}
				call, ok := ast.Unparen(as.Rhs[0]).(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				// X.Span shaped like stack.Tracer's:
				// Span(context.Context, ...any) (stack.Span, func(*error)).
				fn := calleeFunc(pass, call)
				if fn == nil || fn.Name() != "Span" {
					return true
				}
				sig, res := fn.Signature(), fn.Signature().Results()
				if sig.Recv() == nil || !sig.Variadic() || sig.Params().Len() != 2 || res.Len() != 2 ||
					typeString(sig.Params().At(0).Type()) != "context.Context" ||
					typeString(res.At(0).Type()) != stackPath+".Span" {
					return true
				}
				if end, ok := res.At(1).Type().(*types.Signature); !ok || end.Params().Len() != 1 ||
					end.Results().Len() != 0 || typeString(end.Params().At(0).Type()) != "*error" {
					return true
				}
				name, _ := as.Lhs[0].(*ast.Ident)
				arg, _ := ast.Unparen(call.Args[0]).(*ast.Ident)
				if name == nil || arg == nil || name.Name == "_" {
					return true
				}
				ctx, _ := pass.TypesInfo.Uses[arg].(*types.Var)
				span := pass.TypesInfo.ObjectOf(name)
				// A parent stack.Span used after a child opens is deliberate.
				if ctx == nil || span == nil || span.Parent() == nil || span == ctx ||
					typeString(ctx.Type()) == stackPath+".Span" {
					return true
				}

				// The first write to ctx after the span retires it; the
				// write's own right-hand side still reads the stale value.
				stop := body.End()
				written := map[*ast.Ident]bool{}
				var uses []*ast.Ident
				ast.Inspect(body, func(m ast.Node) bool {
					switch m := m.(type) {
					case *ast.AssignStmt:
						for _, lhs := range m.Lhs {
							if id, ok := lhs.(*ast.Ident); ok && pass.TypesInfo.ObjectOf(id) == ctx {
								written[id] = true
								if m.Pos() > call.End() && m.End() < stop {
									stop = m.End()
								}
							}
						}
					case *ast.Ident:
						if m.Pos() > call.End() && !written[m] && pass.TypesInfo.Uses[m] == ctx &&
							span.Parent().Contains(m.Pos()) {
							uses = append(uses, m)
						}
					}
					return true
				})

				at := pass.Fset.Position(name.Pos())
				for _, u := range uses {
					if u.Pos() >= stop || reported[u] {
						continue
					}
					reported[u] = true
					pass.Report(analysis.Diagnostic{
						Pos: u.Pos(), End: u.End(),
						Message: fmt.Sprintf(
							"warning[UV0012]: %s is used after %s opened a span — pass %s: it is the context, so the call nests under the span (opened at %s:%d)",
							u.Name, name.Name, name.Name, filepath.Base(at.Filename), at.Line),
						Related: []analysis.RelatedInformation{{
							Pos: name.Pos(), End: name.End(),
							Message: fmt.Sprintf("%s opened from %s here", name.Name, u.Name),
						}},
					})
				}
				return true
			})
			return true
		})
	}
}
