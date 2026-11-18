// Package predictioncontext selects the context of one editor suggestion from
// an immutable, caller-owned snapshot. It does no I/O, parsing or
// authorization, and it never waits: every input is already in memory.
//
// The cursor block is an editable region of whole lines around the caret.
// The model rewrites that region, so one request can insert text at the
// caret, change nearby lines or change a later line in the region.
package predictioncontext

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"crude/agent/provider"
	"crude/types"
)

const (
	// BuilderVersion changes whenever the selected context changes shape.
	BuilderVersion = "editor-suggestion-context-v2"
	// MaxSourceBytes is the largest document that suggestions read.
	MaxSourceBytes = 2 << 20

	regionLinesBefore = 4
	regionLinesAfter  = 6
	regionMaxBytes    = 3 << 10
	excerptBefore     = 6 << 10
	excerptAfter      = 2 << 10
	maxEdits          = 8
	maxEditBytes      = 600
	maxSnippets       = 4
	maxSnippetBytes   = 1200
	maxDiagnostics    = 6
	maxCompletions    = 8
)

var (
	ErrInvalid = errors.New("invalid suggestion context input")
	ErrBudget  = errors.New("suggestion context does not fit its budget")
)

// Snippet is related code from another place in the workspace.
type Snippet struct {
	Path string
	Line int
	Text string
}

// Input is a complete snapshot. Text and Caret come from the canonical
// document; the other sources are already qualified by their owners.
type Input struct {
	Path        string // workspace-relative, forward slashes
	Language    string
	Text        []byte
	Caret       int // UTF-8 byte offset
	Diagnostics []types.LSPDiagnostic
	RecentEdits []types.RecentEdit // newest last
	Snippets    []Snippet
	Completions []string
}

// Context is the selected, bounded context of one request.
type Context struct {
	Path, Language string
	// RegionStart and RegionEnd delimit the editable region in the source.
	RegionStart, RegionEnd int
	// Region is the region's source text; Caret is the caret offset in it.
	Region string
	Caret  int
	// Before and After are file excerpts around the region.
	Before, After string
	// Prefix and Suffix are the fill-in-the-middle windows at the caret.
	Prefix, Suffix string
	Edits          []types.RecentEdit
	Snippets       []Snippet
	Diagnostics    []string
	Completions    []string
	// Digest identifies the complete selection for response reuse.
	Digest          string
	EstimatedTokens int
}

// Build selects context within about budgetTokens. Optional sources are
// dropped first (snippets, then edits, then excerpts); the region stays.
func Build(in Input, budgetTokens int) (Context, error) {
	if len(in.Text) > MaxSourceBytes || in.Caret < 0 || in.Caret > len(in.Text) ||
		!utf8.Valid(in.Text) || (in.Caret < len(in.Text) && !utf8.RuneStart(in.Text[in.Caret])) {
		return Context{}, ErrInvalid
	}
	text := in.Text
	start, end := regionBounds(text, in.Caret)
	c := Context{
		Path: in.Path, Language: in.Language, RegionStart: start, RegionEnd: end,
		Region: string(text[start:end]), Caret: in.Caret - start,
		Before: tailLines(text[:start], excerptBefore), After: headLines(text[end:], excerptAfter),
		Prefix: string(text[max(0, in.Caret-excerptBefore):in.Caret]),
		Suffix: string(text[in.Caret:min(len(text), in.Caret+excerptAfter)]),
	}
	c.Prefix = trimToRuneStart(c.Prefix)
	c.Suffix = trimToRuneEnd(c.Suffix)
	regionFirst, regionLast := lineOf(text, start), lineOf(text, max(start, end-1))
	for _, d := range in.Diagnostics {
		if len(c.Diagnostics) >= maxDiagnostics {
			break
		}
		if d.Line >= regionFirst-2 && d.Line <= regionLast+2 {
			c.Diagnostics = append(c.Diagnostics, fmt.Sprintf("line %d: %s", d.Line+1, clip(firstLine(d.Message), 200)))
		}
	}
	for _, label := range in.Completions {
		if len(c.Completions) >= maxCompletions {
			break
		}
		if label = strings.TrimSpace(firstLine(label)); label != "" {
			c.Completions = append(c.Completions, clip(label, 80))
		}
	}
	edits := in.RecentEdits
	if len(edits) > maxEdits {
		edits = edits[len(edits)-maxEdits:]
	}
	for _, e := range edits {
		e.Removed, e.Inserted = clip(e.Removed, maxEditBytes), clip(e.Inserted, maxEditBytes)
		c.Edits = append(c.Edits, e)
	}
	for _, s := range in.Snippets {
		if len(c.Snippets) >= maxSnippets {
			break
		}
		s.Text = clip(s.Text, maxSnippetBytes)
		c.Snippets = append(c.Snippets, s)
	}
	for !c.fits(budgetTokens) {
		switch {
		case len(c.Snippets) > 0:
			c.Snippets = c.Snippets[:len(c.Snippets)-1]
		case len(c.Edits) > 0:
			c.Edits = c.Edits[1:]
		case len(c.Before) > 512:
			c.Before = tailLines([]byte(c.Before), len(c.Before)/2)
			c.Prefix = c.Prefix[len(c.Prefix)-min(len(c.Prefix), len(c.Before)+c.Caret):]
			c.Prefix = trimToRuneStart(c.Prefix)
		case len(c.After) > 256:
			c.After = headLines([]byte(c.After), len(c.After)/2)
		default:
			return Context{}, ErrBudget
		}
	}
	c.digest()
	return c, nil
}

