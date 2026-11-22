package workspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"crude/lsp/lspedit"
	"crude/lsp/lspinlay"
	"crude/types/insight"
	"crude/workspace/document"
)

const (
	inlayMaxViews         = 16
	inlayMaxSequences     = 128
	inlayMaxRetainedBytes = 16 << 20
	inlayLifetime         = 5 * time.Minute
)

// ErrInlayBusy is a definite non-admission: no provider or sequence changed.
var ErrInlayBusy = errors.New("inlay request capacity is occupied")

type inlayViewKey struct {
	owner document.Owner
	view  string
}

// The document owner retains one immutable response per private view. Tombstones
// retain its last admitted sequence until expiry, so a cancelled/failed request
// cannot make an older request current again. This is ephemeral read evidence,
// not a second document, persistence or mutation authority.
type inlayView struct {
	source   insight.Source
	sequence uint64
	expires  time.Time
	ctx      context.Context
	cancel   context.CancelFunc
	view     insight.LSPInlaySet
	items    []lspinlay.Hint
	rawBytes int
	check    func() error
}

func (v *inlayView) retire() {
	v.cancel()
	v.view = insight.LSPInlaySet{}
	v.items, v.check, v.rawBytes = nil, nil, 0
}

func validLSPView(view string, sequence uint64) bool {
	return view != "" && len(view) <= 128 && utf8.ValidString(view) &&
		!strings.ContainsAny(view, "\x00\r\n") && sequence > 0
}

func (s *DocumentService) pruneInlayViewsLocked() {
	now := time.Now()
	for key, view := range s.inlayViews {
		if now.After(view.expires) {
			view.cancel()
			delete(s.inlayViews, key)
			continue
		}
		if view.ctx.Err() != nil {
			continue
		} // Keep the sequence fence, not evidence.
		_, err := s.readOnlyLSPSourceLocked(key.owner, view.source)
		if err != nil || view.check != nil && view.check() != nil {
			view.retire()
		}
	}
}

func (s *DocumentService) closeInlayViewsLocked(owner document.Owner, documentID string) {
	for key, view := range s.inlayViews {
		if key.owner == owner && (documentID == "" || view.source.DocumentID == documentID) {
			// Detach ends the owner epoch. Closing one file does not: another
			// shell can retain its source while this owner reopens the file.
			// Keep the bounded sequence fence, but discard all old evidence.
			if documentID == "" {
				view.cancel()
				delete(s.inlayViews, key)
			} else {
				view.retire()
			}
		}
	}
}

