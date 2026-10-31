// Package lspaction passively parses bounded literal LSP code actions. It neither
// resolves providers nor authorizes URIs, source identity, or mutations.
package lspaction

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"unicode/utf8"

	"crude/lspedit"
)

const (
	maxListBytes = 1 << 20
	maxItems     = 64
	maxItemBytes = 64 << 10
	maxDepth     = 64
	maxTitle     = 4 << 10
	maxKind      = 256
	maxReason    = 4 << 10
	maxVisible   = 64 << 10
)

// Action is detached evidence, not authority. Raw is the original item JSON;
// Edit is its original edit leaf, including unsupported shapes. Neither belongs
// in shell DTOs. A row is eligible for later domain review only when it has Edit,
// no UnsupportedReason and no disabled object; NeedsResolve is never eligibility.
// Optional preferred presence and open kind strings are preserved literally.
type Action struct {
	Title, Kind                       string
	Preferred, HasPreferred           bool
	DisabledReason, UnsupportedReason string
	NeedsResolve                      bool
	Raw, Edit                         json.RawMessage
}

// Parse observes null/[] as empty. Malformed JSON, ambiguous duplicate members,
// invalid display types and budget violations reject the complete response.
// Unsupported operations remain visible rows; no edit is partially accepted.
// Limits: 1 MiB list, 64 items, 64 KiB/item, 64 container levels (including the
// list), 4 KiB title/disabled reason, 256-byte kind and 64 KiB total visible text.
// Source-relative endpoint/overlap checks and same-active-URI authorization
// necessarily belong to the later canonical-source owner, not this parser.
func Parse(raw []byte, resolveSupported bool) ([]Action, error) {
	if len(raw) > maxListBytes {
		return nil, fmt.Errorf("LSP action: list byte bound exceeded")
	}
	if err := validateJSON(raw, 0); err != nil {
		return nil, err
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return nil, fmt.Errorf("LSP action: expected action list or null")
	}
	var result []Action
	visible := 0
	for decoder.More() {
		if len(result) == maxItems {
			return nil, fmt.Errorf("LSP action: item count bound exceeded")
		}
		var item json.RawMessage
		if err := decoder.Decode(&item); err != nil {
			return nil, err
		}
		action, err := parseItem(item, resolveSupported)
		if err != nil {
			return nil, err
		}
		visible += len(action.Title) + len(action.Kind) + len(action.DisabledReason) + len(action.UnsupportedReason)
		if visible > maxVisible {
			return nil, fmt.Errorf("LSP action: aggregate visible text bound exceeded")
		}
		result = append(result, action)
	}
	return result, nil // validateJSON already checked closure and trailing input.
}

// Resolve sends nothing. The caller must separately gate provider resolve
// support and retain exact provider/source identity. Original must be a lazy,
// supported literal. Resolution may add only edit, never change/remove an
// existing field or introduce another field. JSON string spelling, whitespace
// and key order are immaterial; number tokens are compared without conversion
// or rounding (even 1, 1.0 and 1e0 remain distinct).
func Resolve(originalRaw, resolvedRaw []byte) (Action, error) {
	for _, raw := range [][]byte{originalRaw, resolvedRaw} {
		if len(raw) > maxItemBytes {
			return Action{}, fmt.Errorf("LSP action: item byte bound exceeded")
		}
		// Reserve the list level so list and resolve item budgets agree.
		if err := validateJSON(raw, 1); err != nil {
			return Action{}, err
		}
	}
	original, err := parseItem(originalRaw, true)
	if err != nil {
		return Action{}, err
	}
	if !original.NeedsResolve || original.UnsupportedReason != "" || len(original.Edit) != 0 {
		return Action{}, fmt.Errorf("LSP action: original is not a supported resolvable literal")
	}
	before, err := shape(originalRaw)
	if err != nil {
		return Action{}, err
	}
	after, err := shape(resolvedRaw)
	if err != nil {
		return Action{}, err
	}
	if len(after) != len(before)+1 {
		return Action{}, fmt.Errorf("LSP action: resolve must add only edit")
	}
	if _, ok := after["edit"]; !ok {
		return Action{}, fmt.Errorf("LSP action: resolve did not add edit")
	}
	for name, value := range before {
		replacement, exists := after[name]
		if !exists || !reflect.DeepEqual(value, replacement) {
			return Action{}, fmt.Errorf("LSP action: resolve changed an existing attribute")
		}
	}
	action, err := parseItem(resolvedRaw, false)
	if err != nil {
		return Action{}, err
	}
	if len(action.Edit) == 0 || action.UnsupportedReason != "" || action.NeedsResolve {
		return Action{}, fmt.Errorf("LSP action: resolved action is not supported edit-only")
	}
	return action, nil
}

