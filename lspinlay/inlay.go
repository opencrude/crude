// Package lspinlay passively normalizes bounded original-JSON LSP inlay hints.
// Raw evidence is not authority: this package never resolves URIs, maps source
// positions, executes commands, applies edits, or negotiates provider support.
package lspinlay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

const (
	MaxRawBytes     = 1 << 20
	MaxHints        = 1024
	MaxParts        = 32
	MaxLabelBytes   = 4096
	MaxDisplayBytes = 64 << 10
	MaxDepth        = 64

	// Metadata limits apply even to opaque data and unknown extension members.
	// Every array/object has at most MaxMetadataItems entries, and every decoded
	// string (including a member name) at most MaxMetadataStringBytes UTF-8 bytes.
	// Raw bytes bound their aggregate size. Known label limits are stricter.
	MaxMetadataItems       = 1024
	MaxMetadataStringBytes = 64 << 10

	HintsAnswer = "hints"
	NullAnswer  = "null"
	EmptyAnswer = "empty"
)

type Position struct{ Line, Character int }
type Range struct{ Start, End Position }
type Location struct {
	URI   string
	Range Range
}

// Markup.Form is "" when absent, "string" for a plain string, and "markup"
// for MarkupContent. Kind is populated only for MarkupContent.
type Markup struct {
	Present           bool
	Form, Kind, Value string
}

type LabelPart struct {
	Value      string
	Tooltip    Markup
	Location   *Location
	HasCommand bool
}

// Hint retains detached, exact item JSON, including all inert metadata and
// optional-member presence. Label is a bytewise concatenation of part values;
// grapheme segmentation and all display/source mapping belong to consumers.
type Hint struct {
	Raw                       json.RawMessage
	Position                  Position
	Label                     string
	Parts                     []LabelPart
	LabelIsParts              bool
	Kind                      int
	HasKind                   bool
	PaddingLeft, PaddingRight bool
	Tooltip                   Markup
	HasTextEdits, HasData     bool
}

type Result struct {
	Answer string
	Hints  []Hint
}

// Parse accepts null or an array and preserves response order, including hints
// at equal positions. A malformed or over-budget item rejects the whole list.
// Known optional nulls are invalid except data:null. Unknown members stay inert.
// String labels, parts arrays, and individual part values must be nonempty.
func Parse(raw []byte) (Result, error) {
	if err := validateJSON(raw, 0); err != nil {
		return Result{}, err
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return Result{Answer: NullAnswer}, nil
	}
	items, err := array(raw)
	if err != nil {
		return Result{}, err
	}
	if len(items) == 0 {
		return Result{Answer: EmptyAnswer}, nil
	}
	if len(items) > MaxHints {
		return Result{}, invalid("hint count bound exceeded")
	}
	hints := make([]Hint, 0, len(items))
	display := 0
	for i, item := range items {
		hint, err := parseHint(item)
		if err != nil {
			return Result{}, fmt.Errorf("LSP inlay hint %d: %w", i, err)
		}
		display += len(hint.Label)
		if display > MaxDisplayBytes {
			return Result{}, invalid("aggregate label byte bound exceeded")
		}
		hints = append(hints, hint)
	}
	return Result{Answer: HintsAnswer, Hints: hints}, nil
}

