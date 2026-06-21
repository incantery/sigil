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