func parseItem(raw []byte, resolveSupported bool) (Action, error) {
	if len(raw) > maxItemBytes {
		return Action{}, fmt.Errorf("LSP action: item byte bound exceeded")
	}
	fields, err := object(raw)
	if err != nil {
		return Action{}, err
	}
	var action Action
	if action.Title, err = text(fields["title"], maxTitle); err != nil {
		return Action{}, err
	}
	if value, exists := fields["kind"]; exists {
		if action.Kind, err = text(value, maxKind); err != nil {
			return Action{}, err
		}
	}
	if value, exists := fields["isPreferred"]; exists {
		if !bytes.Equal(value, []byte("true")) && !bytes.Equal(value, []byte("false")) {
			return Action{}, fmt.Errorf("LSP action: expected isPreferred boolean")
		}
		action.HasPreferred = true
		action.Preferred = bytes.Equal(value, []byte("true"))
	}
	_, disabled := fields["disabled"]
	if disabled {
		value, err := object(fields["disabled"])
		if err != nil {
			return Action{}, err
		}
		if action.DisabledReason, err = text(value["reason"], maxReason); err != nil {
			return Action{}, err
		}
	}
	if value, exists := fields["diagnostics"]; exists {
		items, err := array(value)
		if err != nil {
			return Action{}, fmt.Errorf("LSP action: expected diagnostics array")
		}
		for _, item := range items {
			if _, err := object(item); err != nil {
				return Action{}, fmt.Errorf("LSP action: expected diagnostic object")
			}
		}
	}
	action.Raw = bytes.Clone(raw)
	if edit, exists := fields["edit"]; exists {
		action.Edit = bytes.Clone(edit)
	}
	switch {
	case fields["command"] != nil:
		if bytes.HasPrefix(fields["command"], []byte(`"`)) {
			action.UnsupportedReason = "Bare Command actions are not supported."
		} else {
			action.UnsupportedReason = "Actions containing commands are not supported."
		}
	case has(fields, "arguments", "commands", "executeCommand"):
		action.UnsupportedReason = "Actions containing commands are not supported."
	case has(fields, "changes", "documentChanges", "changeAnnotations", "textDocument",
		"edits", "newText", "range", "oldUri", "newUri", "uri", "options", "annotationId"):
		action.UnsupportedReason = "Edit operations must be inside a text-only WorkspaceEdit."
	case has(fields, "notebookDocument", "notebookDocumentEdit", "cellTextDocuments", "cells"):
		action.UnsupportedReason = "Notebook actions are not supported."
	case disabled:
		// Also guards a disabled object whose (valid) reason is the empty string:
		// Action has no HasDisabled field and must never mistake it for enabled.
		action.UnsupportedReason = "Disabled actions cannot be applied."
	case len(action.Edit) != 0:
		action.UnsupportedReason = editReason(action.Edit)
	case resolveSupported:
		action.NeedsResolve = true
	default:
		action.UnsupportedReason = "Action has no edit and edit resolution is not supported."
	}
	return action, nil
}

