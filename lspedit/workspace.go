package lspedit

import (
	"bytes"
	"fmt"
	"sort"
	"unicode/utf8"

	"crude/types"
)

// OperationKind identifies one ordered WorkspaceEdit operation. Only ordinary
// text edits and creation of a new file are admitted; the file owner still
// validates workspace scope, absence and source identity.
type OperationKind string

const (
	OperationText   OperationKind = "text"
	OperationCreate OperationKind = "create"
)

// EditOperation is one text target or a create-file operation. Edits are
// ordered source UTF-16 operations; Versioned distinguishes documentChanges
// from the changes map.
type EditOperation struct {
	Kind      OperationKind
	URI       string
	Versioned bool
	Version   *int32
	Edits     []types.LSPTextEdit
}

// WorkspaceEdit is a bounded, detached WorkspaceEdit. It never carries file
// authority; the canonical document service resolves URIs and preimages.
type WorkspaceEdit struct {
	Operations []EditOperation
}

// ParseWorkspace parses a bounded WorkspaceEdit with ordinary text edits and
// create-file operations. Other resource operations are rejected as a whole.
func ParseWorkspace(raw []byte) (WorkspaceEdit, error) {
	if len(raw) > MaxRawBytes || !utf8.Valid(raw) {
		return WorkspaceEdit{}, fmt.Errorf("LSP edit: invalid encoding or raw JSON byte bound exceeded")
	}
	if err := validateJSON(raw); err != nil {
		return WorkspaceEdit{}, err
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return WorkspaceEdit{}, nil
	}
	root, err := object(raw, "changes", "documentChanges")
	if err != nil {
		return WorkspaceEdit{}, err
	}
	if _, changes := root["changes"]; changes {
		if _, documents := root["documentChanges"]; documents {
			return WorkspaceEdit{}, fmt.Errorf("LSP edit: changes and documentChanges cannot be combined")
		}
	}
	budget := parseBudget{}
	var operations []EditOperation
	if value, ok := root["changes"]; ok {
		targets, err := decodeObject(value, MaxDocuments)
		if err != nil {
			return WorkspaceEdit{}, err
		}
		uris := make([]string, 0, len(targets))
		for uri := range targets {
			uris = append(uris, uri)
		}
		sort.Strings(uris)
		for _, uri := range uris {
			if err := budget.target(uri); err != nil {
				return WorkspaceEdit{}, err
			}
			edits, err := budget.edits(targets[uri])
			if err != nil {
				return WorkspaceEdit{}, err
			}
			if len(edits) == 0 {
				return WorkspaceEdit{}, fmt.Errorf("LSP edit: edit target has no ordinary text edits")
			}
			operations = append(operations, EditOperation{Kind: OperationText, URI: uri, Edits: edits})
		}
	}
	if value, ok := root["documentChanges"]; ok {
		items, err := array(value, MaxDocuments)
		if err != nil {
			return WorkspaceEdit{}, err
		}
		textTargets := make(map[string]bool)
		for _, item := range items {
			entry, err := object(item, "kind", "uri", "textDocument", "edits")
			if err != nil {
				return WorkspaceEdit{}, err
			}
			if kindRaw, present := entry["kind"]; present {
				kind, err := stringValue(kindRaw)
				if err != nil || kind != "create" {
					return WorkspaceEdit{}, fmt.Errorf("LSP edit: only create-file resource operations are supported")
				}
				uri, err := stringValue(entry["uri"])
				if err != nil {
					return WorkspaceEdit{}, err
				}
				if err := budget.target(uri); err != nil {
					return WorkspaceEdit{}, err
				}
				operations = append(operations, EditOperation{Kind: OperationCreate, URI: uri})
				continue
			}
			target, err := exactObject(entry["textDocument"], "uri", "version")
			if err != nil {
				return WorkspaceEdit{}, err
			}
			uri, err := stringValue(target["uri"])
			if err != nil {
				return WorkspaceEdit{}, err
			}
			if textTargets[uri] {
				return WorkspaceEdit{}, fmt.Errorf("LSP edit: duplicate document target")
			}
			textTargets[uri] = true
			if err := budget.target(uri); err != nil {
				return WorkspaceEdit{}, err
			}
			versionRaw, present := target["version"]
			if !present {
				return WorkspaceEdit{}, fmt.Errorf("LSP edit: missing document version")
			}
			var version *int32
			if !bytes.Equal(bytes.TrimSpace(versionRaw), []byte("null")) {
				number, err := integer(versionRaw, true)
				if err != nil {
					return WorkspaceEdit{}, err
				}
				v := int32(number)
				version = &v
			}
			edits, err := budget.edits(entry["edits"])
			if err != nil {
				return WorkspaceEdit{}, err
			}
			if len(edits) == 0 {
				return WorkspaceEdit{}, fmt.Errorf("LSP edit: edit target has no ordinary text edits")
			}
			operations = append(operations, EditOperation{Kind: OperationText, URI: uri, Versioned: true, Version: version, Edits: edits})
		}
	}
	return WorkspaceEdit{Operations: operations}, nil
}
