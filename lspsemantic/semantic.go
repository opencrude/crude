// Package lspsemantic validates passive semantic-token evidence against one
// immutable UTF-8 source. It does not request, persist, paint or authorize edits.
package lspsemantic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"crude/document"
)

const (
	MaxRawBytes          = 4 << 20
	MaxDataInts          = 250000
	MaxLegendEntries     = 256
	MaxLegendStringBytes = 256
	MaxResultIDBytes     = 4096
	maxDeltaEdits        = 50000
)

var ErrInvalid = errors.New("invalid semantic token evidence")

func invalid(message string) error { return fmt.Errorf("%w: %s", ErrInvalid, message) }

// Legend indexes are supplied by the exact provider instance, not interpreted
// as a language identity. Unknown names stay inert for the renderer's fallback.
type Legend struct{ Types, Modifiers []string }
type Span struct {
	Start, End int
	Type       string
	Modifiers  uint32
}
type Result struct {
	ID   string
	Data []uint32
	Null bool
}

func (l Legend) Validate() error {
	if len(l.Types) > MaxLegendEntries || len(l.Modifiers) > 31 {
		return invalid("legend size")
	}
	for _, list := range [][]string{l.Types, l.Modifiers} {
		seen := make(map[string]bool, len(list))
		for _, s := range list {
			if s == "" || len(s) > MaxLegendStringBytes || !utf8.ValidString(s) || strings.ContainsAny(s, "\x00\r\n") || seen[s] {
				return invalid("legend name")
			}
			seen[s] = true
		}
	}
	return nil
}

// object preserves field presence and rejects duplicate names before Go's JSON
// decoder can replace them. Only standard fields are interpreted; extensions
// cannot turn into executable behavior or overwrite a checked member.
func object(raw []byte) (map[string]json.RawMessage, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || raw[0] != '{' {
		return nil, invalid("expected object")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	tok, err := d.Token()
	if err != nil || tok != json.Delim('{') {
		return nil, invalid("object start")
	}
	result := make(map[string]json.RawMessage)
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, invalid("object key")
		}
		key, ok := token.(string)
		if !ok || len(key) > MaxLegendStringBytes || len(result) >= 64 {
			return nil, invalid("object members")
		}
		if _, exists := result[key]; exists {
			return nil, invalid("duplicate member")
		}
		var value json.RawMessage
		if err := d.Decode(&value); err != nil {
			return nil, invalid("object value")
		}
		result[key] = value
	}
	if tok, err = d.Token(); err != nil || tok != json.Delim('}') {
		return nil, invalid("object end")
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, invalid("trailing JSON")
	}
	return result, nil
}

// validRaw additionally rejects unpaired Unicode escapes: encoding/json would
// otherwise repair an opaque result ID into a different provider identity.
func validRaw(raw []byte) error {
	if len(raw) == 0 || len(raw) > MaxRawBytes || !utf8.Valid(raw) || !json.Valid(raw) {
		return invalid("JSON encoding or byte bound")
	}
	inString := false
	for i := 0; i < len(raw); i++ {
		if raw[i] == '"' {
			inString = !inString
			continue
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
			return invalid("unpaired Unicode escape")
		}
		low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return invalid("unpaired Unicode escape")
		}
		i += 6
	}
	return nil
}
func resultID(fields map[string]json.RawMessage) (string, error) {
	raw, ok := fields["resultId"]
	if !ok {
		return "", nil
	}
	if len(raw) == 0 || bytes.TrimSpace(raw)[0] != '"' {
		return "", invalid("result ID")
	}
	var id string
	if json.Unmarshal(raw, &id) != nil || len(id) > MaxResultIDBytes || strings.ContainsAny(id, "\x00\r\n") {
		return "", invalid("result ID")
	}
	return id, nil
}
func dataArray(raw json.RawMessage) ([]uint32, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) < 2 || raw[0] != '[' || raw[len(raw)-1] != ']' {
		return nil, invalid("data array")
	}
	values := make([]uint32, 0, min(len(raw)/2, MaxDataInts))
	whitespace := func(b byte) bool { return b == ' ' || b == '\t' || b == '\r' || b == '\n' }
	i := 1
	for i < len(raw)-1 && whitespace(raw[i]) {
		i++
	}
	if i == len(raw)-1 {
		return values, nil
	}
	for {
		if i >= len(raw)-1 || raw[i] < '0' || raw[i] > '9' || len(values) >= MaxDataInts {
			return nil, invalid("data values or count")
		}
		start := i
		var n uint32
		for i < len(raw)-1 && raw[i] >= '0' && raw[i] <= '9' {
			digit := uint32(raw[i] - '0')
			if n > (math.MaxInt32-digit)/10 {
				return nil, invalid("data integer")
			}
			n = n*10 + digit
			i++
		}
		if i-start > 1 && raw[start] == '0' {
			return nil, invalid("data integer")
		}
		values = append(values, n)
		for i < len(raw)-1 && whitespace(raw[i]) {
			i++
		}
		if i == len(raw)-1 {
			return values, nil
		}
		if raw[i] != ',' {
			return nil, invalid("data delimiter")
		}
		i++
		for i < len(raw)-1 && whitespace(raw[i]) {
			i++
		}
	}
}