// regionBounds returns whole lines around the caret, within regionMaxBytes.
func regionBounds(text []byte, caret int) (int, int) {
	start := lineStart(text, caret)
	for i := 0; i < regionLinesBefore && start > 0; i++ {
		prev := lineStart(text, start-1)
		if caret-prev > regionMaxBytes/2 {
			break
		}
		start = prev
	}
	end := lineEnd(text, caret)
	for i := 0; i < regionLinesAfter && end < len(text); i++ {
		next := lineEnd(text, end+1)
		if next-start > regionMaxBytes {
			break
		}
		end = next
	}
	if end < len(text) && text[end] == '\n' {
		end++ // the region owns its last line break
	}
	return start, end
}

func lineStart(text []byte, at int) int {
	for at > 0 && text[at-1] != '\n' {
		at--
	}
	return at
}

func lineEnd(text []byte, at int) int {
	for at < len(text) && text[at] != '\n' {
		at++
	}
	return at
}

func lineOf(text []byte, at int) int {
	n := 0
	for _, b := range text[:at] {
		if b == '\n' {
			n++
		}
	}
	return n
}

func tailLines(text []byte, limit int) string {
	if len(text) <= limit {
		return string(text)
	}
	cut := len(text) - limit
	for cut < len(text) && text[cut-1] != '\n' {
		cut++
	}
	return string(text[cut:])
}

func headLines(text []byte, limit int) string {
	if len(text) <= limit {
		return string(text)
	}
	cut := limit
	for cut > 0 && text[cut-1] != '\n' {
		cut--
	}
	return string(text[:cut])
}

func trimToRuneStart(s string) string {
	for len(s) > 0 && !utf8.RuneStart(s[0]) {
		s = s[1:]
	}
	return s
}

func trimToRuneEnd(s string) string {
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func clip(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func (c *Context) fits(budget int) bool {
	return c.estimate() <= budget
}

func (c *Context) estimate() int {
	n := provider.EstimateTokens(c.Region, "") + provider.EstimateTokens(c.Before, "") + provider.EstimateTokens(c.After, "")
	for _, e := range c.Edits {
		n += provider.EstimateTokens(e.Removed, "") + provider.EstimateTokens(e.Inserted, "") + 12
	}
	for _, s := range c.Snippets {
		n += provider.EstimateTokens(s.Text, "") + 12
	}
	for _, d := range c.Diagnostics {
		n += provider.EstimateTokens(d, "")
	}
	for _, l := range c.Completions {
		n += provider.EstimateTokens(l, "")
	}
	c.EstimatedTokens = n
	return n
}

func (c *Context) digest() {
	h := sha256.New()
	write := func(parts ...string) {
		for _, p := range parts {
			fmt.Fprintf(h, "%d:%s;", len(p), p)
		}
	}
	write(BuilderVersion, c.Path, c.Language, c.Region, fmt.Sprint(c.Caret), c.Before, c.After)
	for _, e := range c.Edits {
		write(e.Path, fmt.Sprint(e.Line), e.Author, e.Removed, e.Inserted)
	}
	for _, s := range c.Snippets {
		write(s.Path, fmt.Sprint(s.Line), s.Text)
	}
	write(strings.Join(c.Diagnostics, "\n"), strings.Join(c.Completions, "\n"))
	c.Digest = hex.EncodeToString(h.Sum(nil))
}
