// Package lspcompletion retains original completion identity and validates the
// documentation-only resolve subset. It never authorizes or applies an edit.
package lspcompletion

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"unicode/utf8"
)

const (
	MaxRawBytes    = 1 << 20
	MaxItems       = 256
	MaxStringBytes = 64 << 10
	MaxDepth       = 32
	maxMembers     = 4096
)

var ErrInvalid = errors.New("invalid completion evidence")

type Documentation struct {
	Detail, Form, Kind, Value   string
	HasDetail, HasDocumentation bool
}

// Item has no insertion interpretation. Consumers validate a supported insertion
// separately and retain this original object, never an expanded neutral item, as
// the identity of the corresponding row's documentation resolve.
type Item struct {
	Label                string
	FilterText, SortText string
	Documentation        Documentation
	Original             json.RawMessage
}

type Result struct {
	Items      []Item
	Null       bool
	Incomplete bool
}

func invalid(why string) error { return fmt.Errorf("%w: %s", ErrInvalid, why) }

// Parse accepts a standard array, null, or CompletionList. Defaults are not
// advertised by the documentation-only consumer and cannot be silently ignored.
// Unknown item fields, including opaque data, remain in the detached original.
func Parse(raw []byte) (Result, error) {
	if err := validate(raw); err != nil {
		return Result{}, err
	}
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) {
		return Result{Null: true}, nil
	}
	itemsJSON := raw
	out := Result{}
	if len(raw) != 0 && raw[0] == '{' {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil {
			return Result{}, invalid("completion list object")
		}
		if _, supplied := fields["itemDefaults"]; supplied {
			return Result{}, invalid("completion list defaults are not advertised")
		}
		incomplete, supplied := fields["isIncomplete"]
		if !supplied || !isBool(incomplete) || json.Unmarshal(incomplete, &out.Incomplete) != nil {
			return Result{}, invalid("completion list requires Boolean isIncomplete")
		}
		itemsJSON = fields["items"]
	}
	itemsJSON = bytes.TrimSpace(itemsJSON)
	if len(itemsJSON) == 0 || itemsJSON[0] != '[' {
		return Result{}, invalid("completion result requires an item array")
	}
	var originals []json.RawMessage
	if err := json.Unmarshal(itemsJSON, &originals); err != nil || len(originals) > MaxItems {
		return Result{}, invalid("completion item count or container")
	}
	out.Items = make([]Item, 0, len(originals))
	for _, rawItem := range originals {
		item, err := readItem(rawItem)
		if err != nil {
			return Result{}, err
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

func isBool(raw []byte) bool {
	raw = bytes.TrimSpace(raw)
	return bytes.Equal(raw, []byte("true")) || bytes.Equal(raw, []byte("false"))
}

func readItem(raw []byte) (Item, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return Item{}, invalid("completion item requires an object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Item{}, invalid("completion item object")
	}
	var label string
	if !readString(fields["label"], &label) || label == "" {
		return Item{}, invalid("completion item requires a nonempty string label")
	}
	doc, err := readDocumentation(fields)
	if err != nil {
		return Item{}, err
	}
	filter, order := label, label
	for name, value := range map[string]*string{"filterText": &filter, "sortText": &order} {
		if raw, supplied := fields[name]; supplied && !readString(raw, value) {
			return Item{}, invalid(name + " requires a string")
		}
	}
	return Item{Label: label, FilterText: filter, SortText: order, Documentation: doc, Original: bytes.Clone(raw)}, nil
}

func readString(raw []byte, out *string) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) != 0 && raw[0] == '"' && json.Unmarshal(raw, out) == nil && len(*out) <= MaxStringBytes
}

