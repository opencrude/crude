package coreprotocol

import (
	"crude/document"
	"crude/terminalvt"
	"crude/workspace"
)

const (
	DocumentOpen    = "document.open"
	DocumentRead    = "document.read"
	DocumentSync    = "document.sync"
	DocumentClose   = "document.close"
	DocumentEdit    = "document.edit"
	DocumentUndo    = "document.undo"
	DocumentRedo    = "document.redo"
	DocumentSave    = "document.save"
	DocumentFormat  = "document.format"
	TerminalList    = "terminal.list"
	TerminalCreate  = "terminal.create"
	TerminalRows    = "terminal.rows"
	TerminalControl = "terminal.control"
	TerminalRelease = "terminal.release"
	TerminalInput   = "terminal.input"
	TerminalResize  = "terminal.resize"
	TerminalClose   = "terminal.close"
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
}
type TerminalIDRequest struct {
	ID string `json:"id"`
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