// ParseFull accepts full or range results: a present empty array and protocol
// null are different observations. Data is detached and bounded.
func ParseFull(raw []byte) (Result, error) {
	if err := validRaw(raw); err != nil {
		return Result{}, err
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return Result{Null: true}, nil
	}
	fields, err := object(raw)
	if err != nil {
		return Result{}, err
	}
	if _, exists := fields["edits"]; exists {
		return Result{}, invalid("full result contains delta")
	}
	id, err := resultID(fields)
	if err != nil {
		return Result{}, err
	}
	data, err := dataArray(fields["data"])
	if err != nil || len(data)%5 != 0 {
		return Result{}, invalid("full data tuple count")
	}
	return Result{ID: id, Data: data}, nil
}

// ParseDelta applies nonoverlapping edits against the immutable old integer
// array. Providers may send edits in any order or return a replacement full result.
func ParseDelta(raw []byte, base []uint32) (Result, error) {
	if err := validRaw(raw); err != nil {
		return Result{}, err
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return Result{Null: true}, nil
	}
	fields, err := object(raw)
	if err != nil {
		return Result{}, err
	}
	_, hasData := fields["data"]
	editRaw, hasEdits := fields["edits"]
	if hasData {
		if hasEdits {
			return Result{}, invalid("ambiguous delta")
		}
		return ParseFull(raw)
	}
	if !hasEdits || len(bytes.TrimSpace(editRaw)) == 0 || bytes.TrimSpace(editRaw)[0] != '[' {
		return Result{}, invalid("delta edits")
	}
	id, err := resultID(fields)
	if err != nil {
		return Result{}, err
	}
	var edits []json.RawMessage
	if json.Unmarshal(editRaw, &edits) != nil || len(edits) > maxDeltaEdits || len(base) > MaxDataInts {
		return Result{}, invalid("delta edit count")
	}
	type edit struct {
		start, end int
		data       []uint32
	}
	parsed := make([]edit, 0, len(edits))
	size := len(base)
	for _, rawEdit := range edits {
		fields, err := object(rawEdit)
		if err != nil {
			return Result{}, err
		}
		number := func(name string) (int, error) {
			raw := bytes.TrimSpace(fields[name])
			if len(raw) == 0 || raw[0] < '0' || raw[0] > '9' {
				return 0, invalid("delta integer")
			}
			var n uint32
			if json.Unmarshal(raw, &n) != nil || n > math.MaxInt32 {
				return 0, invalid("delta integer")
			}
			return int(n), nil
		}
		start, err := number("start")
		if err != nil {
			return Result{}, err
		}
		count, err := number("deleteCount")
		if err != nil {
			return Result{}, err
		}
		if start > len(base) || count > len(base)-start {
			return Result{}, invalid("out-of-range delta")
		}
		var insert []uint32
		if raw, ok := fields["data"]; ok {
			insert, err = dataArray(raw)
			if err != nil {
				return Result{}, err
			}
		}
		size += len(insert) - count
		parsed = append(parsed, edit{start, start + count, insert})
	}
	slices.SortStableFunc(parsed, func(a, b edit) int { return a.start - b.start })
	for i := 1; i < len(parsed); i++ {
		prev, cur := parsed[i-1], parsed[i]
		if cur.start < prev.end {
			return Result{}, invalid("overlapping delta")
		}
	}
	if size < 0 || size > MaxDataInts {
		return Result{}, invalid("delta result bound")
	}
	if size%5 != 0 {
		return Result{}, invalid("delta data tuple count")
	}
	result := make([]uint32, 0, size)
	cursor := 0
	for _, e := range parsed {
		result = append(result, base[cursor:e.start]...)
		result = append(result, e.data...)
		cursor = e.end
	}
	result = append(result, base[cursor:]...)
	return Result{ID: id, Data: result}, nil
}

