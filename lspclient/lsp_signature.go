package lspclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"crude/types"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

const (
	maxSignatureBytes      = 64 << 10
	maxSignatures          = 32
	maxSignatureParameters = 64
)

var errSignatureResult = errors.New("invalid or oversized language-server parameter information")

// RequestSignatureHelp keeps provider/wire-version identity through the request.
// Validation and conversion run off Update; label offsets are UTF-16, never
// visual columns. The transport's frame bound is separate from this result bound.
func (l *LSPIntegration) RequestSignatureHelp(ctx context.Context, filePath string, line, col int) (types.LSPSignatureHelp, error) {
	defer l.observeRequest("signature", time.Now())
	l.mu.Lock()
	fp := filepath.Clean(filePath)
	state := l.openFiles[fp]
	client := l.client
	version := stateVersion(state)
	if client == nil || state == nil {
		l.mu.Unlock()
		return types.LSPSignatureHelp{}, errors.New("file not open in LSP")
	}
	text, uri := state.text, state.uri
	l.mu.Unlock()
	if !signaturePosition(text, line, col) {
		return types.LSPSignatureHelp{}, errors.New("parameter information needs a valid UTF-16 source position")
	}
	raw, err := client.RequestSignatureHelp(ctx, uri, protocol.Position{
		Line: uint32(line), Character: uint32(col), // bounded by signaturePosition
	})
	if !l.currentDocument(fp, client, state, version) {
		return types.LSPSignatureHelp{}, context.Canceled
	}
	if err != nil {
		return types.LSPSignatureHelp{}, err
	}
	return parseSignatureHelp(raw)
}

func signaturePosition(text string, line, col int) bool {
	if line < 0 || col < 0 || uint64(line) > math.MaxUint32 || uint64(col) > math.MaxUint32 {
		return false
	}
	for range line {
		_, rest, ok := strings.Cut(text, "\n")
		if !ok {
			return false
		}
		text = rest
	}
	content, _, _ := strings.Cut(text, "\n")
	_, ok := signatureOffset(strings.TrimSuffix(content, "\r"), col)
	return ok
}

func signatureOffset(label string, offset int) (int, bool) {
	if offset < 0 {
		return 0, false
	}
	units := 0
	for index, r := range label {
		if units == offset {
			return index, true
		}
		units += utf16.RuneLen(r)
		if units > offset {
			return 0, false
		}
	}
	return len(label), units == offset
}

// The wire protocol's generated uint32 fields cannot distinguish omitted
// signature-level activeParameter from an explicit zero overriding the result.
// This focused DTO preserves that distinction without changing generated code.
func parseSignatureHelp(raw json.RawMessage) (types.LSPSignatureHelp, error) {
	var out types.LSPSignatureHelp
	if len(raw) > maxSignatureBytes || !utf8.Valid(raw) {
		return out, errSignatureResult
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return out, nil
	}
	type parameter struct {
		Label         json.RawMessage `json:"label"`
		Documentation json.RawMessage `json:"documentation"`
	}
	var input struct {
		Signatures *[]struct {
			Label           string          `json:"label"`
			Documentation   json.RawMessage `json:"documentation"`
			Parameters      []parameter     `json:"parameters"`
			ActiveParameter json.RawMessage `json:"activeParameter"`
		} `json:"signatures"`
		ActiveSignature json.RawMessage `json:"activeSignature"`
		ActiveParameter json.RawMessage `json:"activeParameter"`
	}
	if json.Unmarshal(raw, &input) != nil || input.Signatures == nil || len(*input.Signatures) > maxSignatures {
		return out, errSignatureResult
	}
	activeSignature, err := signatureIndex(input.ActiveSignature)
	if err != nil {
		return out, err
	}
	activeParameter, err := signatureIndex(input.ActiveParameter)
	if err != nil {
		return out, err
	}
	if int64(activeSignature) < int64(len(*input.Signatures)) {
		out.ActiveSignature = int(activeSignature)
	}
	for _, signature := range *input.Signatures {
		if signature.Label == "" || len(signature.Parameters) > maxSignatureParameters {
			return types.LSPSignatureHelp{}, errSignatureResult
		}
		doc, err := signatureDocumentation(signature.Documentation)
		if err != nil {
			return types.LSPSignatureHelp{}, err
		}
		item := types.LSPSignature{Label: signature.Label, Documentation: doc, ActiveParameter: -1}
		active := activeParameter
		if len(signature.ActiveParameter) > 0 {
			active, err = signatureIndex(signature.ActiveParameter)
			if err != nil {
				return types.LSPSignatureHelp{}, err
			}
		}
		if len(signature.Parameters) > 0 {
			item.ActiveParameter = 0
			if int64(active) < int64(len(signature.Parameters)) {
				item.ActiveParameter = int(active)
			}
		}
		for _, parameter := range signature.Parameters {
			label, err := signatureParameterLabel(signature.Label, parameter.Label)
			if err != nil {
				return types.LSPSignatureHelp{}, err
			}
			doc, err := signatureDocumentation(parameter.Documentation)
			if err != nil {
				return types.LSPSignatureHelp{}, err
			}
			item.Parameters = append(item.Parameters, types.LSPSignatureParameter{Label: label, Documentation: doc})
		}
		out.Signatures = append(out.Signatures, item)
	}
	return out, nil
}

func signatureIndex(raw json.RawMessage) (uint32, error) {
	var index uint32
	if len(raw) == 0 {
		return index, nil
	}
	// We do not advertise noActiveParameterSupport. Null is not an omitted
	// field and must not be fabricated as "parameter zero is active".
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &index) != nil {
		return 0, errSignatureResult
	}
	return index, nil
}

func signatureParameterLabel(signature string, raw json.RawMessage) (string, error) {
	var label string
	if json.Unmarshal(raw, &label) == nil && label != "" && strings.Contains(signature, label) {
		return label, nil
	}
	var offsets []int
	if json.Unmarshal(raw, &offsets) != nil || len(offsets) != 2 || offsets[0] > offsets[1] {
		return "", errSignatureResult
	}
	start, startOK := signatureOffset(signature, offsets[0])
	end, endOK := signatureOffset(signature, offsets[1])
	if !startOK || !endOK || start == end {
		return "", errSignatureResult
	}
	return signature[start:end], nil
}

func signatureDocumentation(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return "", nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	var markup struct {
		Kind  string `json:"kind"`
		Value string `json:"value"`
	}
	if json.Unmarshal(raw, &markup) != nil || markup.Kind != "plaintext" && markup.Kind != "markdown" {
		return "", errSignatureResult
	}
	if markup.Kind == "markdown" {
		markup.Value = stripMarkdown(strings.ReplaceAll(markup.Value, "**", ""))
	}
	return markup.Value, nil
}
