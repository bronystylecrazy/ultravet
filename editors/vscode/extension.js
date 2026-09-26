// ultravet VS Code extension: the thinnest possible wrapper — spawn
// ultravet-lsp and let LSP do everything. gopls keeps its jobs; this
// server only publishes wiring diagnostics and quick-fixes.
const { workspace } = require('vscode')
const { LanguageClient } = require('vscode-languageclient/node')

let client

function activate() {
  const serverPath = workspace.getConfiguration('ultravet').get('serverPath') || 'ultravet-lsp'
  client = new LanguageClient(
    'ultravet',
    'ultravet',
    { command: serverPath, args: [] },
    {
      documentSelector: [{ scheme: 'file', language: 'go' }],
      synchronize: { fileEvents: workspace.createFileSystemWatcher('**/*.go') },
    },
  )
  client.start()
}

function deactivate() {
  return client ? client.stop() : undefined
}

module.exports = { activate, deactivate }
