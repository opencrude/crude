package coreprotocol

import (
	"crude/types"
	"crude/workspace"
)

// LSP snapshot serves every synchronous shell read without remote I/O. Other
// methods are explicit worker requests; detach never stops shared intelligence.
const (
	LSPSnapshotMethod            = "lsp.snapshot"
	LSPClearDiagnostics          = "lsp.clear_diagnostics"
	LSPOpenFile                  = "lsp.open_file"
	LSPCloseFile                 = "lsp.close_file"
	LSPNotifyWatchedFilesChanged = "lsp.notify_watched_files_changed"
	LSPSetActiveFile             = "lsp.set_active_file"
	LSPStartForWorkspace         = "lsp.start_for_workspace"
	LSPStop                      = "lsp.stop"
	LSPRestartServer             = "lsp.restart_server"
	LSPStopServer                = "lsp.stop_server"
	LSPFindReferences            = "lsp.references"
	LSPRequestCompletions        = "lsp.completions"
	LSPRequestDefinition         = "lsp.definition"
	LSPRequestDocumentSymbols    = "lsp.document_symbols"
	LSPRequestHover              = "lsp.hover"
	LSPRequestRename             = "lsp.rename"
)

type LSPRequest struct {
	Path               string                       `json:"path,omitempty"`
	Text               string                       `json:"text,omitempty"`
	Line               int                          `json:"line,omitempty"`
	Col                int                          `json:"col,omitempty"`
	IncludeDeclaration bool                         `json:"include_declaration,omitempty"`
	Reason             workspace.DocumentSaveReason `json:"reason,omitempty"`
	Before             bool                         `json:"before,omitempty"`
	ChangeType         int                          `json:"change_type,omitempty"`
	ServerID           string                       `json:"server_id,omitempty"`
	NewName            string                       `json:"new_name,omitempty"`
}

type LSPResult struct {
	Applied     bool                       `json:"applied"`
	Locations   []workspace.LSPLocation    `json:"locations,omitempty"`
	Completions []workspace.CompletionItem `json:"completions,omitempty"`
	Symbols     []workspace.LSPSymbol      `json:"symbols,omitempty"`
	Hover       string                     `json:"hover,omitempty"`
	Edit        *LSPEdit                   `json:"edit,omitempty"`
}

type LSPSnapshot struct {
	Version      uint64                               `json:"version"`
	Available    bool                                 `json:"available"`
	Diagnostics  map[string][]workspace.LSPDiagnostic `json:"diagnostics"`
	Symbols      map[string][]workspace.LSPSymbol     `json:"symbols"`
	Insights     workspace.ProjectInsights            `json:"insights"`
	Health       []types.LSPServerHealth              `json:"health"`
	Settings     types.LSPSettings                    `json:"settings"`
	Connected    bool                                 `json:"connected"`
	Ready        bool                                 `json:"ready"`
	Status       workspace.LSPStatus                  `json:"status"`
	Error        string                               `json:"error,omitempty"`
	ShowInEditor bool                                 `json:"show_in_editor"`
	ShowInTree   bool                                 `json:"show_in_tree"`
}