// Decode converts relative UTF-16 tuples in one forward pass; repeated tokens on
// dense or long lines never rescan earlier source. Tokens must be ordered,
// nonoverlapping, single-line and exactly on rune boundaries.
func Decode(text string, legend Legend, data []uint32) ([]Span, error) {
	return DecodeContext(context.Background(), text, legend, data)
}

// DecodeContext permits the request owner to retire CPU work after a source or
// workspace change; a cancelled decode publishes no partial spans.
func DecodeContext(ctx context.Context, text string, legend Legend, data []uint32) ([]Span, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := legend.Validate(); err != nil {
		return nil, err
	}
	if len(text) > document.MaxBytes || !utf8.ValidString(text) || len(data) > MaxDataInts || len(data)%5 != 0 {
		return nil, invalid("source or tuple bound")
	}
	out := make([]Span, 0, len(data)/5)
	line, col, offset := uint64(0), uint64(0), 0
	tokenLine, tokenCol := uint64(0), uint64(0)
	advanceLine := func() error {
		next := strings.IndexByte(text[offset:], '\n')
		if next < 0 {
			return invalid("line outside source")
		}
		offset += next + 1
		line++
		col = 0
		return nil
	}
	advanceCol := func(target uint64) error {
		for col < target {
			if offset >= len(text) || text[offset] == '\n' || text[offset] == '\r' && offset+1 < len(text) && text[offset+1] == '\n' {
				return invalid("column outside source")
			}
			r, n := utf8.DecodeRuneInString(text[offset:])
			units := uint64(utf16.RuneLen(r))
			if col+units > target {
				return invalid("column splits UTF-16 rune")
			}
			col += units
			offset += n
		}
		if col != target {
			return invalid("overlapping token")
		}
		return nil
	}
	for i := 0; i < len(data); i += 5 {
		if i%4095 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		dl, dc, length, kind, mods := data[i], data[i+1], data[i+2], data[i+3], data[i+4]
		for _, n := range data[i : i+5] {
			if n > math.MaxInt32 {
				return nil, invalid("integer exceeds LSP uinteger")
			}
		}
		if length == 0 || int(kind) >= len(legend.Types) || (len(legend.Modifiers) < 31 && mods>>len(legend.Modifiers) != 0) {
			return nil, invalid("token length, type or modifiers")
		}
		if dl > 0 {
			tokenLine += uint64(dl)
			tokenCol = uint64(dc)
		} else {
			tokenCol += uint64(dc)
		}
		if tokenLine > math.MaxInt32 || tokenCol+uint64(length) > math.MaxInt32 || tokenLine < line {
			return nil, invalid("token coordinates")
		}
		for line < tokenLine {
			if err := advanceLine(); err != nil {
				return nil, err
			}
		}
		if err := advanceCol(tokenCol); err != nil {
			return nil, err
		}
		start := offset
		// Single-line capability requires an overlong token to stop at this
		// line's end, not to spill onto the next line or reject useful ink.
		target := tokenCol + uint64(length)
		for col < target && offset < len(text) && text[offset] != '\n' && (text[offset] != '\r' || offset+1 >= len(text) || text[offset+1] != '\n') {
			r, n := utf8.DecodeRuneInString(text[offset:])
			units := uint64(utf16.RuneLen(r))
			if col+units > target {
				return nil, invalid("column splits UTF-16 rune")
			}
			col += units
			offset += n
		}
		if offset == start {
			return nil, invalid("empty clipped token")
		}
		out = append(out, Span{Start: start, End: offset, Type: legend.Types[kind], Modifiers: mods})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