// Resolve validates two original-JSON items, sending nothing. Only top-level
// tooltip and label[i].tooltip may be added or changed; a present tooltip cannot
// be removed. All other members must remain the same JSON value, including number
// spelling. Key order, whitespace and equivalent string escapes are immaterial.
// Both inputs reserve one container level for the list, as Parse does.
func Resolve(originalRaw, resolvedRaw []byte) (Hint, error) {
	var result Hint
	shapes := make([]map[string]any, 0, 2)
	for _, raw := range [][]byte{originalRaw, resolvedRaw} {
		if err := validateJSON(raw, 1); err != nil {
			return Hint{}, err
		}
		hint, err := parseHint(raw)
		if err != nil {
			return Hint{}, err
		}
		result = hint
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var fields map[string]any
		if err := decoder.Decode(&fields); err != nil {
			return Hint{}, err
		}
		shapes = append(shapes, fields)
	}
	if err := stripTooltips(shapes[0], shapes[1]); err != nil {
		return Hint{}, err
	}
	// Strip only exact label-part tooltip slots, never similarly named metadata.
	before, partsBefore := shapes[0]["label"].([]any)
	after, partsAfter := shapes[1]["label"].([]any)
	if partsBefore && partsAfter && len(before) == len(after) {
		for i := range before {
			if err := stripTooltips(before[i].(map[string]any), after[i].(map[string]any)); err != nil {
				return Hint{}, err
			}
		}
	}
	if !reflect.DeepEqual(shapes[0], shapes[1]) {
		return Hint{}, invalid("resolve changed a non-tooltip member")
	}
	return result, nil
}

func stripTooltips(before, after map[string]any) error {
	if _, had := before["tooltip"]; had {
		if _, has := after["tooltip"]; !has {
			return invalid("resolve removed a tooltip")
		}
	}
	delete(before, "tooltip")
	delete(after, "tooltip")
	return nil
}

func parseHint(raw []byte) (Hint, error) {
	fields, err := object(raw)
	if err != nil {
		return Hint{}, err
	}
	var h Hint
	if h.Position, err = position(fields["position"]); err != nil {
		return Hint{}, err
	}
	label := bytes.TrimSpace(fields["label"])
	if len(label) != 0 && label[0] == '"' {
		h.Label, err = text(label, MaxLabelBytes)
	} else {
		var parts []json.RawMessage
		parts, err = array(label)
		if err == nil {
			if len(parts) == 0 || len(parts) > MaxParts {
				return Hint{}, invalid("label part count bound exceeded")
			}
			h.LabelIsParts = true
			var combined strings.Builder
			for _, rawPart := range parts {
				part, partErr := parsePart(rawPart)
				if partErr != nil {
					return Hint{}, partErr
				}
				if combined.Len()+len(part.Value) > MaxLabelBytes {
					return Hint{}, invalid("label byte bound exceeded")
				}
				combined.WriteString(part.Value)
				h.Parts = append(h.Parts, part)
			}
			h.Label = combined.String()
		}
	}
	if err != nil {
		return Hint{}, err
	}
	if h.Label == "" {
		return Hint{}, invalid("empty label")
	}
	if value, ok := fields["kind"]; ok {
		h.HasKind = true
		if h.Kind, err = coordinate(value); err != nil || (h.Kind != 1 && h.Kind != 2) {
			return Hint{}, invalid("kind must be 1 or 2")
		}
	}
	if h.PaddingLeft, err = optionalBool(fields, "paddingLeft"); err != nil {
		return Hint{}, err
	}
	if h.PaddingRight, err = optionalBool(fields, "paddingRight"); err != nil {
		return Hint{}, err
	}
	if h.Tooltip, err = tooltip(fields); err != nil {
		return Hint{}, err
	}
	if value, ok := fields["textEdits"]; ok {
		h.HasTextEdits = true
		edits, err := array(value)
		if err != nil {
			return Hint{}, err
		}
		for _, rawEdit := range edits {
			edit, err := object(rawEdit)
			if err != nil {
				return Hint{}, err
			}
			if _, err := parseRange(edit["range"]); err != nil {
				return Hint{}, err
			}
			if _, err := text(edit["newText"], MaxMetadataStringBytes); err != nil {
				return Hint{}, err
			}
		}
	}
	_, h.HasData = fields["data"]
	h.Raw = bytes.Clone(raw)
	return h, nil
}

