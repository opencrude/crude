// Editor-private numeric snippet anchors and mirror planning. Text and history
// remain owned by the existing buffer and edit transaction.
package editor

import (
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"crude/snippet"
	"crude/types"
)

var errSnippetRetired = errors.New("snippet interaction must retire")

type snippetOccurrence struct {
	stop, start, end, parent int
	choices                  []string
	live                     bool
}

type snippetSplice struct {
	start, end int
	text       string
}

type snippetSession struct {
	occurrences []snippetOccurrence
	start, end  int // primary insertion span; end is also the final fallback
	active      int
}

func newSnippetSession(text string, plan *types.CompletionSnippet, offset int) (*snippetSession, error) {
	if offset < 0 || offset > int(^uint(0)>>1)-len(text) {
		return nil, errSnippetRetired
	}
	if err := snippet.Validate(text, plan); err != nil {
		return nil, err
	}
	s := &snippetSession{start: offset, end: offset + len(text), active: -1}
	for _, o := range plan.Occurrences {
		s.occurrences = append(s.occurrences, snippetOccurrence{
			stop: o.Stop, start: offset + o.StartByte, end: offset + o.EndByte,
			parent: o.Parent, choices: slices.Clone(o.Choices), live: true,
		})
	}
	for i, o := range s.occurrences {
		if o.stop > 0 && (s.active < 0 || o.stop < s.occurrences[s.active].stop) {
			s.active = i
		}
	}
	return s, nil
}

func (s *snippetSession) selectStop(stop int) bool {
	for i, o := range s.occurrences {
		if o.live && o.stop == stop && stop > 0 {
			s.active = i
			return true
		}
	}
	return false
}

func (s *snippetSession) inside(index, parent int) bool {
	for p := index; p >= 0; p = s.occurrences[p].parent {
		if p == parent {
			return true
		}
	}
	return false
}

// observeLocal consumes one primitive's PRE-edit coordinates after its text
// mutation. It never writes recursively from the PieceTable's observer.
// Only active-field edits can initiate mirrors. Foreign/cross-field edits retire
// private interaction without refusing or undoing the user's actual edit.
func (s *snippetSession) observeLocal(edit snippetSplice) error {
	if s.active < 0 || edit.start < 0 || edit.end < edit.start || !utf8.ValidString(edit.text) {
		return errSnippetRetired
	}
	a := s.occurrences[s.active]
	if !a.live || edit.start < a.start || edit.end > a.end {
		return errSnippetRetired
	}
	delta := len(edit.text) - (edit.end - edit.start)
	// Touched descendants lose their semantic slot, not the accepted bytes.
	// Their copies are removed when their ancestor snippetTemplate propagates.
	for i, o := range s.occurrences {
		if !o.live || i == s.active || !s.inside(i, s.active) {
			continue
		}
		touched := edit.start < o.end && edit.end > o.start
		if edit.start == edit.end {
			touched = o.start <= edit.start && edit.start <= o.end
		} else if o.start == o.end {
			touched = edit.start <= o.start && o.start <= edit.end
		}
		if touched || !s.occurrences[o.parent].live {
			s.occurrences[i].live = false
		}
	}
	for i := range s.occurrences {
		o := &s.occurrences[i]
		if !o.live {
			continue
		}
		if s.inside(s.active, i) {
			o.end += delta // active field and its actual containing instances
			continue
		}
		if o.start >= edit.end && (edit.start != edit.end || o.start > edit.start || i > s.active) {
			o.start += delta
			o.end += delta
		}
	}
	s.end += delta
	return nil
}

// preparePeer runs only in the canonical publication's prepare phase on a clone.
// The sorted disjoint edits share PRE-batch coordinates, not sequential offsets.
// Readiness, own ACK and savepoint-only updates must not be submitted as edits.
// A touching/intersecting peer snippetSplice retires interaction; it never initiates
// a local mirror or changes the canonical publication's acceptance.
func (s *snippetSession) preparePeer(edits []snippetSplice) (*snippetSession, error) {
	if len(edits) > snippet.MaxOccurrences {
		return nil, errSnippetRetired
	}
	for i, edit := range edits {
		if edit.start < 0 || edit.end < edit.start || !utf8.ValidString(edit.text) ||
			i > 0 && (edit.start < edits[i-1].end || edit.start == edits[i-1].start) {
			return nil, errSnippetRetired
		}
	}
	next := s.clone()
	for i := len(edits) - 1; i >= 0; i-- {
		edit := edits[i]
		delta := len(edit.text) - (edit.end - edit.start)
		for _, o := range next.occurrences {
			if o.live && edit.start <= o.end && edit.end >= o.start {
				return nil, errSnippetRetired
			}
		}
		for i := range next.occurrences {
			o := &next.occurrences[i]
			if o.live && o.start > edit.end {
				o.start += delta
				o.end += delta
			}
		}
		if next.start > edit.end {
			next.start += delta
		}
		if next.end > edit.end {
			next.end += delta
		}
	}
	return next, nil
}

func (s *snippetSession) clone() *snippetSession {
	next := *s
	next.occurrences = slices.Clone(s.occurrences)
	return &next // choices are immutable initial alternatives
}

type snippetReadSpan func(int, int) (string, error)

type snippetTemplate struct {
	text        string
	occurrences []snippetOccurrence // relative to text, in preorder
}

type snippetMirrorBuilder struct {
	source   *snippetSession
	read     snippetReadSpan
	first    map[int]int
	values   map[int]snippetTemplate
	visiting map[int]bool
	bytes    int
	nodes    int
}

