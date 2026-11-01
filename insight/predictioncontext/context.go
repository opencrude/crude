// Package predictioncontext prepares bounded literal context from an immutable
// caller-owned canonical snapshot. It does no I/O, parsing, acquisition or
// authorization. Build belongs on request workers, never a presentation path.
package predictioncontext

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"crude/provider"
	"crude/types"
)

const (
	BuilderVersion   = "editor-prediction-context-v1"
	MaxSourceBytes   = 2 << 20
	MaxContextBytes  = 32 << 10
	MaxIdentityBytes = 4096
	MaxDiagnostics   = 256
	MaxSelectedFacts = 16
	MaxMetadataBytes = 128 << 10
	maxWindowBytes   = 16 << 10
	maxMessageBytes  = 4096
	maxUnknownInputs = 64
	maxReasonBytes   = 512
	maxLanguageBytes = 128
)

var (
	ErrInvalid = errors.New("invalid prediction context input")
	ErrLimit   = errors.New("prediction context input exceeds bounds")
	ErrBudget  = errors.New("prediction context budget cannot fit metadata")
)

type Input struct {
	Source      types.InsightSource
	Text        []byte
	Caret       int // Exact UTF-8 byte offset, not a visual or UTF-16 column.
	Language    string
	Diagnostics types.InsightDiagnostics
}

// Coverage is a partial observation, never project completeness or freshness
// authority. SourceStart/End delimit the selected literal window in input bytes.
type Coverage struct {
	Partial       bool   `json:"partial"`
	SourceStart   int    `json:"source_start"`
	Caret         int    `json:"caret"`
	SourceEnd     int    `json:"source_end"`
	SourceBytes   int    `json:"source_bytes"`
	Diagnostics   string `json:"diagnostics"`
	ObservedFacts int    `json:"observed_facts"`
	SelectedFacts int    `json:"selected_facts"`
}

type Context struct {
	BuilderVersion string                    `json:"builder"`
	Source         types.InsightSource       `json:"source"`
	Language       string                    `json:"language"`
	Prefix         string                    `json:"prefix"`
	Suffix         string                    `json:"suffix"`
	Facts          *types.InsightDiagnostics `json:"facts"`
	MissingInputs  []string                  `json:"missing_inputs"`
	Coverage       Coverage                  `json:"coverage"`
	// FactsJSON contains qualified facts, source identity, coverage and missing
	// inputs, without duplicating literal prefix/suffix. JSON is the complete
	// canonical selected envelope. Both are bounded, detached encodings.
	FactsJSON       string `json:"-"`
	JSON            string `json:"-"`
	Digest          string `json:"-"`
	EstimatedTokens int    `json:"-"`
}

