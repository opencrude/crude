package workspace

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"crude/document"
	"crude/lspcompletion"
	"crude/lspedit"
	"crude/types"
)

const (
	completionMaxViews         = 16
	completionMaxSequences     = 128
	completionMaxRetainedBytes = 16 << 20
	completionLifetime         = 5 * time.Minute
)

type completionViewKey struct {
	owner document.Owner
	view  string
}

// A view owns one ephemeral invocation, its raw original objects and detached
// display evidence. A retired sequence remains a tombstone until expiry.
type completionView struct {
	source    types.InsightSource
	sequence  uint64
	expires   time.Time
	ctx       context.Context
	cancel    context.CancelFunc
	set       types.LSPCompletionSet
	originals map[string][]byte
	resolved  map[string]types.LSPCompletionDocumentation
	bytes     int
	check     func() error
}

func (v *completionView) retire() {
	v.cancel()
	v.set, v.originals, v.resolved, v.bytes, v.check = types.LSPCompletionSet{}, nil, nil, 0, nil
}

func (s *DocumentService) pruneCompletionViewsLocked() {
	for key, view := range s.completionViews {
		_, err := s.readOnlyLSPSourceLocked(key.owner, view.source)
		if time.Now().After(view.expires) || err != nil || view.check != nil && view.check() != nil {
			view.cancel()
			delete(s.completionViews, key)
		}
	}
}

func (s *DocumentService) closeCompletionViewsLocked(owner document.Owner, documentID string) {
	for key, view := range s.completionViews {
		if key.owner == owner && (documentID == "" || view.source.DocumentID == documentID) {
			view.cancel()
			delete(s.completionViews, key)
		}
	}
}

func (w *Workspace) RequestLSPCompletions(ctx context.Context, owner document.Owner, source types.InsightSource, viewID string, sequence uint64, line, col int) (types.LSPCompletionSet, error) {
	if w == nil || w.LiveDocuments == nil || w.LSPMgr == nil {
		return types.LSPCompletionSet{}, errors.New("completion requires canonical source and a language service")
	}
	if !validLSPView(viewID, sequence) {
		return types.LSPCompletionSet{}, errors.New("invalid completion view or sequence")
	}
	if err := ctx.Err(); err != nil {
		return types.LSPCompletionSet{}, err
	}
	s, key := w.LiveDocuments, completionViewKey{owner, viewID}
	s.mu.Lock()
	s.pruneCompletionViewsLocked()
	text, err := s.readOnlyLSPSourceLocked(owner, source)
	if err == nil {
		err = validateNavigationPosition(string(text), line, col)
	}
	previous := s.completionViews[key]
	if err == nil && previous != nil && sequence <= previous.sequence {
		err = errors.New("completion invocation was superseded")
	}
	if err == nil && previous == nil && len(s.completionViews) >= completionMaxSequences {
		err = errors.New("completion sequence tracking is full; close an earlier document view")
	}
	active := 0
	for k, other := range s.completionViews {
		if k != key && other.ctx.Err() == nil {
			active++
		}
	}
	if err == nil && active >= completionMaxViews {
		err = errors.New("close an earlier completion view before requesting another")
	}
	if err != nil {
		s.mu.Unlock()
		return types.LSPCompletionSet{}, err
	}
	text = bytes.Clone(text)
	lifetime, retire := context.WithTimeout(context.Background(), completionLifetime)
	expires, _ := lifetime.Deadline()
	entry := &completionView{source: source, sequence: sequence, expires: expires, ctx: lifetime, cancel: retire}
	if previous != nil {
		previous.cancel()
	}
	if s.completionViews == nil {
		s.completionViews = make(map[completionViewKey]*completionView)
	}
	s.completionViews[key] = entry
	s.mu.Unlock()

	requestCtx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(lifetime, cancel)
	defer func() {
		stop()
		cancel()
		s.mu.Lock()
		if s.completionViews[key] == entry && entry.set.ID == "" {
			entry.retire()
		}
		s.mu.Unlock()
	}()
	response, err := w.LSPMgr.RequestCompletions(requestCtx, source.Path, line, col)
	if err != nil {
		return types.LSPCompletionSet{}, err
	}
	if response.Boundary.Source != source {
		return types.LSPCompletionSet{}, document.ErrRevision
	}
	originals, result, retained, err := normalizeCompletionSet(string(text), line, col, response)
	if err != nil {
		return types.LSPCompletionSet{}, err
	}
	check, err := w.LSPMgr.completionFence(response.Boundary, response.WireVersion, response.FeatureGeneration)
	if err != nil {
		return types.LSPCompletionSet{}, err
	}
	result.ID, result.ViewID, result.Sequence, result.ExpiresAt = "completion-"+rand.Text(), viewID, sequence, expires
	identity, err := json.Marshal(struct {
		Owner           document.Owner
		ID, View, Raw   string
		Sequence        uint64
		Boundary        types.InsightBoundary
		Wire, Line, Col int
	}{owner, result.ID, viewID, reviewedHash(string(response.JSON)), sequence, result.Boundary, result.WireVersion, line, col})
	if err != nil {
		return types.LSPCompletionSet{}, err
	}
	result.Digest = reviewedHash(string(identity))
	s.mu.Lock()
	defer s.mu.Unlock()
	// Release retires the authoritative invocation synchronously. Its
	// AfterFunc relay may not have cancelled the provider context yet.
	if err := entry.ctx.Err(); err != nil {
		return types.LSPCompletionSet{}, err
	}
	if err := requestCtx.Err(); err != nil {
		return types.LSPCompletionSet{}, err
	}
	if s.completionViews[key] != entry {
		return types.LSPCompletionSet{}, errors.New("completion view was replaced during its request")
	}
	if _, err := s.readOnlyLSPSourceLocked(owner, source); err != nil {
		return types.LSPCompletionSet{}, err
	}
	if err := check(); err != nil {
		return types.LSPCompletionSet{}, err
	}
	if !s.completionBudgetLocked(entry, retained) {
		return types.LSPCompletionSet{}, errors.New("retained completion evidence exceeds its 16 MiB consumer bound")
	}
	entry.set, entry.originals, entry.bytes, entry.check = result, originals, retained, check
	entry.resolved = make(map[string]types.LSPCompletionDocumentation)
	return cloneCompletionSet(result), nil
}

