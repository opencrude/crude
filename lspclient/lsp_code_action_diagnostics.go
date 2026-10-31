package lspclient

import (
	"bytes"
	"encoding/json"
	"io"
	"path/filepath"
	"strconv"
	"unicode/utf8"

	"crude/types"

	"github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// This adjunct is not another visible diagnostic store. It is immutable original
// protocol input for one manual query and is replaced with every publication.
// Its version/sequence must match the accepted transient publication. No UI DTO
// can be used to rebuild it after omission or invalidation.
type codeActionDiagnosticAdjunct struct {
	version, count int
	sequence       uint64
	diagnostics    []originalActionDiagnostic
	detail         string
}

type originalActionDiagnostic struct {
	raw        json.RawMessage
	start, end int // strict source byte offsets, not display coordinates
}

func (a codeActionDiagnosticAdjunct) selectRange(version int, sequence uint64, start, end int) ([]json.RawMessage, string) {
	if a.detail != "" {
		return nil, a.detail
	}
	if a.version != version || a.sequence == 0 || a.sequence != sequence {
		return nil, "diagnostics omitted: not yet published for this source"
	}
	if a.count == 0 {
		return nil, "versioned publication contains no diagnostics"
	}
	var result []json.RawMessage
	for _, d := range a.diagnostics {
		match := start < d.end && d.start < end
		if start == end {
			match = (d.start <= start && start < d.end) || (d.start == start && d.end == start)
		} else if d.start == d.end {
			match = start <= d.start && d.start < end
		}
		if match {
			result = append(result, bytes.Clone(d.raw))
		}
	}
	if len(result) == 0 {
		return nil, "versioned publication has no matching diagnostics"
	}
	return result, "original diagnostics from exact versioned publication"
}

// Keep the existing tolerant visible publication/callback contract. The adjunct
// is stricter: malformed or ambiguous metadata must never refresh old proof.
func (l *LSPIntegration) receiveDiagnostics(launch *lspLaunch, params json.RawMessage) {
	var diagnostics struct {
		protocol.PublishDiagnosticsParams
		Version *int32 `json:"version"`
	}
	err := json.Unmarshal(params, &diagnostics)
	// A malformed or ambiguous URI cannot safely identify a single document.
	// Disqualify all adjuncts of this exact live producer, not visible facts.
	if err != nil || len(params) > lsp.MaxCodeActionDiagnosticBytes+(16<<10) || !validOriginalDiagnosticJSON(params) {
		l.mu.Lock()
		if launch != nil && l.launch == launch && l.client != nil {
			for _, state := range l.openFiles {
				state.actionDiagnostics = codeActionDiagnosticAdjunct{detail: "diagnostics omitted: malformed or ambiguous publication"}
			}
		}
		l.mu.Unlock()
	}
	if err == nil {
		lspDiagnostics.Inc()
		l.updateDiagnostics(launch, diagnostics.URI, diagnostics.Version, diagnostics.Diagnostics, params)
	}
}

func qualifyActionDiagnostics(text string, p types.InsightDiagnosticPublication, originals []json.RawMessage) codeActionDiagnosticAdjunct {
	omit := func(reason string) codeActionDiagnosticAdjunct {
		return codeActionDiagnosticAdjunct{detail: "diagnostics omitted: " + reason}
	}
	if p.Limited {
		return omit("limited publication")
	}
	if p.ReportedVersion == nil {
		return omit("unversioned publication")
	}
	if *p.ReportedVersion != p.WireVersion {
		return omit("stale or future publication")
	}
	if len(originals) != 1 {
		return omit("original publication JSON unavailable")
	}
	raw := originals[0]
	// Bounded envelope validation work. This is a consumer bound after the
	// upstream JSON-RPC decode, not a claim about its initial frame allocation.
	if len(raw) > lsp.MaxCodeActionDiagnosticBytes+(16<<10) {
		return omit("overbound original publication envelope")
	}
	if !validOriginalDiagnosticJSON(raw) {
		return omit("malformed or ambiguous original publication")
	}
	var envelope map[string]json.RawMessage
	if json.Unmarshal(raw, &envelope) != nil || envelope == nil {
		return omit("malformed original publication")
	}
	var version int
	var uri protocol.DocumentURI
	if json.Unmarshal(envelope["version"], &version) != nil || version != p.WireVersion ||
		json.Unmarshal(envelope["uri"], &uri) != nil {
		return omit("malformed publication metadata")
	}
	path, err := uri.Path()
	if err != nil || filepath.Clean(path) != p.Boundary.Source.Path {
		return omit("malformed publication URI")
	}
	var diagnostics []json.RawMessage
	array := bytes.TrimSpace(envelope["diagnostics"])
	if len(array) == 0 || array[0] != '[' || json.Unmarshal(array, &diagnostics) != nil {
		return omit("malformed diagnostics array")
	}
	if len(diagnostics) > lsp.MaxCodeActionDiagnostics {
		return omit("overbound original diagnostic count")
	}
	total := 0
	for _, d := range diagnostics {
		if len(d) > lsp.MaxCodeActionDiagnosticBytes-total {
			return omit("overbound original diagnostic bytes")
		}
		total += len(d)
	}
	a := codeActionDiagnosticAdjunct{version: version, sequence: p.Sequence, count: len(diagnostics)}
	for _, d := range diagnostics {
		start, end, ok := originalDiagnosticOffsets(text, d)
		if !ok {
			return omit("malformed diagnostic metadata or source range")
		}
		a.diagnostics = append(a.diagnostics, originalActionDiagnostic{raw: bytes.Clone(d), start: start, end: end})
	}
	return a
}

func originalDiagnosticOffsets(text string, raw json.RawMessage) (int, int, bool) {
	var d map[string]json.RawMessage
	if json.Unmarshal(raw, &d) != nil || d == nil {
		return 0, 0, false
	}
	message := bytes.TrimSpace(d["message"])
	if len(message) == 0 || message[0] != '"' {
		return 0, 0, false
	}
	var span map[string]json.RawMessage
	if json.Unmarshal(d["range"], &span) != nil {
		return 0, 0, false
	}
	line, col, validStart := originalDiagnosticPosition(span["start"])
	endLine, endCol, validEnd := originalDiagnosticPosition(span["end"])
	if !validStart || !validEnd {
		return 0, 0, false
	}
	// Protocol's code is an opaque integer-or-string union. Keep its original
	// spelling, including large numeric tokens, without a float64 round-trip.
	if code, present := d["code"]; present {
		value := bytes.TrimSpace(code)
		if len(value) == 0 || (value[0] != '"' && value[0] != '-' && (value[0] < '0' || value[0] > '9')) {
			return 0, 0, false
		}
	}
	start, end, err := codeActionOffsets(text, line, col, endLine, endCol)
	return start, end, err == nil
}

func originalDiagnosticPosition(raw json.RawMessage) (int, int, bool) {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil {
		return 0, 0, false
	}
	var line, col *int
	if json.Unmarshal(fields["line"], &line) != nil || json.Unmarshal(fields["character"], &col) != nil ||
		line == nil || col == nil {
		return 0, 0, false
	}
	return *line, *col, true
}

// encoding/json tolerates duplicate members and replaces invalid string input.
// Original protocol evidence must instead fail closed. This walk uses Number,
// compares decoded member names, bounds nesting and rejects unpaired escapes.
func validOriginalDiagnosticJSON(raw []byte) bool {
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if !originalDiagnosticValue(decoder, 0) {
		return false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return false
	}
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
			return false
		}
		low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
		if err != nil || low < 0xdc00 || low > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}

func originalDiagnosticValue(d *json.Decoder, depth int) bool {
	token, err := d.Token()
	if err != nil {
		return false
	}
	delim, container := token.(json.Delim)
	if !container {
		return true
	}
	if depth >= 64 {
		return false
	}
	switch delim {
	case '{':
		seen := make(map[string]bool)
		for d.More() {
			key, err := d.Token()
			name, ok := key.(string)
			if err != nil || !ok || seen[name] {
				return false
			}
			seen[name] = true
			if !originalDiagnosticValue(d, depth+1) {
				return false
			}
		}
	case '[':
		for d.More() {
			if !originalDiagnosticValue(d, depth+1) {
				return false
			}
		}
	default:
		return false
	}
	_, err = d.Token()
	return err == nil
}