// Build requires an immutable snapshot for the duration of the call. The caller
// must recheck source lifetime/revision before egress or use; the content digest
// is not an authority token and cannot resurrect a retired source.
//
// EstimatedTokens covers the complete JSON encoding (including fact/coverage
// keys) AND independently rounded Language+Prefix+Suffix+FactsJSON estimates,
// taking the greater. Prompt escaping/framing and system text are the caller's
// separate budget. There is no model tokenizer or wire-usage claim here.
func Build(in Input, maxEstimatedTokens int) (Context, error) {
	if maxEstimatedTokens <= 0 {
		return Context{}, ErrBudget
	}
	if err := validate(in); err != nil {
		return Context{}, err
	}
	c := Context{
		BuilderVersion: BuilderVersion, Source: cloneSource(in.Source),
		Language:      strings.Clone(in.Language),
		MissingInputs: []string{"enclosing-scope", "imports", "signatures", "recent-edits", "project-coverage"},
		Coverage: Coverage{Partial: true, Caret: in.Caret, SourceStart: in.Caret,
			SourceEnd: in.Caret, SourceBytes: len(in.Text), Diagnostics: diagnosticStatus(in)},
	}
	if c.Coverage.Diagnostics == "exact-current-partial-context" {
		if !validPositions(in.Text, in.Diagnostics.Diagnostics) {
			return Context{}, ErrInvalid
		}
		d := cloneFacts(in.Diagnostics)
		slices.Sort(d.Boundary.Context.UnknownInputs)
		slices.SortFunc(d.Diagnostics, compareDiagnostic)
		c.Coverage.ObservedFacts = len(d.Diagnostics)
		d.Diagnostics = slices.Clone(d.Diagnostics[:min(len(d.Diagnostics), MaxSelectedFacts)])
		c.Facts = &d
	}
	// Reserve literal context before optional facts.
	reserve := min(256, len(in.Text))
	for {
		c.setWindow(in, reserve)
		c.qualifyFacts()
		c.encode()
		if c.fits(maxEstimatedTokens) {
			break
		}
		if c.Facts != nil {
			if len(c.Facts.Diagnostics) > 0 {
				c.Facts.Diagnostics[len(c.Facts.Diagnostics)-1] = types.LSPDiagnostic{}
				c.Facts.Diagnostics = c.Facts.Diagnostics[:len(c.Facts.Diagnostics)-1]
			} else {
				c.Facts = nil
				c.Coverage.Diagnostics = "omitted-budget"
			}
			continue
		}
		if reserve > 0 {
			reserve /= 2
			continue
		}
		return Context{}, ErrBudget
	}
	// A bounded binary search maximizes a near-caret byte window. UTF-8 and
	// CRLF cuts are moved inward; no source byte is normalized or synthesized.
	best := c
	low, high := reserve+1, min(len(in.Text), maxWindowBytes)
	for low <= high {
		mid := low + (high-low)/2
		c.setWindow(in, mid)
		c.encode()
		if c.fits(maxEstimatedTokens) {
			best = c
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	sum := sha256.Sum256([]byte(best.JSON))
	best.Digest = hex.EncodeToString(sum[:])
	return best, nil
}

func (c *Context) qualifyFacts() {
	c.Coverage.SelectedFacts = 0
	if c.Facts != nil {
		c.Coverage.SelectedFacts = len(c.Facts.Diagnostics)
		c.Facts.Limited = c.Coverage.SelectedFacts < c.Coverage.ObservedFacts
		if c.Facts.Limited {
			c.Coverage.Diagnostics = "selected-subset-partial-context"
		}
	}
	c.MissingInputs = c.MissingInputs[:5]
	if c.Facts == nil || c.Facts.Limited {
		c.MissingInputs = append(c.MissingInputs, "diagnostics")
	}
}

func (c *Context) setWindow(in Input, n int) {
	before := min(in.Caret, (n*2+2)/3)
	after := min(len(in.Text)-in.Caret, n-before)
	before = min(in.Caret, n-after)
	start, end := in.Caret-before, in.Caret+after
	for start < in.Caret && !utf8.RuneStart(in.Text[start]) {
		start++
	}
	for end > in.Caret && end < len(in.Text) && !utf8.RuneStart(in.Text[end]) {
		end--
	}
	if start > 0 && start < in.Caret && in.Text[start-1] == '\r' && in.Text[start] == '\n' {
		start++
	}
	if end > in.Caret && end < len(in.Text) && in.Text[end-1] == '\r' && in.Text[end] == '\n' {
		end--
	}
	c.Prefix = string(in.Text[start:in.Caret])
	c.Suffix = string(in.Text[in.Caret:end])
	c.Coverage.SourceStart, c.Coverage.SourceEnd = start, end
}

func (c *Context) encode() {
	// These structs contain only JSON-supported values; validation has already
	// bounded every string/count. Marshal cannot fail on these concrete types.
	facts, _ := json.Marshal(struct {
		Builder  string                    `json:"builder"`
		Source   types.InsightSource       `json:"source"`
		Facts    *types.InsightDiagnostics `json:"facts"`
		Missing  []string                  `json:"missing_inputs"`
		Coverage Coverage                  `json:"coverage"`
	}{c.BuilderVersion, c.Source, c.Facts, c.MissingInputs, c.Coverage})
	c.FactsJSON = string(facts)
	full, _ := json.Marshal(c)
	c.JSON = string(full)
	c.EstimatedTokens = max(provider.EstimateTokens(c.JSON, ""),
		provider.EstimateTokens(c.Language, "")+provider.EstimateTokens(c.Prefix, "")+
			provider.EstimateTokens(c.Suffix, "")+provider.EstimateTokens(c.FactsJSON, ""))
}

func (c Context) fits(tokens int) bool {
	return c.EstimatedTokens <= tokens && len(c.JSON) <= MaxContextBytes &&
		len(c.FactsJSON)+len(c.Language)+len(c.Prefix)+len(c.Suffix) <= MaxContextBytes
}

func diagnosticStatus(in Input) string {
	d := in.Diagnostics
	switch {
	case d.ID == "":
		return "unavailable"
	case d.Boundary.Source != in.Source:
		return "foreign-source"
	case d.Limited || d.State == types.InsightLimited:
		return "limited"
	case d.State != types.InsightCurrent:
		return "not-current"
	case !d.HasExactSource():
		return "unverified-version"
	case d.Sequence == 0 || d.Boundary.Context.Generation == 0 ||
		d.Boundary.Context.ID == "" || d.Boundary.Context.Root == "" ||
		d.Boundary.Producer.InstanceID == "" || d.Boundary.Producer.Provider == "" ||
		d.Boundary.Producer.ToolIdentity == "":
		return "unqualified"
	default:
		return "exact-current-partial-context"
	}
}

func validate(in Input) error {
	// Reject sizes before inspecting bytes, allocating, cloning or sorting.
	if len(in.Text) > MaxSourceBytes || len(in.Language) > maxLanguageBytes ||
		len(in.Diagnostics.Diagnostics) > MaxDiagnostics ||
		len(in.Diagnostics.Boundary.Context.UnknownInputs) > maxUnknownInputs {
		return ErrLimit
	}
	if in.Caret < 0 || in.Caret > len(in.Text) || in.Source.Kind != "canonical" ||
		in.Source.Revision == 0 {
		return ErrInvalid
	}
	for _, v := range []string{in.Source.WorkspaceGeneration, in.Source.DocumentID, in.Source.Path, in.Source.Lifetime} {
		if v == "" {
			return ErrInvalid
		}
	}
	d := in.Diagnostics
	weight := 0
	check := func(s string, limit int) error {
		if len(s) > limit {
			return ErrLimit
		}
		weight += len(s)
		if weight > MaxMetadataBytes {
			return ErrLimit
		}
		if !utf8.ValidString(s) {
			return ErrInvalid
		}
		return nil
	}
	for _, s := range []types.InsightSource{in.Source, d.Boundary.Source} {
		for _, v := range []string{s.WorkspaceGeneration, s.DocumentID, s.Path, s.Lifetime, s.Kind} {
			if err := check(v, MaxIdentityBytes); err != nil {
				return err
			}
		}
	}
	for _, v := range []string{d.ID, d.Binding, string(d.State), d.Boundary.Context.ID,
		d.Boundary.Context.Root, d.Boundary.Producer.InstanceID, d.Boundary.Producer.Provider,
		d.Boundary.Producer.ToolIdentity} {
		if err := check(v, MaxIdentityBytes); err != nil {
			return err
		}
	}
	for _, v := range d.Boundary.Context.UnknownInputs {
		if err := check(v, maxReasonBytes); err != nil {
			return err
		}
	}
	if err := check(d.Detail, maxReasonBytes); err != nil {
		return err
	}
	for _, fact := range d.Diagnostics {
		if fact.Line < 0 || fact.Col < 0 || fact.EndLine < fact.Line ||
			fact.EndCol < 0 || (fact.Line == fact.EndLine && fact.EndCol < fact.Col) ||
			fact.Severity < 0 || fact.Severity > types.DiagHint {
			return ErrInvalid
		}
		for _, v := range []string{fact.Message, fact.Source, fact.Code} {
			if err := check(v, maxMessageBytes); err != nil {
				return err
			}
		}
	}
	if !utf8.Valid(in.Text) || !utf8.ValidString(in.Language) ||
		(in.Caret < len(in.Text) && !utf8.RuneStart(in.Text[in.Caret])) {
		return ErrInvalid
	}
	return nil
}

// Validate all requested UTF-16 endpoints in one source scan, without allocating
// a source-sized line index. CRLF is a line separator, not two columns.
func validPositions(text []byte, diagnostics []types.LSPDiagnostic) bool {
	points := make(map[[2]int]bool, len(diagnostics)*2)
	for _, d := range diagnostics {
		points[[2]int{d.Line, d.Col}] = false
		points[[2]int{d.EndLine, d.EndCol}] = false
	}
	if len(points) == 0 {
		return true
	}
	line, col, remaining := 0, 0, len(points)
	for i := 0; ; {
		key := [2]int{line, col}
		if found, ok := points[key]; ok && !found {
			points[key] = true
			remaining--
			if remaining == 0 {
				return true
			}
		}
		if i == len(text) {
			break
		}
		r, n := utf8.DecodeRune(text[i:])
		i += n
		if r == '\r' && i < len(text) && text[i] == '\n' {
			i++
			line, col = line+1, 0
		} else if r == '\n' {
			line, col = line+1, 0
		} else {
			col++
			if r > 0xffff {
				col++
			}
		}
	}
	return false
}

func cloneSource(s types.InsightSource) types.InsightSource {
	s.WorkspaceGeneration = strings.Clone(s.WorkspaceGeneration)
	s.DocumentID, s.Path = strings.Clone(s.DocumentID), strings.Clone(s.Path)
	s.Lifetime, s.Kind = strings.Clone(s.Lifetime), strings.Clone(s.Kind)
	return s
}

func cloneFacts(d types.InsightDiagnostics) types.InsightDiagnostics {
	d = types.CloneInsightDiagnostics(d)
	d.ID, d.Binding, d.Detail = strings.Clone(d.ID), strings.Clone(d.Binding), strings.Clone(d.Detail)
	d.State = types.InsightState(strings.Clone(string(d.State)))
	d.Boundary.Source = cloneSource(d.Boundary.Source)
	b := &d.Boundary
	b.Context.ID, b.Context.Root = strings.Clone(b.Context.ID), strings.Clone(b.Context.Root)
	for i, v := range b.Context.UnknownInputs {
		b.Context.UnknownInputs[i] = strings.Clone(v)
	}
	b.Producer.InstanceID = strings.Clone(b.Producer.InstanceID)
	b.Producer.Provider, b.Producer.ToolIdentity = strings.Clone(b.Producer.Provider), strings.Clone(b.Producer.ToolIdentity)
	for i := range d.Diagnostics {
		f := &d.Diagnostics[i]
		f.Message, f.Source, f.Code = strings.Clone(f.Message), strings.Clone(f.Source), strings.Clone(f.Code)
	}
	return d
}

func compareDiagnostic(a, b types.LSPDiagnostic) int {
	for _, pair := range [][2]int{{a.Line, b.Line}, {a.Col, b.Col}, {a.EndLine, b.EndLine},
		{a.EndCol, b.EndCol}, {a.Severity, b.Severity}} {
		if pair[0] < pair[1] {
			return -1
		}
		if pair[0] > pair[1] {
			return 1
		}
	}
	for _, pair := range [][2]string{{a.Source, b.Source}, {a.Code, b.Code}, {a.Message, b.Message}} {
		if cmp := strings.Compare(pair[0], pair[1]); cmp != 0 {
			return cmp
		}
	}
	return 0
}
