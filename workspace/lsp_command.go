package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"

	"crude/types"
)

// ExecuteCodeActionCommand runs one explicit server command for the exact
// synchronized source. Any workspace/applyEdit request is captured by the
// adapter and returned as original evidence; the caller must review it through
// the canonical edit owner before any file changes.
func (m *LSPManager) ExecuteCodeActionCommand(ctx context.Context, expected types.InsightBoundary, expectedWire int, command string, arguments json.RawMessage) (types.LSPCommandResponse, error) {
	ctx, stop := context.WithTimeout(ctx, ManualLSPRequestTimeout)
	defer stop()
	if err := ctx.Err(); err != nil {
		return types.LSPCommandResponse{}, err
	}
	if command == "" || len(command) > 4096 || len(arguments) > 1<<20 {
		return types.LSPCommandResponse{}, errors.New("invalid or oversized language-server command")
	}
	if !m.featureOn(func(s types.LSPSettings) bool { return s.Enabled }) {
		return types.LSPCommandResponse{}, errors.New("code actions unavailable: language intelligence is off or blocked")
	}
	var out types.LSPCommandResponse
	err := m.trackInflight(func() error {
		if err := m.awaitSynchronized(ctx, expected.Source.Path); err != nil {
			return err
		}
		client, requestCtx, valid, cancel := m.requestClientWithin(ctx, expected.Source.Path, ManualLSPRequestTimeout)
		defer cancel()
		commandClient, ok := client.(LSPCommandClient)
		if client == nil || !ok {
			return errors.New("code actions unavailable: the language server cannot run commands")
		}
		m.mu.Lock()
		doc := m.documents[filepath.Clean(expected.Source.Path)]
		var boundary types.InsightBoundary
		wire := 0
		if doc != nil && doc.opened && !doc.syncPending {
			boundary, wire = doc.boundary, doc.wireVersion
			boundary.Context.UnknownInputs = append([]string(nil), boundary.Context.UnknownInputs...)
		}
		generation := m.contextGeneration
		m.mu.Unlock()
		if !sameInsightBoundary(boundary, expected) || wire != expectedWire || wire <= 0 ||
			boundary.Context.Generation != generation {
			return errors.New("the exact code-action source or provider is no longer synchronized")
		}
		if err := requestCtx.Err(); err != nil {
			return err
		}
		response, callErr := commandClient.ExecuteCommand(requestCtx, expected.Source.Path, wire, command, arguments)
		if err := requestCtx.Err(); err != nil {
			return err
		}
		if !valid() {
			return errors.New("code-action provider changed while the command ran")
		}
		m.mu.Lock()
		current := m.documents[filepath.Clean(expected.Source.Path)]
		exact := current == doc && current != nil && current.opened && !current.syncPending &&
			current.wireVersion == wire && sameInsightBoundary(current.boundary, boundary) &&
			generation == m.contextGeneration
		m.mu.Unlock()
		if !exact {
			return errors.New("code-action source or context changed while the command ran")
		}
		if callErr != nil && len(response.WorkspaceEdits) == 0 && response.ShowDocument == nil {
			return callErr
		}
		response.Result = bytes.Clone(response.Result)
		for i := range response.WorkspaceEdits {
			response.WorkspaceEdits[i] = bytes.Clone(response.WorkspaceEdits[i])
		}
		response.Messages = append([]string(nil), response.Messages...)
		out = response
		return nil
	})
	return out, err
}
