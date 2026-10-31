package lspnav

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxJSONDepth = 64

// Parse consumes original JSON, before generated protocol decoding can erase
// malformed numbers, duplicate members, missing fields or invalid Unicode.
// Definition, type-definition and implementation accept null, one Location,
// homogeneous Location[] or homogeneous LocationLink[]. References accepts
// only null or Location[]. A single LocationLink is not a standard answer.
//
// The contract is deliberately closed: unknown keys (including metadata) are
// rejected at every object level. Required fields cannot be null. Optional
// originSelectionRange, if supplied, must be a range. JSON containers are limited
// to 64 nested levels. Any failure returns a zero Result, never partial targets.
// Target order and duplicate targets are preserved; coordinates are checked
// structurally, not against target text. No URI is resolved or authorized here.
func Parse(kind Kind, raw []byte) (Result, error) {
	switch kind {
	case Definition, TypeDefinition, Implementation, References:
	default:
		return Result{}, fmt.Errorf("LSP navigation: unknown kind %q", kind)
	}
	if len(raw) > MaxRawBytes || !utf8.Valid(raw) {
		return Result{}, fmt.Errorf("LSP navigation: invalid UTF-8 or raw byte bound exceeded")
	}
	if err := validateJSON(raw); err != nil {
		return Result{}, err
	}
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) {
		return Result{Answer: NullAnswer}, nil
	}
	if raw[0] == '{' && kind != References {
		target, err := parseTarget(raw)
		if err != nil {
			return Result{}, err
		}
		if target.Link {
			return Result{}, fmt.Errorf("LSP navigation: single LocationLink is not supported")
		}
		return Result{Answer: TargetsAnswer, Targets: []Target{target}}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if token, err := decoder.Token(); err != nil || token != json.Delim('[') {
		return Result{}, fmt.Errorf("LSP navigation: expected navigation array")
	}
	var targets []Target
	for decoder.More() {
		if len(targets) == MaxTargets {
			return Result{}, fmt.Errorf("LSP navigation: target count bound exceeded")
		}
		var row json.RawMessage
		if err := decoder.Decode(&row); err != nil {
			return Result{}, err
		}
		target, err := parseTarget(row)
		if err != nil {
			return Result{}, err
		}
		if target.Link && kind == References {
			return Result{}, fmt.Errorf("LSP navigation: references cannot contain LocationLink")
		}
		if len(targets) != 0 && target.Link != targets[0].Link {
			return Result{}, fmt.Errorf("LSP navigation: mixed navigation shapes")
		}
		targets = append(targets, target)
	}
	// validateJSON already verified array closure and the absence of trailing data.
	if len(targets) == 0 {
		return Result{Answer: EmptyAnswer}, nil
	}
	return Result{Answer: TargetsAnswer, Targets: targets}, nil
}

func parseTarget(raw []byte) (Target, error) {
	fields, err := object(raw, "uri", "range", "targetUri", "targetRange",
		"targetSelectionRange", "originSelectionRange")
	if err != nil {
		return Target{}, err
	}
	_, location := fields["uri"]
	_, link := fields["targetUri"]
	if location == link {
		return Target{}, fmt.Errorf("LSP navigation: ambiguous or missing target shape")
	}
	var target Target
	if location {
		if len(fields) != 2 {
			return Target{}, fmt.Errorf("LSP navigation: expected only Location fields")
		}
		if target.URI, err = uri(fields["uri"]); err != nil {
			return Target{}, err
		}
		if target.Range, err = parseRange(fields["range"]); err != nil {
			return Target{}, err
		}
		target.Selection = target.Range
		return target, nil
	}
	if _, ok := fields["range"]; ok {
		return Target{}, fmt.Errorf("LSP navigation: Location range in LocationLink")
	}
	target.Link = true
	if target.URI, err = uri(fields["targetUri"]); err != nil {
		return Target{}, err
	}
	if target.Range, err = parseRange(fields["targetRange"]); err != nil {
		return Target{}, err
	}
	if target.Selection, err = parseRange(fields["targetSelectionRange"]); err != nil {
		return Target{}, err
	}
	if before(target.Selection.Start, target.Range.Start) || before(target.Range.End, target.Selection.End) {
		return Target{}, fmt.Errorf("LSP navigation: target selection outside target range")
	}
	if rawOrigin, ok := fields["originSelectionRange"]; ok {
		origin, err := parseRange(rawOrigin)
		if err != nil {
			return Target{}, err
		}
		target.Origin = &origin
	}
	return target, nil
}

func uri(raw []byte) (string, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '"' {
		return "", fmt.Errorf("LSP navigation: expected URI string")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", err
	}
	if value == "" || len(value) > MaxURIBytes || strings.ContainsRune(value, 0) {
		return "", fmt.Errorf("LSP navigation: empty, oversized or NUL-containing URI")
	}
	return value, nil
}

func parseRange(raw []byte) (Range, error) {
	fields, err := object(raw, "start", "end")
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
	if before(end, start) {
		return Range{}, fmt.Errorf("LSP navigation: reversed range")
	}
	return Range{Start: start, End: end}, nil
}

func before(a, b Position) bool {
	return a.Line < b.Line || a.Line == b.Line && a.Character < b.Character
}

func position(raw []byte) (Position, error) {
	fields, err := object(raw, "line", "character")
	if err != nil {
		return Position{}, err
	}
	line, err := coordinate(fields["line"])
	if err != nil {
		return Position{}, err
	}
	character, err := coordinate(fields["character"])
	if err != nil {
		return Position{}, err
	}
	return Position{Line: line, Character: character}, nil
}

func coordinate(raw []byte) (int, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return 0, fmt.Errorf("LSP navigation: missing coordinate")
	}
	for _, c := range raw {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("LSP navigation: expected canonical nonnegative integer")
		}
	}
	// JSON syntax validation has already excluded leading zeroes.
	value, err := strconv.ParseInt(string(raw), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("LSP navigation: coordinate exceeds int32")
	}
	return int(value), nil
}

// Objects are decoded only after whole-input validation, so a duplicate cannot
// overwrite an operative value, including when its member name was escaped.
func object(raw []byte, allowed ...string) (map[string]json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, fmt.Errorf("LSP navigation: expected object")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	for key := range fields {
		found := false
		for _, name := range allowed {
			found = found || key == name
		}
		if !found {
			return nil, fmt.Errorf("LSP navigation: unknown field %q", key)
		}
	}
	return fields, nil
}

func validateJSON(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := jsonValue(decoder, 0); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("LSP navigation: trailing JSON input")
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
	if depth == maxJSONDepth {
		return fmt.Errorf("LSP navigation: JSON nesting bound exceeded")
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
				return fmt.Errorf("LSP navigation: duplicate or invalid JSON member")
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
		return fmt.Errorf("LSP navigation: unexpected JSON delimiter")
	}
	_, err = decoder.Token()
	return err
}

// encoding/json replaces unpaired escaped surrogates; reject rather than
// silently changing the decoded URI or key. JSON syntax is already valid, so
// every escape has its required bytes and escaped quotes are skipped together.
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
			return fmt.Errorf("LSP navigation: unpaired JSON surrogate")
		}
		low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return fmt.Errorf("LSP navigation: unpaired JSON surrogate")
		}
		i += 6
	}
	return nil
}
