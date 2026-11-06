package lspedit

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"unicode/utf8"

	"crude/types"
)

const (
	MaxRawBytes         = 1 << 20
	MaxDocuments        = 200
	MaxEdits            = 1024
	MaxStringBytes      = 64 << 10
	MaxReplacementBytes = 1 << 20
	maxJSONDepth        = 64
)

// DocumentEdit retains the wire target without resolving or authorizing its URI.
// Versioned distinguishes changes-map entries from documentChanges entries.
// For a versioned entry, nil Version means explicit JSON null, never omission.
type DocumentEdit struct {
	URI       string
	Versioned bool
	Version   *int32
	Edits     []types.LSPTextEdit
	Create    bool
}

// Parse consumes original WorkspaceEdit JSON, not a generated protocol value:
// generated values can erase missing/null versions and unsupported annotations.
// Null and an empty object both mean no edits. documentChanges takes precedence
// over changes when both are supplied, but both representations must be supported
// and fit the aggregate budgets. No part of an unsupported operation is returned.
//
// In addition to the exported size/count limits, JSON nesting is capped at 64.
// Duplicate JSON members (including escaped-equivalent names) are rejected.
// Unknown nonoperative metadata is ignored, never interpreted or executed.
func Parse(raw []byte) ([]DocumentEdit, error) {
	if len(raw) > MaxRawBytes || !utf8.Valid(raw) {
		return nil, fmt.Errorf("LSP edit: invalid encoding or raw JSON byte bound exceeded")
	}
	if err := validateJSON(raw); err != nil {
		return nil, err
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	root, err := object(raw, "changes", "documentChanges")
	if err != nil {
		return nil, err
	}
	budget := parseBudget{}
	var changes, documents []DocumentEdit
	if value, ok := root["changes"]; ok {
		targets, err := decodeObject(value, MaxDocuments)
		if err != nil {
			return nil, err
		}
		uris := make([]string, 0, len(targets))
		for uri := range targets {
			if len(uri) == 0 || len(uri) > MaxStringBytes {
				return nil, fmt.Errorf("LSP edit: invalid URI or string bound exceeded")
			}
			uris = append(uris, uri)
		}
		sort.Strings(uris) // deterministic changes-map order, not URI normalization
		for _, uri := range uris {
			if err := budget.target(uri); err != nil {
				return nil, err
			}
			edits, err := budget.edits(targets[uri])
			if err != nil {
				return nil, err
			}
			changes = append(changes, DocumentEdit{URI: uri, Edits: edits})
		}
	}
	if value, ok := root["documentChanges"]; ok {
		items, err := array(value, MaxDocuments)
		if err != nil {
			return nil, err
		}
		seen := make(map[string]bool, len(items))
		for _, item := range items {
			entry, err := object(item, "textDocument", "edits")
			if err != nil {
				return nil, err
			}
			target, err := object(entry["textDocument"], "uri", "version")
			if err != nil {
				return nil, err
			}
			uri, err := stringValue(target["uri"])
			if err != nil {
				return nil, err
			}
			if seen[uri] {
				return nil, fmt.Errorf("LSP edit: duplicate document target")
			}
			seen[uri] = true
			if err := budget.target(uri); err != nil {
				return nil, err
			}
			versionRaw, present := target["version"]
			if !present {
				return nil, fmt.Errorf("LSP edit: missing document version")
			}
			var version *int32
			if !bytes.Equal(bytes.TrimSpace(versionRaw), []byte("null")) {
				number, err := integer(versionRaw, true)
				if err != nil {
					return nil, err
				}
				v := int32(number)
				version = &v
			}
			edits, err := budget.edits(entry["edits"])
			if err != nil {
				return nil, err
			}
			documents = append(documents, DocumentEdit{URI: uri, Versioned: true, Version: version, Edits: edits})
		}
		return documents, nil
	}
	return changes, nil
}

type parseBudget struct {
	documents, count, replacements int
}

func (b *parseBudget) target(uri string) error {
	b.documents++
	if b.documents > MaxDocuments || len(uri) == 0 || len(uri) > MaxStringBytes {
		return fmt.Errorf("LSP edit: invalid URI or document/string bound exceeded")
	}
	return nil
}

func (b *parseBudget) edits(raw json.RawMessage) ([]types.LSPTextEdit, error) {
	items, err := array(raw, MaxEdits-b.count)
	if err != nil {
		return nil, err
	}
	b.count += len(items)
	result := make([]types.LSPTextEdit, 0, len(items))
	for _, item := range items {
		edit, err := exactObject(item, "range", "newText")
		if err != nil {
			return nil, err
		}
		replacement, err := stringValue(edit["newText"])
		if err != nil {
			return nil, err
		}
		b.replacements += len(replacement)
		if b.replacements > MaxReplacementBytes {
			return nil, fmt.Errorf("LSP edit: aggregate replacement bound exceeded")
		}
		r, err := exactObject(edit["range"], "start", "end")
		if err != nil {
			return nil, err
		}
		line, col, err := position(r["start"])
		if err != nil {
			return nil, err
		}
		endLine, endCol, err := position(r["end"])
		if err != nil {
			return nil, err
		}
		if endLine < line || endLine == line && endCol < col {
			return nil, fmt.Errorf("LSP edit: reversed range")
		}
		result = append(result, types.LSPTextEdit{
			Line: line, Col: col, EndLine: endLine, EndCol: endCol, NewText: replacement,
		})
	}
	return result, nil
}

func position(raw json.RawMessage) (int, int, error) {
	p, err := exactObject(raw, "line", "character")
	if err != nil {
		return 0, 0, err
	}
	line, err := integer(p["line"], false)
	if err != nil {
		return 0, 0, err
	}
	col, err := integer(p["character"], false)
	return line, col, err
}

func integer(raw json.RawMessage, signed bool) (int, error) {
	n, err := strconv.ParseInt(string(bytes.TrimSpace(raw)), 10, 32)
	// ParseInt accepts a leading plus; JSON validation has already rejected it.
	if err != nil || !signed && n < 0 {
		return 0, fmt.Errorf("LSP edit: expected 32-bit integer (signed=%t)", signed)
	}
	return int(n), nil
}

func stringValue(raw json.RawMessage) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return "", fmt.Errorf("LSP edit: expected string")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	if len(value) > MaxStringBytes {
		return "", fmt.Errorf("LSP edit: consumed string bound exceeded")
	}
	return value, nil
}

