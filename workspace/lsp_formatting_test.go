package workspace

import (
	"context"
	"crude/document"
	"crude/types"
	"errors"
	"reflect"
	"testing"
)

func (c *navigationClient) RequestRangeFormatting(_ context.Context, path string, _ types.LSPFormattingSpan, _ types.LSPFormattingOptions) (types.LSPFormattingResponse, error) {
	c.mu.Lock()
	wire := c.versions[path]
	c.mu.Unlock()
	c.navMu.Lock()
	out := types.LSPFormattingResponse{WireVersion: wire + c.wireDelta, JSON: []byte(c.raw), Unsupported: c.unsupported}
	err, entered, release := c.err, c.entered, c.release
	c.navMu.Unlock()
	if entered != nil {
		entered <- struct{}{}
	}
	if release != nil {
		<-release
	}
	return out, err
}
func TestRangeFormattingQualifiedPlanAndStrictBoundaries(t *testing.T) {
	f := newNavigationFixture(t, "a😀=1;\r\nz=2;")
	before, err := f.w.LiveDocuments.Snapshot(f.source.DocumentID)
	if err != nil {
		t.Fatal(err)
	}
	in := types.LSPFormattingRequest{Source: f.source, Ranges: []types.LSPFormattingSpan{{Start: 0, End: 8}}, Options: types.LSPFormattingOptions{TabSize: 4, InsertSpaces: true}}
	for _, tc := range []struct {
		raw string
		bad bool
	}{
		{`[{"range":{"start":{"line":0,"character":3},"end":{"line":0,"character":4}},"newText":" = "}]`, false},
		{"null", false}, {"[]", false},
		{`[{"range":{"start":{"line":0,"character":2},"end":{"line":0,"character":3}},"newText":""}]`, true},
		{`[{"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":1}},"newText":"outside"}]`, true},
		{`[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":3}},"newText":"x"},{"range":{"start":{"line":0,"character":1},"end":{"line":0,"character":3}},"newText":"y"}]`, true},
	} {
		f.client.raw = tc.raw
		result, err := f.w.RequestSelectionFormatting(t.Context(), f.owner, in)
		if tc.bad {
			if err == nil || !reflect.DeepEqual(result, types.LSPFormattingResult{}) {
				t.Fatal("invalid result exposed a plan", tc.raw, result, err)
			}
			continue
		}
		if err != nil || result.Boundary.Source != in.Source || result.WireVersion != 1 || result.Boundary.Producer.InstanceID == "" {
			t.Fatal("missing source/provider boundary", result, err)
		}
		if len(result.Edits) > 0 {
			if result.Edits[0] != (document.Edit{Start: 5, End: 6, Text: " = "}) {
				t.Fatal("UTF-16 changed bytes", result.Edits)
			}
		}
	}
	f.unchanged(t, before)
	f.client.wireDelta = 1
	f.client.raw = "[]"
	if out, err := f.w.RequestSelectionFormatting(t.Context(), f.owner, in); !errors.Is(err, document.ErrRevision) || !reflect.DeepEqual(out, types.LSPFormattingResult{}) {
		t.Fatal("wrong wire version accepted", out, err)
	}
	f.client.wireDelta = 0
	f.client.unsupported = true
	out, err := f.w.RequestSelectionFormatting(t.Context(), f.owner, in)
	if err != nil || !out.Unsupported || len(out.Edits) != 0 {
		t.Fatal("unsupported must not fall back to whole file", out, err)
	}
	in.Source.Revision++
	if _, err = f.w.RequestSelectionFormatting(t.Context(), f.owner, in); err == nil {
		t.Fatal("stale source accepted")
	}
}
func TestRangeFormattingRejectsInvalidSourceSelections(t *testing.T) {
	text := "a😀=1;\r\nz"
	for _, ranges := range [][]types.LSPFormattingSpan{nil, {{Start: 1, End: 1}}, {{Start: 2, End: 5}}, {{Start: 0, End: 8}, {Start: 7, End: 8}}, {{Start: 0, End: 9}}, {{Start: 0, End: 200}}, make([]types.LSPFormattingSpan, 257)} {
		if err := validateFormattingRanges(text, types.LSPFormattingRequest{Ranges: ranges, Options: types.LSPFormattingOptions{TabSize: 4}}); err == nil {
			t.Fatal("invalid ranges accepted", ranges)
		}
	}
}
