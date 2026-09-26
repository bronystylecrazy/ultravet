# ultravet

The static analyzer for the [di](https://github.com/bronystylecrazy/di)
kernel: wiring errors at keystroke time, before anything compiles, let alone
boots.

```
error[DI0001]: no provider for *pg.Config (needed by NewDB)
```

The registration API is declarative, so `di.Provide(NewDB)` is analyzable:
ultravet rebuilds the dependency graph from source and reports at the
assembly site with the same codes `ultra explain` teaches. When an assembly
holds anything it cannot resolve statically, it stays silent rather than
guess — `Validate()` is the runtime covenant; ultravet is the earlier,
zero-false-positive net.

```
go run github.com/bronystylecrazy/ultravet/cmd/ultravet@latest ./...
go vet -vettool=$(which ultravet) ./...
```

| | |
|---|---|
| `cmd/ultravet` | the CLI and vet tool: `-fix`, `-format json\|github` (github is automatic in Actions) |
| `cmd/ultravet-lsp` | the language server, beside gopls — diagnostics on open and save, "Register NewX" quick-fixes |
| `lsp` | the server itself |
| `editors/vscode` | the VS Code extension wrapping `ultravet-lsp` |

It also knows [ultrastack](https://github.com/bronystylecrazy/ultrastack)'s
assembly roots and presets, and runs its doctrine checks (`UVxxxx`) on
products built with it; `ultra vet` delegates here.

## License

MIT
