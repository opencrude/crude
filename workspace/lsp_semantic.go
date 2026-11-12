package workspace

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf16"

	"crude/document"
	"crude/lspedit"
	"crude/lspsemantic"
	"crude/types"
)

// ErrSemanticBusy reports a known non-admission: no view or provider state
// changed, so a source-qualified reader may retry after the shared cooldown.
var ErrSemanticBusy = errors.New("semantic request capacity is occupied")

// SemanticTokenClient is optional: unsupported transports never need a dummy
// implementation and do not cause a second language-server process.
type SemanticTokenClient interface {
	RequestSemanticTokens(context.Context, string, string, string, types.LSPSemanticRange) (types.LSPSemanticResponse, error)
}
type semanticBase struct {
	boundary types.InsightBoundary
	result   lspsemantic.Result
	support  types.LSPSemanticSupport
}
type semanticView struct {
	source   types.InsightSource
	sequence uint64
	expires  time.Time
	cancel   context.CancelFunc
	loading  bool
	base     *semanticBase
}

func (s *DocumentService) closeSemanticViewsLocked(owner document.Owner, id string) {
	for key, v := range s.semanticViews {
		if key.owner == owner && (id == "" || v.source.DocumentID == id) {
			v.cancel()
			if id == "" {
				delete(s.semanticViews, key)
			} else {
				// Keep the sequence fence across a peer-retained reopen, but
				// never reuse the closed view's semantic delta base.
				v.base = nil
				v.loading = false
			}
		}
	}
}
func (s *DocumentService) pruneSemanticViewsLocked() {
	for key, v := range s.semanticViews {
		if time.Now().After(v.expires) {
			v.cancel()
			delete(s.semanticViews, key)
		}
	}
}
func (s *DocumentService) ReleaseLSPSemantic(owner document.Owner, view string, sequence uint64) error {
	if !validLSPView(view, sequence) {
		return errors.New("invalid semantic view or sequence")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Requests and releases are independent read jobs. The release can arrive
	// first, including ahead of a newer request while an older one is retained.
	// Record its sequence before any late request can publish or retain a base.
	if s.closed || !s.attachedOwner(owner) {
		return nil
	}
	s.pruneSemanticViewsLocked()
	key := inlayViewKey{owner, view}
	previous := s.semanticViews[key]
	if previous != nil && previous.sequence > sequence {
		return nil
	}
	if previous == nil && len(s.semanticViews) >= inlayMaxSequences {
		return errors.New("too many semantic views")
	}
	if previous != nil {
		previous.cancel()
	}
	if s.semanticViews == nil {
		s.semanticViews = make(map[inlayViewKey]*semanticView)
	}
	s.semanticViews[key] = &semanticView{sequence: sequence, expires: time.Now().Add(inlayLifetime), cancel: func() {}}
	return nil
}

// RequestLSPSemantic qualifies every answer against an acknowledged source and
// active owner. The core retains only bounded delta bases per view, and cancels
// older sequences before dispatch. A result ID never comes from a shell.
func (w *Workspace) RequestLSPSemantic(ctx context.Context, owner document.Owner, source types.InsightSource, view string, sequence uint64, span types.LSPSemanticRange) (types.LSPSemanticSet, error) {
	if w == nil || w.LiveDocuments == nil || w.LSPMgr == nil {
		return types.LSPSemanticSet{}, errors.New("semantic colors need shared source and a language server")
	}
	if !validLSPView(view, sequence) {
		return types.LSPSemanticSet{}, errors.New("invalid semantic view or sequence")
	}
	if err := ctx.Err(); err != nil {
		return types.LSPSemanticSet{}, err
	}
	s := w.LiveDocuments
	key := inlayViewKey{owner, view}
	s.mu.Lock()
	s.pruneSemanticViewsLocked()
	text, err := s.readOnlyLSPSourceLocked(owner, source)
	old := s.semanticViews[key]
	if err == nil && old != nil && sequence <= old.sequence {
		err = errors.New("semantic view request was superseded")
	}
	if err == nil && old == nil && len(s.semanticViews) >= inlayMaxSequences {
		// Retired sequence records expire, so this is also a definite
		// non-admission rather than a permanent provider failure.
		err = ErrSemanticBusy
	}
	// Count actual running requests, including superseded providers that have
	// not honored cancellation. Replacing a map entry must not free a slot.
	if err == nil && s.semanticRunning >= inlayMaxViews {
		err = ErrSemanticBusy
	}

	if err != nil {
		s.mu.Unlock()
		return types.LSPSemanticSet{}, err
	}
	text = bytes.Clone(text)
	// Use one cancellation owner for both the caller and the view. A release
	// under s.mu must revoke publication synchronously, not via an asynchronously
	// scheduled context.AfterFunc relay that can run after the final fence.
	request, cancel := context.WithCancel(ctx)
	entry := &semanticView{source: source, sequence: sequence, expires: time.Now().Add(inlayLifetime), cancel: cancel, loading: true}
	if old != nil {
		old.cancel()
		if old.source.WorkspaceGeneration == source.WorkspaceGeneration && old.source.DocumentID == source.DocumentID && old.source.Lifetime == source.Lifetime && old.source.Path == source.Path {
			entry.base = old.base
		}
	}
	if s.semanticViews == nil {
		s.semanticViews = make(map[inlayViewKey]*semanticView)
	}
	s.semanticViews[key] = entry
	base := entry.base // acquire the immutable pointer before release can clear it
	s.semanticRunning++
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		s.semanticRunning--
		if s.semanticViews[key] == entry {
			entry.loading = false
		}
		s.mu.Unlock()
	}()
	result, nextBase, err := w.LSPMgr.semanticTokens(request, source, span, string(text), base)
	if err != nil {
		return types.LSPSemanticSet{}, err
	}
	fence, err := w.LSPMgr.semanticFence(result.Boundary, result.WireVersion, formattingGate, "semantic colors")
	if err != nil {
		return types.LSPSemanticSet{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.semanticViews[key] != entry || request.Err() != nil {
		return types.LSPSemanticSet{}, context.Canceled
	}
	if _, err := s.readOnlyLSPSourceLocked(owner, source); err != nil {
		return types.LSPSemanticSet{}, err
	}
	if err := fence(); err != nil {
		return types.LSPSemanticSet{}, err
	}
	retained := 0
	for k, v := range s.semanticViews {
		if k != key && v.base != nil {
			retained += len(v.base.result.Data) * 4
		}
	}
	if nextBase != nil && retained+len(nextBase.result.Data)*4 > inlayMaxRetainedBytes {
		nextBase = nil
	}
	entry.base = nextBase
	result.ViewID, result.Sequence = view, sequence
	return result, nil
}

func (m *LSPManager) semanticTokens(ctx context.Context, source types.InsightSource, span types.LSPSemanticRange, text string, base *semanticBase) (types.LSPSemanticSet, *semanticBase, error) {
	if !m.featureOn(formattingGate) {
		return types.LSPSemanticSet{}, nil, errors.New("semantic colors need enabled Code Intelligence")
	}
	var result types.LSPSemanticSet
	var next *semanticBase
	err := m.trackInflight(func() error {
		if err := m.awaitSynchronized(ctx, source.Path); err != nil {
			return err
		}
		client, request, valid, stop := m.requestClientWithin(ctx, source.Path, ManualLSPRequestTimeout)
		defer stop()
		if client == nil {
			return errors.New("semantic colors need a synchronized language server")
		}
		m.mu.Lock()
		doc := m.documents[filepath.Clean(source.Path)]
		wire := 0
		var boundary types.InsightBoundary
		if doc != nil && doc.opened && !doc.syncPending {
			wire, boundary = doc.wireVersion, doc.boundary
			boundary.Context.UnknownInputs = slices.Clone(boundary.Context.UnknownInputs)
		}
		m.mu.Unlock()
		if boundary.Source != source {
			return document.ErrRevision
		}
		fence, err := m.semanticFence(boundary, wire, formattingGate, "semantic colors")
		if err != nil {
			return err
		}
		result = types.LSPSemanticSet{Boundary: boundary, WireVersion: wire, Range: span}
		provider, ok := client.(SemanticTokenClient)
		if !ok {
			result.Unsupported = true
			return fence()
		}
		support, err := provider.RequestSemanticTokens(request, source.Path, "support", "", span)
		if err != nil {
			return err
		}
		if !valid() || request.Err() != nil {
			return context.Canceled
		}
		if err := fence(); err != nil {
			return err
		}
		if support.WireVersion != wire {
			return document.ErrRevision
		}
		if support.Unsupported {
			result.Unsupported = true
			return nil
		}
		legend := lspsemantic.Legend{Types: support.Support.Types, Modifiers: support.Support.Modifiers}
		if err := legend.Validate(); err != nil {
			return err
		}
		result.Types = slices.Clone(legend.Types)
		result.Modifiers = slices.Clone(legend.Modifiers)
		mode := "range"
		previous := ""
		if support.Support.Full {
			mode = "full"
			result.Full = true
			// A delta is specifically relative to a prior result, often from a
			// previous source/context generation. The producer and legend
			// must still be identical; the new answer is fenced and decoded
			// against the current source before any color can be installed.
			if support.Support.Delta && base != nil && base.result.ID != "" && base.boundary.Producer == boundary.Producer && base.boundary.Context.ID == boundary.Context.ID && reflect.DeepEqual(base.support, support.Support) {
				mode = "full/delta"
				previous = base.result.ID
			}
		} else if !support.Support.Range {
			result.Unsupported = true
			return nil
		}
		wireSpan := span
		if mode == "range" && wireSpan.End.Character == -1 {
			start, err := lspedit.Offset(text, wireSpan.End.Line, 0)
			if err != nil {
				return err
			}
			line := text[start:]
			if n := strings.IndexByte(line, '\n'); n >= 0 {
				line = line[:n]
				line = strings.TrimSuffix(line, "\r")
			}
			wireSpan.End.Character = 0
			for _, r := range line {
				wireSpan.End.Character += utf16.RuneLen(r)
			}
		}
		rangeStart, rangeEnd := 0, len(text)
		if mode == "range" {
			a, err := lspedit.Offset(text, wireSpan.Start.Line, wireSpan.Start.Character)
			if err != nil {
				return err
			}
			b, err := lspedit.Offset(text, wireSpan.End.Line, wireSpan.End.Character)
			if err != nil || b < a {
				return errors.New("invalid semantic source range")
			}
			rangeStart, rangeEnd = a, b
		}
		call := func(method, id string) (lspsemantic.Result, []lspsemantic.Span, error) {
			response, err := provider.RequestSemanticTokens(request, source.Path, method, id, wireSpan)
			if request.Err() != nil {
				return lspsemantic.Result{}, nil, request.Err()
			}
			if !valid() {
				return lspsemantic.Result{}, nil, context.Canceled
			}
			if err != nil {
				return lspsemantic.Result{}, nil, err
			}
			if err := fence(); err != nil {
				return lspsemantic.Result{}, nil, err
			}
			if response.WireVersion != wire || response.Unsupported || !reflect.DeepEqual(response.Support, support.Support) {
				return lspsemantic.Result{}, nil, document.ErrRevision
			}
			var raw lspsemantic.Result
			if method == "full/delta" {
				raw, err = lspsemantic.ParseDelta(response.JSON, base.result.Data)
			} else {
				raw, err = lspsemantic.ParseFull(response.JSON)
			}
			if err != nil {
				return lspsemantic.Result{}, nil, err
			}
			if raw.Null {
				return raw, nil, nil
			}
			spans, err := lspsemantic.DecodeContext(request, text, legend, raw.Data)
			if err == nil && method == "range" {
				for _, token := range spans {
					if token.End <= rangeStart || token.Start >= rangeEnd {
						return lspsemantic.Result{}, nil, errors.New("semantic token is outside the requested range")
					}
				}
			}
			return raw, spans, err
		}
		raw, spans, err := call(mode, previous)
		if err != nil && mode == "full/delta" && request.Err() == nil && valid() && fence() == nil {
			raw, spans, err = call("full", "")
		}
		if err != nil {
			return err
		}
		result.Null = raw.Null
		indices := make(map[string]int, len(legend.Types))
		for i, name := range legend.Types {
			indices[name] = i
		}
		result.Spans = make([]types.LSPSemanticSpan, len(spans))
		for i, s := range spans {
			result.Spans[i] = types.LSPSemanticSpan{Start: s.Start, End: s.End, Type: indices[s.Type], Modifiers: s.Modifiers}
		}
		if result.Full && !raw.Null && raw.ID != "" {
			next = &semanticBase{boundary: boundary, result: raw, support: support.Support}
		}
		return fence()
	})
	if err != nil {
		return types.LSPSemanticSet{}, nil, err
	}
	return result, next, ctx.Err()
}
