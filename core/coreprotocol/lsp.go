package coreprotocol

import (
	"crude/types/insight"
	"crude/types/setting"
	"crude/types/statuscenter"
	"crude/workspace"
)

// LSP snapshot serves every synchronous shell read without remote I/O. Other
// methods are explicit worker requests; detach never stops shared intelligence.
const (
	LSPSnapshotMethod            = "lsp.snapshot"
	LSPNotifyWatchedFilesChanged = "lsp.notify_watched_files_changed"
	LSPSetActiveFile             = "lsp.set_active_file"
	LSPStartForWorkspace         = "lsp.start_for_workspace"
	LSPStop                      = "lsp.stop"
	LSPRefresh                   = "lsp.refresh"
	LSPRestartServer             = "lsp.restart_server"
	LSPStopServer                = "lsp.stop_server"
	LSPRequestDocumentSymbols    = "lsp.document_symbols"
	LSPRequestHover              = "lsp.hover"
	LSPRequestSignatureHelp      = "lsp.signature_help"
)

type LSPRequest struct {
	Trigger    insight.LSPSignatureContext  `json:"trigger"`
	Path       string                       `json:"path,omitempty"`
	Line       int                          `json:"line,omitempty"`
	Col        int                          `json:"col,omitempty"`
	Reason     workspace.DocumentSaveReason `json:"reason,omitempty"`
	Before     bool                         `json:"before,omitempty"`
	ChangeType int                          `json:"change_type,omitempty"`
	ServerID   string                       `json:"server_id,omitempty"`
}

type LSPResult struct {
	Applied       bool                     `json:"applied"`
	Symbols       []insight.LSPSymbol      `json:"symbols,omitempty"`
	Hover         string                   `json:"hover,omitempty"`
	SignatureHelp insight.LSPSignatureHelp `json:"signature_help"`
}

type LSPSnapshot struct {
	Generation           string                                       `json:"generation"`
	ContextGeneration    uint64                                       `json:"context_generation"`
	Version              uint64                                       `json:"version"`
	Available            bool                                         `json:"available"`
	Diagnostics          map[string]insight.Diagnostics               `json:"diagnostics"`
	Symbols              map[string][]insight.LSPSymbol               `json:"symbols"`
	Insights             insight.ProjectInsights                      `json:"insights"`
	Health               []statuscenter.LSPServerHealth               `json:"health"`
	Assembly             insight.AssemblyPlan                         `json:"assembly"`
	Settings             setting.LSP                                  `json:"settings"`
	Assistance           map[string]insight.LSPAssistanceCapabilities `json:"assistance"`
	Connected            bool                                         `json:"connected"`
	Ready                bool                                         `json:"ready"`
	Status               insight.LSPStatus                            `json:"status"`
	Error                string                                       `json:"error,omitempty"`
	ShowInEditor         bool                                         `json:"show_in_editor"`
	ShowInTree           bool                                         `json:"show_in_tree"`
	ShowInlays           bool                                         `json:"show_inlays"`
	InlayGeneration      uint64                                       `json:"inlay_generation"`
	ShowCompletions      bool                                         `json:"show_completions"`
	CompletionGeneration uint64                                       `json:"completion_generation"`
}
