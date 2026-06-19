// Sigil VS Code extension: a thin client around `sigil lsp`.
//
// The TextMate grammar in syntaxes/ gives instant lexical coloring; the
// language server then layers compiler-accurate semantic tokens,
// diagnostics (same pipeline as `sigil check`), document symbols,
// go-to-definition, and hover on top.

const vscode = require('vscode');
const { LanguageClient } = require('vscode-languageclient/node');

let client;

function activate(context) {
  const serverPath = vscode.workspace
    .getConfiguration('sigil')
    .get('serverPath', 'sigil');

  client = new LanguageClient(
    'sigil',
    'Sigil Language Server',
    { command: serverPath, args: ['lsp'] },
    {
      documentSelector: [
        { scheme: 'file', language: 'sigil' },
        { scheme: 'untitled', language: 'sigil' },
      ],
    },
  );

  client.start().catch((err) => {
    vscode.window.showWarningMessage(
      `Sigil: could not start \`${serverPath} lsp\` (${err.message}). ` +
        'Install the CLI (make install) or set sigil.serverPath. ' +
        'Syntax highlighting still works without it.',
    );
  });

  context.subscriptions.push({ dispose: () => client && client.stop() });
}

function deactivate() {
  return client ? client.stop() : undefined;
}

module.exports = { activate, deactivate };