func editReason(raw json.RawMessage) string {
	edit, err := object(raw)
	if err != nil {
		return "Edit must be a text-only WorkspaceEdit object."
	}
	if has(edit, "changeAnnotations") {
		return "Change annotations are not supported."
	}
	if has(edit, "kind", "oldUri", "newUri", "options") {
		return "Resource operations are not supported."
	}
	if has(edit, "notebookDocument", "notebookDocumentEdit", "cellTextDocuments", "cells") {
		return "Notebook edits are not supported."
	}
	var rawEdits json.RawMessage
	if has(edit, "documentChanges") {
		targets, err := array(edit["documentChanges"])
		if len(edit) != 1 || err != nil || len(targets) != 1 {
			return "Edit must contain exactly one versioned text-document target."
		}
		target, err := exactObject(targets[0], "textDocument", "edits")
		if err != nil {
			return "Edit must contain exactly one versioned text-document target."
		}
		doc, err := exactObject(target["textDocument"], "uri", "version")
		if err != nil {
			return "Edit must contain exactly one versioned text-document target."
		}
		var version int32
		uri, uriErr := text(doc["uri"], 4096)
		if uriErr != nil || uri == "" || json.Unmarshal(doc["version"], &version) != nil || version <= 0 {
			return "Edit must contain exactly one versioned text-document target."
		}
		rawEdits = target["edits"]
	} else {
		if len(edit) != 1 || edit["changes"] == nil {
			return "Only one changes or documentChanges member is supported."
		}
		targets, err := object(edit["changes"])
		if err != nil || len(targets) != 1 {
			return "Edit must target exactly one nonempty URI."
		}
		for uri, edits := range targets {
			if uri == "" {
				return "Edit must target exactly one nonempty URI."
			}
			rawEdits = edits
		}
	}
	items, err := array(rawEdits)
	if err != nil || len(items) == 0 {
		return "Edit must contain at least one ordinary text edit."
	}
	for _, item := range items {
		// lspedit validates values/ranges. This boundary also rejects every
		// extra edit member rather than silently ignoring an operation.
		entry, err := exactObject(item, "range", "newText")
		if err != nil {
			return "Only ordinary text edits are supported."
		}
		r, err := exactObject(entry["range"], "start", "end")
		if err != nil {
			return "Only ordinary text edits are supported."
		}
		for _, endpoint := range []string{"start", "end"} {
			if _, err := exactObject(r[endpoint], "line", "character"); err != nil {
				return "Only ordinary text edits are supported."
			}
		}
	}
	if _, err := lspedit.Parse(raw); err != nil {
		return "Unsupported or malformed ordinary text edits."
	}
	return ""
}

func has(fields map[string]json.RawMessage, names ...string) bool {
	for _, name := range names {
		if _, exists := fields[name]; exists {
			return true
		}
	}
	return false
}

func object(raw []byte) (map[string]json.RawMessage, error) {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, fmt.Errorf("LSP action: expected object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}

func exactObject(raw []byte, names ...string) (map[string]json.RawMessage, error) {
	fields, err := object(raw)
	if err != nil {
		return nil, err
	}
	if len(fields) != len(names) {
		return nil, fmt.Errorf("LSP action: unexpected edit member")
	}
	for _, name := range names {
		if _, exists := fields[name]; !exists {
			return nil, fmt.Errorf("LSP action: missing edit member")
		}
	}
	return fields, nil
}

func array(raw []byte) ([]json.RawMessage, error) {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, fmt.Errorf("LSP action: expected array")
	}
	var result []json.RawMessage
	err := json.Unmarshal(raw, &result)
	return result, err
}

func text(raw []byte, limit int) (string, error) {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '"' {
		return "", fmt.Errorf("LSP action: expected string")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	if len(value) > limit {
		return "", fmt.Errorf("LSP action: display string byte bound exceeded")
	}
	return value, nil
}

func shape(raw []byte) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var fields map[string]any
	err := decoder.Decode(&fields)
	return fields, err
}

func validateJSON(raw []byte, depth int) error {
	if !utf8.Valid(raw) {
		return fmt.Errorf("LSP action: invalid UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := jsonValue(decoder, depth); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("LSP action: trailing JSON input")
	}
	return validateSurrogates(raw)
}

func jsonValue(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, container := token.(json.Delim)
	if !container {
		return nil
	}
	if depth >= maxDepth {
		return fmt.Errorf("LSP action: JSON nesting bound exceeded")
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
				return fmt.Errorf("LSP action: duplicate or invalid JSON member")
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
		return fmt.Errorf("LSP action: unexpected JSON delimiter")
	}
	_, err = decoder.Token()
	return err
}

// encoding/json replaces unpaired surrogate escapes; reject those instead of
// conflating opaque strings/keys or altering literal visible text. JSON syntax
// has already been validated, so escape offsets below are known to exist.
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
			return fmt.Errorf("LSP action: unpaired JSON surrogate")
		}
		low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return fmt.Errorf("LSP action: unpaired JSON surrogate")
		}
		i += 6
	}
	return nil
}