// These fields affect standard edit/union semantics. They cannot be treated as
// harmless metadata in the wrong shape (in particular snippet/annotated edits
// and resource operations must never be flattened into literal text).
func operative(key string) bool {
	switch key {
	case "changes", "documentChanges", "changeAnnotations", "textDocument",
		"uri", "version", "edits", "range", "start", "end", "line", "character",
		"newText", "kind", "options", "oldUri", "newUri", "annotationId",
		"insert", "replace", "snippet", "insertText", "insertTextFormat",
		"insertTextMode", "textEdit", "command", "commands", "executeCommand":
		return true
	}
	return false
}

func object(raw json.RawMessage, allowed ...string) (map[string]json.RawMessage, error) {
	fields, err := decodeObject(raw, MaxRawBytes)
	if err != nil {
		return nil, err
	}
	for key := range fields {
		if !operative(key) {
			continue
		}
		found := false
		for _, name := range allowed {
			found = found || key == name
		}
		if !found {
			return nil, fmt.Errorf("LSP edit: unsupported operative field %q", key)
		}
	}
	return fields, nil
}

func exactObject(raw json.RawMessage, names ...string) (map[string]json.RawMessage, error) {
	fields, err := decodeObject(raw, MaxDocuments)
	if err != nil {
		return nil, err
	}
	if len(fields) != len(names) {
		return nil, fmt.Errorf("LSP edit: unexpected object member")
	}
	for _, name := range names {
		if _, exists := fields[name]; !exists {
			return nil, fmt.Errorf("LSP edit: missing object member")
		}
	}
	return fields, nil
}

func decodeObject(raw json.RawMessage, limit int) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, fmt.Errorf("LSP edit: expected object")
	}
	result := make(map[string]json.RawMessage)
	for decoder.More() {
		if len(result) >= limit {
			return nil, fmt.Errorf("LSP edit: object member bound exceeded")
		}
		key, err := decoder.Token()
		if err != nil {
			return nil, err
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		result[key.(string)] = value // validateJSON has rejected duplicates.
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return result, nil
}

func array(raw json.RawMessage, limit int) ([]json.RawMessage, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return nil, fmt.Errorf("LSP edit: expected array")
	}
	var result []json.RawMessage
	for decoder.More() {
		if len(result) >= limit {
			return nil, fmt.Errorf("LSP edit: edit/document count bound exceeded")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	if _, err := decoder.Token(); err != nil {
		return nil, err
	}
	return result, nil
}

// Validate once before decoding objects so duplicate fields cannot overwrite an
// operative meaning. UseNumber prevents even ignored numbers passing via float64.
func validateJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := jsonValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("LSP edit: trailing JSON input")
	}
	return validateSurrogates(raw)
}

func jsonValue(decoder *json.Decoder, depth int) error {
	if depth > maxJSONDepth {
		return fmt.Errorf("LSP edit: JSON nesting bound exceeded")
	}
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, container := token.(json.Delim)
	if !container {
		return nil
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for decoder.More() {
			key, err := decoder.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return fmt.Errorf("LSP edit: duplicate or invalid JSON member")
			}
			seen[name] = true
			if err := jsonValue(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err := jsonValue(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("LSP edit: unexpected JSON delimiter")
	}
	_, err = decoder.Token()
	return err
}

// encoding/json intentionally replaces unpaired escaped surrogates with U+FFFD.
// Source-edit data must fail instead of silently changing replacement/URI bytes.
// Syntax was validated above, so every escape has its required bytes.
func validateSurrogates(raw []byte) error {
	inString := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inString = !inString
		}
		if !inString || raw[i] != '\\' {
			continue
		}
		i++
		if raw[i] != 'u' {
			continue
		}
		n, _ := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
		i += 4
		if n < 0xd800 || n > 0xdfff {
			continue
		}
		if n > 0xdbff || i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
			return fmt.Errorf("LSP edit: unpaired JSON surrogate")
		}
		low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return fmt.Errorf("LSP edit: unpaired JSON surrogate")
		}
		i += 6
	}
	return nil
}