func (s *DocumentService) completionBudgetLocked(entry *completionView, proposed int) bool {
	for _, other := range s.completionViews {
		if other != entry {
			proposed += other.bytes
		}
	}
	return proposed <= completionMaxRetainedBytes
}

func normalizeCompletionSet(text string, line, col int, response types.LSPCompletionResponse) (map[string][]byte, types.LSPCompletionSet, int, error) {
	out := types.LSPCompletionSet{Boundary: response.Boundary, WireVersion: response.WireVersion, FeatureGeneration: response.FeatureGeneration, Line: line, Col: col,
		ResolveSupported: response.ResolveSupported,
		Detail:           "Standard completion evidence is unversioned. Only the original insertion is used; resolve may change documentation and detail only."}
	out.Boundary.Context.UnknownInputs = slices.Clone(response.Boundary.Context.UnknownInputs)
	if response.Unsupported {
		if len(response.JSON) != 0 || response.ResolveSupported {
			return nil, types.LSPCompletionSet{}, 0, errors.New("unsupported completion invented result or resolve evidence")
		}
		out.Answer, out.Detail = types.LSPCompletionUnsupported, "This synchronized provider does not advertise completions."
		return nil, out, 0, nil
	}
	parsed, err := lspcompletion.Parse(response.JSON)
	if err != nil {
		return nil, types.LSPCompletionSet{}, 0, err
	}
	out.Incomplete = parsed.Incomplete
	out.Answer = types.LSPCompletionItems
	if parsed.Null {
		out.Answer = types.LSPCompletionNull
	} else if len(parsed.Items) == 0 {
		out.Answer = types.LSPCompletionEmpty
	}
	originals := make(map[string][]byte, len(parsed.Items))
	cursor, err := lspedit.Offset(text, line, col)
	if err != nil {
		return nil, types.LSPCompletionSet{}, 0, err
	}
	normalizedBytes, rawBytes := 0, 0
	for index, original := range parsed.Items {
		item, err := lspcompletion.Insertion(original)
		if errors.Is(err, lspcompletion.ErrUnsupported) {
			out.UnsupportedItems++
			continue
		}
		if err != nil {
			return nil, types.LSPCompletionSet{}, 0, err
		}
		primary, err := lspcompletion.PrimaryEdit(text, line, col, item)
		if err != nil {
			return nil, types.LSPCompletionSet{}, 0, err
		}
		// The canonical request, not a later private caret, chooses any implicit
		// prefix. Explicit and implicit completions now share one exact edit.
		item.TextEdit = &primary
		start, err := lspedit.Offset(text, primary.Line, primary.Col)
		if err != nil {
			return nil, types.LSPCompletionSet{}, 0, err
		}
		id := fmt.Sprintf("item-%d", index)
		row := types.LSPCompletionEntry{ID: id, Index: index, Insertion: item,
			Prefix: text[start:cursor], FilterText: original.FilterText, SortText: original.SortText,
			Documentation: completionDocumentation(original.Documentation)}
		encoded, err := json.Marshal(row)
		if err != nil {
			return nil, types.LSPCompletionSet{}, 0, err
		}
		normalizedBytes += len(encoded)
		if normalizedBytes > lspcompletion.MaxRawBytes {
			return nil, types.LSPCompletionSet{}, 0, errors.New("expanded completion evidence exceeds its 1 MiB consumer bound")
		}
		out.Entries = append(out.Entries, row)
		originals[id] = original.Original
		rawBytes += len(original.Original)
	}
	return originals, out, normalizedBytes + rawBytes, nil
}

func completionDocumentation(doc lspcompletion.Documentation) types.LSPCompletionDocumentation {
	return types.LSPCompletionDocumentation{HasDetail: doc.HasDetail, Present: doc.HasDocumentation,
		Detail: doc.Detail, Form: doc.Form, Kind: doc.Kind, Value: doc.Value}
}

func cloneCompletionSet(in types.LSPCompletionSet) types.LSPCompletionSet {
	in.Boundary.Context.UnknownInputs = slices.Clone(in.Boundary.Context.UnknownInputs)
	in.Entries = slices.Clone(in.Entries)
	for i := range in.Entries {
		in.Entries[i].Insertion = types.CloneCompletionItem(in.Entries[i].Insertion)
	}
	return in
}
