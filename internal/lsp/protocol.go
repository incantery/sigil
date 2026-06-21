package lsp

// Subset of the LSP types this server uses. Field names/JSON tags follow the
// LSP spec so editors interoperate.

const TextDocumentSyncFull = 1

type InitializeParams struct {
	RootURI          string            `json:"rootUri"`
	WorkspaceFolders []WorkspaceFolder `json:"workspaceFolders"`
}

type WorkspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}

type InitializeResult struct {
	Capabilities ServerCapabilities `json:"capabilities"`
}

type ServerCapabilities struct {
	TextDocumentSync       int  `json:"textDocumentSync"`
	DocumentSymbolProvider bool `json:"documentSymbolProvider"`
}

type TextDocumentItem struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
	Text    string `json:"text"`
}

type TextDocumentIdentifier struct {
	URI string `json:"uri"`
}

type DidOpenParams struct {
	TextDocument TextDocumentItem `json:"textDocument"`
}

type DidChangeParams struct {
	TextDocument struct {
		URI     string `json:"uri"`
		Version int    `json:"version"`
	} `json:"textDocument"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
}

type DidCloseParams struct {
	TextDocument TextDocumentIdentifier `json:"textDocument"`
}
