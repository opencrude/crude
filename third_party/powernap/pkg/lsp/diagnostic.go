package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/sourcegraph/jsonrpc2"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// Pull diagnostics (LSP 3.17 textDocument/diagnostic, workspace/diagnostic).
// This file negotiates the capability and carries the raw reports; which
// documents to pull, and when, belongs to the client's owner.

const (
	// MaxDocumentDiagnosticBytes bounds one document report.
	MaxDocumentDiagnosticBytes = 4 << 20
	// MaxWorkspaceDiagnosticBytes bounds one workspace report or partial result.
	MaxWorkspaceDiagnosticBytes = 64 << 20
	maxDiagnosticSelectors      = 64
	maxDiagnosticIdentifier     = 256
)

var ErrDiagnosticInvalid = errors.New("invalid pull diagnostic provider, request or response")

// DiagnosticFilter is one text document filter of a provider's document
// selector. Empty fields match every value. Notebook cell filters never match.
type DiagnosticFilter struct {
	Language, Scheme, Pattern string
	Notebook                  bool
}

// DiagnosticSupport is one pull diagnostic provider: the static server
// capability or one dynamic registration. A nil Selector covers every
// document the client synchronizes with the server.
type DiagnosticSupport struct {
	Identifier            string
	InterFileDependencies bool
	WorkspaceDiagnostics  bool
	Selector              []DiagnosticFilter
}

// PreviousResultID names the last report the client keeps for one document.
type PreviousResultID struct {
	URI   protocol.DocumentURI `json:"uri"`
	Value string               `json:"value"`
}

// DiagnosticClientCapabilities returns a fresh textDocument.diagnostic leaf.
// The client keeps diagnostics in the publishDiagnostics shape: related
// documents and markup messages are off, as in VS Code's client.
func DiagnosticClientCapabilities() map[string]any {
	return map[string]any{
		"dynamicRegistration":    true,
		"relatedDocumentSupport": false,
		"markupMessageSupport":   false,
	}
}

// DiagnosticProvider returns the static pull diagnostic capability.
func (c *Client) DiagnosticProvider() (DiagnosticSupport, bool, error) {
	if !c.initialized.Load() || c.capabilities.DiagnosticProvider == nil || c.capabilities.DiagnosticProvider.Value == nil {
		return DiagnosticSupport{}, false, nil
	}
	raw, err := json.Marshal(c.capabilities.DiagnosticProvider.Value)
	if err != nil {
		return DiagnosticSupport{}, false, ErrDiagnosticInvalid
	}
	support, err := ParseDiagnosticSupport(raw)
	return support, err == nil, err
}

// ParseDiagnosticSupport decodes DiagnosticOptions or
// DiagnosticRegistrationOptions, from initialize or client/registerCapability.
func ParseDiagnosticSupport(raw json.RawMessage) (DiagnosticSupport, error) {
	var options struct {
		Identifier            string            `json:"identifier"`
		InterFileDependencies bool              `json:"interFileDependencies"`
		WorkspaceDiagnostics  bool              `json:"workspaceDiagnostics"`
		DocumentSelector      []json.RawMessage `json:"documentSelector"`
	}
	if err := json.Unmarshal(raw, &options); err != nil {
		return DiagnosticSupport{}, ErrDiagnosticInvalid
	}
	if len(options.Identifier) > maxDiagnosticIdentifier || !utf8.ValidString(options.Identifier) ||
		strings.IndexByte(options.Identifier, 0) >= 0 || len(options.DocumentSelector) > maxDiagnosticSelectors {
		return DiagnosticSupport{}, ErrDiagnosticInvalid
	}
	support := DiagnosticSupport{Identifier: options.Identifier, InterFileDependencies: options.InterFileDependencies,
		WorkspaceDiagnostics: options.WorkspaceDiagnostics}
	if options.DocumentSelector == nil {
		return support, nil
	}
	support.Selector = []DiagnosticFilter{}
	for _, entry := range options.DocumentSelector {
		var filter struct {
			Language string          `json:"language"`
			Scheme   string          `json:"scheme"`
			Pattern  json.RawMessage `json:"pattern"`
			Notebook json.RawMessage `json:"notebook"`
		}
		if json.Unmarshal(entry, &filter) != nil {
			return DiagnosticSupport{}, ErrDiagnosticInvalid
		}
		pattern, err := globPattern(filter.Pattern)
		if err != nil {
			return DiagnosticSupport{}, err
		}
		support.Selector = append(support.Selector, DiagnosticFilter{Language: filter.Language, Scheme: filter.Scheme,
			Pattern: pattern, Notebook: len(filter.Notebook) != 0})
	}
	return support, nil
}

