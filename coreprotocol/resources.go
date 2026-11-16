package coreprotocol

import (
	"crude/document"
	"crude/indentation"
	"crude/terminalvt"
	"crude/textencoding"
	"crude/workspace"
)

const (
	DocumentOpen            = "document.open"
	DocumentCreateUntitled  = "document.create_untitled"
	DocumentOpenUntitled    = "document.open_untitled"
	DocumentSaveUntitled    = "document.save_untitled"
	DocumentReviewUntitled  = "document.review_untitled_overwrite"
	DocumentCompareUntitled = "document.compare_untitled_overwrite"
	DocumentRead            = "document.read"
	DocumentSync            = "document.sync"
	DocumentClose           = "document.close"
	DocumentEdit            = "document.edit"
	DocumentUndo            = "document.undo"
	DocumentRedo            = "document.redo"
	DocumentSave            = "document.save"
	DocumentReload          = "document.reload"
	DocumentFormat          = "document.format"
	DocumentEditorConfig    = "document.editor_config"
	ProjectFormat           = "project.format"
	TerminalList            = "terminal.list"
	TerminalCreate          = "terminal.create"
	TerminalRows            = "terminal.rows"
	TerminalControl         = "terminal.control"
	TerminalRelease         = "terminal.release"
	TerminalInput           = "terminal.input"
	TerminalResize          = "terminal.resize"
	TerminalClose           = "terminal.close"
)

type DocumentOpenRequest struct {
	Path string `json:"path"`
}
type DocumentReadRequest struct {
	ID string `json:"id"`
}
type DocumentSyncRequest struct {
	ID    string `json:"id"`
	After uint64 `json:"after"`
}
type DocumentEditRequest struct {
	ID       string          `json:"id"`
	Revision uint64          `json:"revision"`
	Group    string          `json:"group,omitempty"`
	Edits    []document.Edit `json:"edits"`
}
type DocumentRevisionRequest struct {
	ID       string                       `json:"id"`
	Revision uint64                       `json:"revision"`
	Reason   workspace.DocumentSaveReason `json:"reason,omitempty"`
	Encoding *textencoding.Encoding       `json:"encoding,omitempty"`
	// Resolve applies only to a reload: it keeps or replaces the text instead
	// of loading the file text.
	Resolve *document.Resolution `json:"resolve,omitempty"`
}
type DocumentSaveUntitledRequest struct {
	ID        string                `json:"id"`
	Directory string                `json:"directory"`
	Name      string                `json:"name"`
	Revision  uint64                `json:"revision"`
	Encoding  textencoding.Encoding `json:"encoding"`
}
type ProjectFormatRequest struct {
	RequestID string `json:"request_id,omitempty"`
}
type TerminalIDRequest struct {
	ID string `json:"id"`
	// TakeOver asks terminal.control for input control even when another
	// window holds it. Only an explicit user choice sets it.
	TakeOver bool `json:"take_over,omitempty"`
	// FromAbsent also replaces a controller whose window lost its connection
	// and waits to reconnect. Typing in this window sets it; a plain claim
	// never takes a lease from an absent controller.
	FromAbsent bool `json:"from_absent,omitempty"`
}
type TerminalRowsRequest struct {
	ID            string `json:"id"`
	Start         int    `json:"start"`
	Count         int    `json:"count"`
	ViewportStart int    `json:"viewport_start"`
	HistoryEpoch  uint64 `json:"history_epoch"`
	HistoryBase   uint64 `json:"history_base"`
}
type TerminalResizeRequest struct {
	Lease workspace.TerminalLease `json:"lease"`
	Cols  int                     `json:"cols"`
	Rows  int                     `json:"rows"`
}
type TerminalInputRequest struct {
	Lease   workspace.TerminalLease `json:"lease"`
	Kind    string                  `json:"kind"`
	Key     terminalvt.Key          `json:"key,omitempty"`
	Mouse   terminalvt.Mouse        `json:"mouse,omitempty"`
	Data    []byte                  `json:"data,omitempty"`
	Focused bool                    `json:"focused,omitempty"`
}

type EditorConfigReply struct {
	Path    string
	Choices indentation.Choices
}
