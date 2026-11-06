package types

// LSPCommandResponse is internal adapter evidence from one explicitly executed
// server command. WorkspaceEdits are the original workspace/applyEdit requests
// that the server expected the client to apply. Nothing here is permission to
// mutate files; the canonical workspace reviews every edit first.
type LSPCommandResponse struct {
	Result         []byte
	WorkspaceEdits [][]byte
	Messages       []string
	ShowDocument   *LSPShowDocument
}

// LSPShowDocument describes a server request to show a file or URL. Selection
// is an optional zero-based LSP position in the shown document.
type LSPShowDocument struct {
	URI                   string
	External, TakeFocus   bool
	HasSelection          bool
	Line, Character       int
	EndLine, EndCharacter int
}