func parsePart(raw []byte) (LabelPart, error) {
	fields, err := object(raw)
	if err != nil {
		return LabelPart{}, err
	}
	var p LabelPart
	if p.Value, err = text(fields["value"], MaxLabelBytes); err != nil {
		return LabelPart{}, err
	}
	if p.Value == "" {
		return LabelPart{}, invalid("empty label part value")
	}
	if p.Tooltip, err = tooltip(fields); err != nil {
		return LabelPart{}, err
	}
	if value, ok := fields["location"]; ok {
		location, err := object(value)
		if err != nil {
			return LabelPart{}, err
		}
		uri, err := text(location["uri"], MaxMetadataStringBytes)
		if err != nil {
			return LabelPart{}, err
		}
		r, err := parseRange(location["range"])
		if err != nil {
			return LabelPart{}, err
		}
		p.Location = &Location{URI: uri, Range: r}
	}
	if value, ok := fields["command"]; ok {
		command, err := object(value)
		if err != nil {
			return LabelPart{}, err
		}
		for _, key := range []string{"title", "command"} {
			if _, err := text(command[key], MaxMetadataStringBytes); err != nil {
				return LabelPart{}, err
			}
		}
		if args, ok := command["arguments"]; ok {
			if _, err := array(args); err != nil {
				return LabelPart{}, err
			}
		}
		p.HasCommand = true
	}
	return p, nil
}

func tooltip(fields map[string]json.RawMessage) (Markup, error) {
	raw, ok := fields["tooltip"]
	if !ok {
		return Markup{}, nil
	}
	if bytes.HasPrefix(bytes.TrimSpace(raw), []byte(`"`)) {
		value, err := text(raw, MaxMetadataStringBytes)
		return Markup{Present: true, Form: "string", Value: value}, err
	}
	m, err := object(raw)
	if err != nil {
		return Markup{}, err
	}
	kind, err := text(m["kind"], MaxMetadataStringBytes)
	if err != nil || (kind != "plaintext" && kind != "markdown") {
		return Markup{}, invalid("invalid markup kind")
	}
	value, err := text(m["value"], MaxMetadataStringBytes)
	return Markup{Present: true, Form: "markup", Kind: kind, Value: value}, err
}

func optionalBool(fields map[string]json.RawMessage, name string) (bool, error) {
	value, ok := fields[name]
	if !ok {
		return false, nil
	}
	switch string(bytes.TrimSpace(value)) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, invalid("expected boolean " + name)
	}
}

func position(raw []byte) (Position, error) {
	fields, err := object(raw)
	if err != nil {
		return Position{}, err
	}
	line, err := coordinate(fields["line"])
	if err != nil {
		return Position{}, err
	}
	character, err := coordinate(fields["character"])
	return Position{Line: line, Character: character}, err
}

func coordinate(raw []byte) (int, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return 0, invalid("missing coordinate")
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, invalid("expected canonical nonnegative int32")
		}
	}
	value, err := strconv.ParseInt(string(raw), 10, 32)
	if err != nil {
		return 0, invalid("coordinate exceeds int32")
	}
	return int(value), nil
}

func parseRange(raw []byte) (Range, error) {
	fields, err := object(raw)
	if err != nil {
		return Range{}, err
	}
	start, err := position(fields["start"])
	if err != nil {
		return Range{}, err
	}
	end, err := position(fields["end"])
	if err != nil {
		return Range{}, err
	}
	if end.Line < start.Line || (end.Line == start.Line && end.Character < start.Character) {
		return Range{}, invalid("reversed range")
	}
	return Range{Start: start, End: end}, nil
}

// These helpers run only after whole-input validation, including duplicates.
func object(raw []byte) (map[string]json.RawMessage, error) {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, invalid("expected object")
	}
	var fields map[string]json.RawMessage
	err := json.Unmarshal(raw, &fields)
	return fields, err
}

func array(raw []byte) ([]json.RawMessage, error) {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '[' {
		return nil, invalid("expected array")
	}
	var items []json.RawMessage
	err := json.Unmarshal(raw, &items)
	return items, err
}

func text(raw []byte, limit int) (string, error) {
	if trimmed := bytes.TrimSpace(raw); len(trimmed) == 0 || trimmed[0] != '"' {
		return "", invalid("expected string")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	if len(value) > limit {
		return "", invalid("string byte bound exceeded")
	}
	return value, nil
}

func invalid(message string) error { return fmt.Errorf("LSP inlay: %s", message) }