// RequestLSPInlayHints is a read-only visible-range query over acknowledged
// canonical text, including unsaved text. A private view sequence invalidates
// its older work before dispatch, not just when the result eventually arrives.
func (w *Workspace) RequestLSPInlayHints(ctx context.Context, owner document.Owner, source insight.Source, viewID string, sequence uint64, span insight.LSPInlayRange) (insight.LSPInlaySet, error) {
	if w == nil || w.LiveDocuments == nil || w.LSPMgr == nil {
		return insight.LSPInlaySet{}, errors.New("inlay hints require canonical source and a language service")
	}
	if !validLSPView(viewID, sequence) {
		return insight.LSPInlaySet{}, errors.New("invalid inlay view or sequence")
	}
	if err := ctx.Err(); err != nil {
		return insight.LSPInlaySet{}, err
	}
	s := w.LiveDocuments
	key := inlayViewKey{owner, viewID}
	s.mu.Lock()
	s.pruneInlayViewsLocked()
	text, err := s.readOnlyLSPSourceLocked(owner, source)
	if err == nil {
		_, _, err = inlayRangeOffsets(string(text), span)
	}
	previous := s.inlayViews[key]
	if err == nil && previous != nil && sequence <= previous.sequence {
		err = errors.New("inlay view request was superseded")
	}
	if err == nil && previous == nil && len(s.inlayViews) >= inlayMaxSequences {
		err = ErrInlayBusy
	}
	if err == nil {
		active := 0
		for k, other := range s.inlayViews {
			if k != key && other.ctx.Err() == nil {
				active++
			}
		}
		if active >= inlayMaxViews {
			err = ErrInlayBusy
		}
	}
	if err == nil && s.inlayRunning >= inlayMaxViews {
		err = ErrInlayBusy
	}
	if err != nil {
		s.mu.Unlock()
		return insight.LSPInlaySet{}, err
	}
	text = bytes.Clone(text)
	lifetime, retire := context.WithTimeout(context.Background(), inlayLifetime)
	expires, _ := lifetime.Deadline()
	entry := &inlayView{source: source, sequence: sequence, expires: expires, ctx: lifetime, cancel: retire}
	if previous != nil {
		previous.cancel()
	}
	if s.inlayViews == nil {
		s.inlayViews = make(map[inlayViewKey]*inlayView)
	}
	s.inlayViews[key] = entry
	s.inlayRunning++
	s.mu.Unlock()

	requestCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(lifetime, cancel)
	defer func() {
		stop()
		cancel()
		s.mu.Lock()
		s.inlayRunning--
		if s.inlayViews[key] == entry && entry.view.ID == "" {
			entry.retire() // retain only a bounded sequence tombstone on failure
		}
		s.mu.Unlock()
	}()
	response, err := w.LSPMgr.RequestInlayHints(requestCtx, source.Path, span)
	if err != nil {
		return insight.LSPInlaySet{}, err
	}
	if response.Boundary.Source != source {
		return insight.LSPInlaySet{}, document.ErrRevision
	}
	items, result, retainedBytes, err := normalizeInlayHints(string(text), span, response)
	if err != nil {
		return insight.LSPInlaySet{}, err
	}
	check, err := w.LSPMgr.inlayFence(response.Boundary, response.WireVersion)
	if err != nil {
		return insight.LSPInlaySet{}, err
	}
	result.ID, result.ViewID, result.Sequence = "inlay-"+rand.Text(), viewID, sequence
	result.ExpiresAt = entry.expires
	identity, err := json.Marshal(struct {
		Owner         document.Owner
		ID, View, Raw string
		Sequence      uint64
		Boundary      insight.Boundary
		Wire          int
		Range         insight.LSPInlayRange
		Answer        string
		Resolve       bool
	}{owner, result.ID, viewID, reviewedHash(string(response.JSON)), sequence, result.Boundary,
		result.WireVersion, span, result.Answer, result.ResolveSupported})
	if err != nil {
		return insight.LSPInlaySet{}, err
	}
	result.Digest = reviewedHash(string(identity))
	s.mu.Lock()
	defer s.mu.Unlock()
	// The retained view is the authority, not its asynchronous provider
	// cancellation relay. A released invocation cannot publish again.
	if err := entry.ctx.Err(); err != nil {
		return insight.LSPInlaySet{}, err
	}
	if err := requestCtx.Err(); err != nil {
		return insight.LSPInlaySet{}, err
	}
	if s.inlayViews[key] != entry {
		return insight.LSPInlaySet{}, errors.New("inlay view was replaced during its request")
	}
	if _, err := s.readOnlyLSPSourceLocked(owner, source); err != nil {
		return insight.LSPInlaySet{}, err
	}
	if err := check(); err != nil {
		return insight.LSPInlaySet{}, err
	}
	for _, other := range s.inlayViews {
		if other != entry {
			retainedBytes += other.rawBytes
		}
	}
	if retainedBytes > inlayMaxRetainedBytes {
		return insight.LSPInlaySet{}, errors.New("retained inlay evidence exceeds its 16 MiB raw-object bound")
	}
	entry.view, entry.items, entry.check = result, items, check
	for _, item := range items {
		entry.rawBytes += len(item.Raw)
	}
	return cloneInlaySet(result), nil
}

func inlayRangeOffsets(text string, span insight.LSPInlayRange) (int, int, error) {
	if len(text) > 2<<20 {
		return 0, 0, errors.New("inlay source exceeds its 2 MiB consumer bound")
	}
	for _, p := range []insight.LSPInlayPosition{span.Start, span.End} {
		if p.Line < 0 || p.Character < 0 || p.Line > math.MaxInt32 || p.Character > math.MaxInt32 {
			return 0, 0, errors.New("invalid inlay range position")
		}
	}
	start, err := lspedit.Offset(text, span.Start.Line, span.Start.Character)
	if err != nil {
		return 0, 0, err
	}
	end, err := lspedit.Offset(text, span.End.Line, span.End.Character)
	if err != nil {
		return 0, 0, err
	}
	if end < start {
		return 0, 0, errors.New("inlay request range is reversed")
	}
	return start, end, nil
}