func readDocumentation(fields map[string]json.RawMessage) (Documentation, error) {
	var out Documentation
	if raw, exists := fields["detail"]; exists {
		if !readString(raw, &out.Detail) {
			return Documentation{}, invalid("completion detail requires a string")
		}
		out.HasDetail = true
	}
	raw, exists := fields["documentation"]
	if !exists {
		return out, nil
	}
	out.HasDocumentation = true
	if readString(raw, &out.Value) {
		out.Form, out.Kind = "string", "plaintext"
		return out, nil
	}
	var markup map[string]json.RawMessage
	if json.Unmarshal(raw, &markup) != nil || len(markup) != 2 {
		return Documentation{}, invalid("completion documentation requires string or MarkupContent")
	}
	if !readString(markup["kind"], &out.Kind) || (out.Kind != "plaintext" && out.Kind != "markdown") ||
		!readString(markup["value"], &out.Value) {
		return Documentation{}, invalid("completion documentation kind or value")
	}
	out.Form = "markup"
	return out, nil
}

// ResolveDocumentation allows only detail/documentation changes. Every other
// original field must remain present and structurally identical, and no new
// adjunct may appear. Comparison keeps exact JSON numeric spellings but ignores
// insignificant object order/whitespace. Insertion is never reconstructed here.
func ResolveDocumentation(original, resolved []byte) (Documentation, error) {
	for _, raw := range [][]byte{original, resolved} {
		if err := validate(raw); err != nil {
			return Documentation{}, err
		}
		if _, err := readItem(raw); err != nil {
			return Documentation{}, err
		}
	}
	var before, after map[string]json.RawMessage
	if err := json.Unmarshal(original, &before); err != nil {
		return Documentation{}, invalid("original completion object")
	}
	if err := json.Unmarshal(resolved, &after); err != nil {
		return Documentation{}, invalid("resolved completion object")
	}
	for key, raw := range before {
		if key == "detail" || key == "documentation" {
			continue
		}
		other, exists := after[key]
		if !exists || !sameValue(raw, other) {
			return Documentation{}, invalid("resolve changed original completion field " + strconv.Quote(key))
		}
	}
	for key := range after {
		if _, exists := before[key]; !exists && key != "detail" && key != "documentation" {
			return Documentation{}, invalid("resolve added unadvertised completion field " + strconv.Quote(key))
		}
	}
	return readDocumentation(after)
}

func sameValue(a, b []byte) bool {
	decode := func(raw []byte) (any, error) {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value any
		err := decoder.Decode(&value)
		return value, err
	}
	av, ae := decode(a)
	bv, be := decode(b)
	return ae == nil && be == nil && reflect.DeepEqual(av, bv)
}

func validate(raw []byte) error {
	if len(raw) == 0 || len(raw) > MaxRawBytes || !utf8.Valid(raw) {
		return invalid("raw byte bound or UTF-8")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := value(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return invalid("trailing JSON")
	}
	// encoding/json replaces unpaired surrogate escapes. Preserve valid pairs
	// exactly and refuse loss even in unknown keys or opaque provider data.
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
			return invalid("unpaired JSON surrogate")
		}
		low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return invalid("unpaired JSON surrogate")
		}
		i += 6
	}
	return nil
}

func value(decoder *json.Decoder, depth int) error {
	token, err := decoder.Token()
	if err != nil {
		return invalid("JSON syntax")
	}
	if text, ok := token.(string); ok && len(text) > MaxStringBytes {
		return invalid("metadata string bound")
	}
	delim, container := token.(json.Delim)
	if !container {
		return nil
	}
	if depth >= MaxDepth {
		return invalid("JSON nesting bound")
	}
	count := 0
	switch delim {
	case '{':
		seen := make(map[string]struct{})
		for decoder.More() {
			key, err := decoder.Token()
			name, ok := key.(string)
			if err != nil || !ok || len(name) > MaxStringBytes {
				return invalid("object member")
			}
			if _, exists := seen[name]; exists {
				return invalid("duplicate object member")
			}
			seen[name] = struct{}{}
			count++
			if count > maxMembers {
				return invalid("object member count")
			}
			if err := value(decoder, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			count++
			if count > maxMembers {
				return invalid("array item count")
			}
			if err := value(decoder, depth+1); err != nil {
				return err
			}
		}
	default:
		return invalid("unexpected JSON delimiter")
	}
	if _, err := decoder.Token(); err != nil {
		return invalid("JSON closing delimiter")
	}
	return nil
}
