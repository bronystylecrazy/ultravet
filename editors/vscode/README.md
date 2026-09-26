# ultravet for VS Code

ultrastack DI wiring diagnostics in the editor, beside gopls: missing
providers (DI0001, with a "Register NewX" quick-fix), ambiguity, cycles,
module privacy, captive scoped deps, dialing constructors — on open and
save, with clickable "declared here" related spans.

## Install

    GOPRIVATE=github.com/bronystylecrazy/* go install github.com/bronystylecrazy/ultrastack/analyzer/cmd/ultravet-lsp@latest
    code --install-extension ultravet-0.1.0.vsix

The extension finds `ultravet-lsp` on PATH; override with the
`ultravet.serverPath` setting.

## Build

    bun install
    bun run build
    bunx @vscode/vsce package