func normalizeInlayHints(text string, span insight.LSPInlayRange, response insight.LSPInlayResponse) ([]lspinlay.Hint, insight.LSPInlaySet, int, error) {
	start, end, err := inlayRangeOffsets(text, span)
	if err != nil {
		return nil, insight.LSPInlaySet{}, 0, err
	}
	out := insight.LSPInlaySet{Boundary: response.Boundary, WireVersion: response.WireVersion, Range: span,
		ResolveSupported: response.ResolveSupported,
		Detail:           "Read-only range evidence. Standard hints are unversioned; locations, commands and text edits are metadata only."}
	out.Boundary.Context.UnknownInputs = slices.Clone(response.Boundary.Context.UnknownInputs)
	if response.Unsupported {
		if len(response.JSON) != 0 || response.ResolveSupported {
			return nil, insight.LSPInlaySet{}, 0, errors.New("unsupported inlay response invented result or resolve evidence")
		}
		out.Answer, out.Detail = insight.LSPInlayUnsupported, "This synchronized provider does not advertise inlay hints."
		return nil, out, 0, nil
	}
	parsed, err := lspinlay.Parse(response.JSON)
	if err != nil {
		return nil, insight.LSPInlaySet{}, 0, err
	}
	out.Answer = parsed.Answer
	retained := 0
	for index, hint := range parsed.Hints {
		row, err := inlayHintSummary(text, index, hint)
		if err != nil {
			return nil, insight.LSPInlaySet{}, 0, err
		}
		// Hints are anchors, not selected source spans. Include the end anchor
		// so zero-length/EOF requests and hints at the final visible EOL work.
		if row.SourceByte < start || row.SourceByte > end {
			return nil, insight.LSPInlaySet{}, 0, errors.New("inlay hint is outside the requested source anchor range")
		}
		out.Hints = append(out.Hints, row)
		retained += len(hint.Raw)
	}
	return parsed.Hints, out, retained, nil
}

func inlayHintSummary(text string, index int, hint lspinlay.Hint) (insight.LSPInlayHint, error) {
	off, err := lspedit.Offset(text, hint.Position.Line, hint.Position.Character)
	if err != nil {
		return insight.LSPInlayHint{}, err
	}
	out := insight.LSPInlayHint{Index: index, SourceByte: off,
		Position: insight.LSPInlayPosition{Line: hint.Position.Line, Character: hint.Position.Character},
		Label:    hint.Label, LabelIsParts: hint.LabelIsParts, Kind: hint.Kind, HasKind: hint.HasKind,
		PaddingLeft: hint.PaddingLeft, PaddingRight: hint.PaddingRight, Tooltip: inlayMarkup(hint.Tooltip),
		HasTextEdits: hint.HasTextEdits, HasData: hint.HasData}
	for _, part := range hint.Parts {
		row := insight.LSPInlayLabelPart{Value: part.Value, Tooltip: inlayMarkup(part.Tooltip), HasCommand: part.HasCommand}
		if loc := part.Location; loc != nil {
			row.Location = &insight.LSPInlayLocation{URI: loc.URI, Range: insight.LSPInlayRange{
				Start: insight.LSPInlayPosition{Line: loc.Range.Start.Line, Character: loc.Range.Start.Character},
				End:   insight.LSPInlayPosition{Line: loc.Range.End.Line, Character: loc.Range.End.Character}}}
		}
		out.Parts = append(out.Parts, row)
	}
	return out, nil
}

func inlayMarkup(m lspinlay.Markup) insight.LSPInlayMarkup {
	return insight.LSPInlayMarkup{Present: m.Present, Form: m.Form, Kind: m.Kind, Value: m.Value}
}

func cloneInlayHint(h insight.LSPInlayHint) insight.LSPInlayHint {
	h.Parts = slices.Clone(h.Parts)
	for i := range h.Parts {
		if loc := h.Parts[i].Location; loc != nil {
			copy := *loc
			h.Parts[i].Location = &copy
		}
	}
	return h
}

func cloneInlaySet(s insight.LSPInlaySet) insight.LSPInlaySet {
	s.Boundary.Context.UnknownInputs = slices.Clone(s.Boundary.Context.UnknownInputs)
	s.Hints = slices.Clone(s.Hints)
	for i := range s.Hints {
		s.Hints[i] = cloneInlayHint(s.Hints[i])
	}
	return s
}