// globPattern accepts a GlobPattern: a pattern string or a RelativePattern
// whose base URI or workspace folder is joined in front of its pattern.
func globPattern(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return "", nil
	}
	var pattern string
	if json.Unmarshal(raw, &pattern) == nil {
		return pattern, nil
	}
	var relative struct {
		BaseURI json.RawMessage `json:"baseUri"`
		Pattern string          `json:"pattern"`
	}
	if json.Unmarshal(raw, &relative) != nil || relative.Pattern == "" {
		return "", ErrDiagnosticInvalid
	}
	var base string
	if json.Unmarshal(relative.BaseURI, &base) != nil {
		var folder struct {
			URI string `json:"uri"`
		}
		if json.Unmarshal(relative.BaseURI, &folder) != nil {
			return "", ErrDiagnosticInvalid
		}
		base = folder.URI
	}
	path, err := protocol.DocumentURI(base).Path()
	if err != nil {
		return "", ErrDiagnosticInvalid
	}
	return strings.TrimSuffix(path, "/") + "/" + strings.TrimPrefix(relative.Pattern, "/"), nil
}

// RequestDocumentDiagnostics pulls one synchronized document's report. The
// raw response is returned for the consumer to validate against its document.
func (c *Client) RequestDocumentDiagnostics(ctx context.Context, uri protocol.DocumentURI, identifier, previousResultID string) (json.RawMessage, error) {
	if !c.initialized.Load() {
		return nil, errors.New("client not initialized")
	}
	if !validDiagnosticText(string(uri), 4096) || !validDiagnosticText(identifier, maxDiagnosticIdentifier) || !validDiagnosticText(previousResultID, 4096) {
		return nil, ErrDiagnosticInvalid
	}
	params := map[string]any{"textDocument": protocol.TextDocumentIdentifier{URI: uri}}
	if identifier != "" {
		params["identifier"] = identifier
	}
	if previousResultID != "" {
		params["previousResultId"] = previousResultID
	}
	var raw json.RawMessage
	if err := c.conn.CallCancelable(ctx, "textDocument/diagnostic", params, &raw); err != nil {
		return nil, fmt.Errorf("document diagnostic request failed: %w", err)
	}
	return boundedReport(raw, MaxDocumentDiagnosticBytes)
}

// RequestWorkspaceDiagnostics pulls the workspace report. The server may keep
// the request open and stream partial results to partialToken through
// $/progress; the final response may then hold only the remaining items.
func (c *Client) RequestWorkspaceDiagnostics(ctx context.Context, identifier string, previous []PreviousResultID, partialToken string) (json.RawMessage, error) {
	if !c.initialized.Load() {
		return nil, errors.New("client not initialized")
	}
	if !validDiagnosticText(identifier, maxDiagnosticIdentifier) || !validDiagnosticText(partialToken, 256) {
		return nil, ErrDiagnosticInvalid
	}
	if previous == nil {
		previous = []PreviousResultID{}
	}
	params := map[string]any{"previousResultIds": previous}
	if identifier != "" {
		params["identifier"] = identifier
	}
	if partialToken != "" {
		params["partialResultToken"] = partialToken
	}
	var raw json.RawMessage
	if err := c.conn.CallCancelable(ctx, "workspace/diagnostic", params, &raw); err != nil {
		return nil, fmt.Errorf("workspace diagnostic request failed: %w", err)
	}
	return boundedReport(raw, MaxWorkspaceDiagnosticBytes)
}

// DiagnosticRetrigger reports whether err is the server's ServerCancelled
// answer and whether it asks the client to pull again. Without data the
// specification defaults to retriggering.
func DiagnosticRetrigger(err error) (cancelled, retrigger bool) {
	var rpc *jsonrpc2.Error
	if !errors.As(err, &rpc) || rpc.Code != int64(protocol.ServerCancelled) {
		return false, false
	}
	if rpc.Data == nil {
		return true, true
	}
	var data struct {
		RetriggerRequest *bool `json:"retriggerRequest"`
	}
	if json.Unmarshal(*rpc.Data, &data) != nil || data.RetriggerRequest == nil {
		return true, true
	}
	return true, *data.RetriggerRequest
}

func boundedReport(raw json.RawMessage, limit int) (json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || len(raw) > limit || !utf8.Valid(raw) || !json.Valid(raw) || raw[0] != '{' {
		return nil, ErrDiagnosticInvalid
	}
	return bytes.Clone(raw), nil
}

func validDiagnosticText(text string, limit int) bool {
	return len(text) <= limit && utf8.ValidString(text) && strings.IndexByte(text, 0) < 0
}
