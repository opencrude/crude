package lspclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"time"

	"crude/types"

	"github.com/charmbracelet/x/powernap/pkg/lsp"
)

const (
	maxCapturedWorkspaceEdits = 8
	maxCapturedCommandMessage = 4 << 10
	maxCapturedCommandBytes   = 2 << 20
)

// ExecuteCommand runs one server-advertised command for the exact synchronized
// source. Server-initiated workspace edits are captured, never applied here, so
// the canonical workspace can review them through its normal edit owner.
func (l *LSPIntegration) ExecuteCommand(ctx context.Context, path string, wireVersion int, command string, arguments json.RawMessage) (types.LSPCommandResponse, error) {
	defer l.observeRequest("executeCommand", time.Now())
	fp := filepath.Clean(path)
	l.mu.Lock()
	if err := l.availableLocked(ctx); err != nil {
		l.mu.Unlock()
		return types.LSPCommandResponse{}, err
	}
	client, state := l.client, l.openFiles[fp]
	if state == nil || state.version != wireVersion {
		l.mu.Unlock()
		return types.LSPCommandResponse{}, context.Canceled
	}
	if l.command != nil {
		l.mu.Unlock()
		return types.LSPCommandResponse{}, errors.New("another language-server command is running")
	}
	capture := &commandCapture{}
	l.command = capture
	l.mu.Unlock()

	if !l.currentDocument(fp, client, state, wireVersion) {
		l.endCommand(capture)
		return types.LSPCommandResponse{}, context.Canceled
	}
	result, err := client.ExecuteCommand(ctx, command, arguments)
	response := l.endCommand(capture)
	if !l.currentDocument(fp, client, state, wireVersion) {
		return types.LSPCommandResponse{}, context.Canceled
	}
	if canceled := ctx.Err(); canceled != nil {
		return types.LSPCommandResponse{}, canceled
	}
	response.Result = bytes.Clone(result)
	if err != nil && len(response.WorkspaceEdits) == 0 && response.ShowDocument == nil {
		return response, err
	}
	// A command that expected applyEdit may report failure because this client
	// kept the edit for human review. Its captured evidence remains authoritative.
	return response, nil
}

func (l *LSPIntegration) endCommand(capture *commandCapture) types.LSPCommandResponse {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.command != capture {
		return types.LSPCommandResponse{}
	}
	l.command = nil
	response := types.LSPCommandResponse{
		WorkspaceEdits: make([][]byte, len(capture.edits)),
		Messages:       append([]string(nil), capture.messages...),
		ShowDocument:   cloneShowDocument(capture.show),
	}
	for i, edit := range capture.edits {
		response.WorkspaceEdits[i] = bytes.Clone(edit)
	}
	return response
}

func cloneShowDocument(in *types.LSPShowDocument) *types.LSPShowDocument {
	if in == nil {
		return nil
	}
	out := *in
	return &out
}

// captureApplyEdit answers with applied:false. No source or disk mutation is
// admitted here; the returned edit is later reviewed and applied by the file
// owner. A command that requires an immediate apply therefore reports failure,
// which ExecuteCommand preserves only as context for captured review evidence.
func (l *LSPIntegration) captureApplyEdit(client *lsp.Client, params json.RawMessage) (any, error) {
	refused := map[string]any{"applied": false, "failureReason": "Crude reviews this change before applying it."}
	var request struct {
		Edit json.RawMessage `json:"edit"`
	}
	if len(params) > maxCapturedCommandBytes || json.Unmarshal(params, &request) != nil ||
		len(bytes.TrimSpace(request.Edit)) == 0 || !json.Valid(request.Edit) {
		return refused, errors.New("invalid workspace/applyEdit request")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.client != client || l.command == nil || len(l.command.edits) >= maxCapturedWorkspaceEdits {
		return refused, nil
	}
	l.command.edits = append(l.command.edits, bytes.Clone(request.Edit))
	return refused, nil
}

func (l *LSPIntegration) captureShowDocument(client *lsp.Client, params json.RawMessage) (any, error) {
	if len(params) > maxCapturedCommandMessage {
		return map[string]any{"success": false}, nil
	}
	var request struct {
		URI                 string `json:"uri"`
		External, TakeFocus bool
		Selection           *struct {
			Start struct{ Line, Character int } `json:"start"`
			End   struct{ Line, Character int } `json:"end"`
		} `json:"selection"`
	}
	if json.Unmarshal(params, &request) != nil || request.URI == "" || len(request.URI) > 4096 {
		return map[string]any{"success": false}, nil
	}
	show := &types.LSPShowDocument{URI: request.URI, External: request.External, TakeFocus: request.TakeFocus}
	if request.Selection != nil {
		show.HasSelection = true
		show.Line, show.Character = request.Selection.Start.Line, request.Selection.Start.Character
		show.EndLine, show.EndCharacter = request.Selection.End.Line, request.Selection.End.Character
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.client != client || l.command == nil {
		return map[string]any{"success": false}, nil
	}
	if l.command.show == nil {
		l.command.show = show
	}
	return map[string]any{"success": true}, nil
}

func (l *LSPIntegration) captureShowMessage(client *lsp.Client, params json.RawMessage) {
	if len(params) > maxCapturedCommandMessage {
		return
	}
	var message struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(params, &message) != nil || strings.TrimSpace(message.Message) == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.client != client || l.command == nil || len(l.command.messages) >= 8 {
		return
	}
	l.command.messages = append(l.command.messages, message.Message)
}