// prepareMirrors is called once at the outer edit-group finalizer. It proposes
// maximal disjoint root-snippetOccurrence splices, never a document export or second
// writer. An editor must validate/apply ALL these through its existing exact
// recorded snippetSplice helper in the originating logical group, then install next.
// Errors propose no effects; the local edit already observed remains accepted.
func (s *snippetSession) prepareMirrors(read snippetReadSpan) ([]snippetSplice, *snippetSession, error) {
	if s.active < 0 || !s.occurrences[s.active].live {
		return nil, nil, errSnippetRetired
	}
	b := snippetMirrorBuilder{
		source: s, read: read, first: make(map[int]int), values: make(map[int]snippetTemplate),
		visiting: make(map[int]bool),
	}
	for i, o := range s.occurrences {
		if o.live {
			if _, exists := b.first[o.stop]; !exists {
				b.first[o.stop] = i
			}
		}
	}
	b.first[s.occurrences[s.active].stop] = s.active
	next := &snippetSession{start: s.start, end: s.end, active: -1}
	var changes []snippetSplice
	delta := 0
	for _, o := range s.occurrences {
		if !o.live || o.parent >= 0 {
			continue
		}
		value, err := b.build(o.stop, 0)
		if err != nil {
			return nil, nil, err
		}
		actual, err := read(o.start, o.end)
		if err != nil {
			return nil, nil, err
		}
		if actual != value.text {
			change := snippetSplice{start: o.start, end: o.end, text: value.text}
			// Known source order disambiguates co-located empty occurrences;
			// combine them instead of submitting ambiguous simultaneous inserts.
			if len(changes) > 0 && changes[len(changes)-1].end == change.start {
				last := &changes[len(changes)-1]
				last.end = change.end
				last.text += change.text
			} else {
				changes = append(changes, change)
			}
		}
		base := len(next.occurrences)
		for _, child := range value.occurrences {
			child.start += o.start + delta
			child.end += o.start + delta
			if child.parent >= 0 {
				child.parent += base
			}
			next.occurrences = append(next.occurrences, child)
		}
		delta += len(value.text) - (o.end - o.start)
		if len(next.occurrences) > snippet.MaxOccurrences {
			return nil, nil, errSnippetRetired
		}
	}
	next.end += delta
	if next.end-next.start > snippet.MaxExpandedBytes {
		return nil, nil, errSnippetRetired
	}
	if !next.selectStop(s.occurrences[s.active].stop) {
		return nil, nil, errSnippetRetired
	}
	return changes, next, nil
}

func (b *snippetMirrorBuilder) build(stop, depth int) (snippetTemplate, error) {
	if depth > snippet.MaxDepth || b.visiting[stop] {
		return snippetTemplate{}, errSnippetRetired
	}
	if value, found := b.values[stop]; found {
		return value, nil
	}
	index, found := b.first[stop]
	if !found {
		return snippetTemplate{}, errSnippetRetired
	}
	o := b.source.occurrences[index]
	if o.end < o.start || o.end-o.start > snippet.MaxExpandedBytes {
		return snippetTemplate{}, errSnippetRetired
	}
	raw, err := b.read(o.start, o.end)
	if err != nil || len(raw) != o.end-o.start || !utf8.ValidString(raw) {
		return snippetTemplate{}, errSnippetRetired
	}
	root := snippetOccurrence{stop: stop, parent: -1, choices: o.choices, live: true}
	value := snippetTemplate{occurrences: []snippetOccurrence{root}}
	b.visiting[stop] = true
	if index == b.source.active {
		value.text = raw
		oldToNew := map[int]int{index: 0}
		for i := index + 1; i < len(b.source.occurrences) && b.source.inside(i, index); i++ {
			child := b.source.occurrences[i]
			if !child.live {
				continue
			}
			parent, exists := oldToNew[child.parent]
			if !exists {
				return snippetTemplate{}, errSnippetRetired
			}
			oldToNew[i] = len(value.occurrences)
			child.start -= o.start
			child.end -= o.start
			child.parent = parent
			value.occurrences = append(value.occurrences, child)
		}
	} else {
		var text strings.Builder
		last := 0
		for _, child := range b.source.occurrences {
			if !child.live || child.parent != index {
				continue
			}
			start, end := child.start-o.start, child.end-o.start
			if start < last || end < start || end > len(raw) {
				return snippetTemplate{}, errSnippetRetired
			}
			nested, err := b.build(child.stop, depth+1)
			if err != nil {
				return snippetTemplate{}, err
			}
			if len(nested.text) > snippet.MaxExpandedBytes-text.Len()-(start-last) {
				return snippetTemplate{}, errSnippetRetired
			}
			text.WriteString(raw[last:start])
			start = text.Len()
			text.WriteString(nested.text)
			base := len(value.occurrences)
			for _, slot := range nested.occurrences {
				slot.start += start
				slot.end += start
				if slot.parent < 0 {
					slot.parent = 0
				} else {
					slot.parent += base
				}
				value.occurrences = append(value.occurrences, slot)
			}
			last = end
		}
		if len(raw)-last > snippet.MaxExpandedBytes-text.Len() {
			return snippetTemplate{}, errSnippetRetired
		}
		text.WriteString(raw[last:])
		value.text = text.String()
	}
	value.occurrences[0].end = len(value.text)
	b.bytes += len(value.text)
	b.nodes += len(value.occurrences)
	if b.bytes > 2<<20 || b.nodes > snippet.MaxOccurrences*(snippet.MaxDepth+1) {
		return snippetTemplate{}, errSnippetRetired
	}
	b.visiting[stop] = false
	b.values[stop] = value
	return value, nil
}
