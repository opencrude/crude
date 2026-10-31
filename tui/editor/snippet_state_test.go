package editor

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"crude/snippet"
)

func snippetStateFixture(t testing.TB, source, prefix, suffix string) (string, *snippetSession) {
	t.Helper()
	text, plan, err := snippet.Parse(source)
	if err != nil {
		t.Fatal(err)
	}
	state, err := newSnippetSession(text, plan, len(prefix))
	if err != nil {
		t.Fatal(err)
	}
	return prefix + text + suffix, state
}

func snippetStateLocal(t testing.TB, text string, state *snippetSession, edit snippetSplice) string {
	t.Helper()
	if edit.start < 0 || edit.end > len(text) || edit.end < edit.start {
		t.Fatal("invalid fixture edit")
	}
	next := text[:edit.start] + edit.text + text[edit.end:]
	if err := state.observeLocal(edit); err != nil {
		t.Fatal(err)
	}
	return next
}

func snippetStateRead(text string) snippetReadSpan {
	return func(start, end int) (string, error) {
		if start < 0 || end < start || end > len(text) {
			return "", errors.New("fixture read beyond actual text")
		}
		return text[start:end], nil
	}
}

func snippetStateMirrored(t testing.TB, text string, state *snippetSession) (string, *snippetSession, []snippetSplice) {
	t.Helper()
	changes, next, err := state.prepareMirrors(snippetStateRead(text))
	if err != nil || next == nil {
		t.Fatalf("mirror preparation: %v", err)
	}
	for i, edit := range changes {
		if edit.start < 0 || edit.end > len(text) || edit.end < edit.start ||
			i > 0 && edit.start <= changes[i-1].end {
			t.Fatalf("mirror batch was overlapping/ambiguous: %+v", changes)
		}
	}
	for i := len(changes) - 1; i >= 0; i-- {
		edit := changes[i]
		text = text[:edit.start] + edit.text + text[edit.end:]
	}
	if next.start < 0 || next.end > len(text) {
		t.Fatal("final fallback escaped actual source")
	}
	values := make(map[int]string)
	for _, o := range next.occurrences {
		if !o.live || o.start < 0 || o.end > len(text) || o.end < o.start {
			t.Fatalf("invalid final occurrence %+v", o)
		}
		value := text[o.start:o.end]
		if old, found := values[o.stop]; found && old != value {
			t.Fatalf("stop %d diverged across copies: %q vs %q", o.stop, old, value)
		}
		values[o.stop] = value
	}
	return text, next, changes
}

func snippetStateReplaceActive(t testing.TB, text string, s *snippetSession, replacement string) string {
	t.Helper()
	active := s.occurrences[s.active]
	text = snippetStateLocal(t, text, s, snippetSplice{start: active.start, end: active.end})
	return snippetStateLocal(t, text, s, snippetSplice{start: active.start, end: active.start, text: replacement})
}

func TestSnippetStateNestedEditUpdatesAllEnclosingMirrorsAndDropsOverwrittenDescendants(t *testing.T) {
	text, s := snippetStateFixture(t, "${1:outer ${2:inner}} / $1 / $2$0", "", "")
	if text != "outer inner / outer inner / inner" || !s.selectStop(2) {
		t.Fatal("bad independent fixture")
	}
	text = snippetStateReplaceActive(t, text, s, "value")
	text, s, changes := snippetStateMirrored(t, text, s)
	if text != "outer value / outer value / value" || len(changes) != 2 ||
		changes[0].start != 14 || changes[0].end != 25 || changes[1].start != 28 {
		t.Fatalf("nested mirrors=%q splices=%+v", text, changes)
	}
	if !s.selectStop(1) {
		t.Fatal("outer stop disappeared")
	}
	text = snippetStateReplaceActive(t, text, s, "custom")
	text, s, _ = snippetStateMirrored(t, text, s)
	if text != "custom / custom / value" || !s.selectStop(2) {
		t.Fatalf("overwritten outer removed independent stop or retained nested defaults: %q", text)
	}
	count := 0
	for _, o := range s.occurrences {
		if o.stop == 2 {
			count++
			if o.parent >= 0 {
				t.Fatal("old inner stop survived in an overwritten mirror")
			}
		}
	}
	if count != 1 {
		t.Fatalf("independent inner mirror count=%d", count)
	}
}

func TestSnippetStateNestedStopPropagatesIntoDifferentParentsNotOnlyItsSelectedAncestry(t *testing.T) {
	text, s := snippetStateFixture(t, "${1:$2} / ${3:$2} / $1 / $3 / ${2:inner}", "", "")
	if !s.selectStop(2) {
		t.Fatal("missing stop")
	}
	text = snippetStateReplaceActive(t, text, s, "next")
	text, _, _ = snippetStateMirrored(t, text, s)
	if text != "next / next / next / next / next" {
		t.Fatalf("nested parent DAG lost a mirror: %q", text)
	}
}

func TestSnippetStateEmptyAdjacentStopsAndRepeatedZeroHaveStableSourceOrder(t *testing.T) {
	text, s := snippetStateFixture(t, "$1$1${2:}$0$0", "", "")
	text = snippetStateLocal(t, text, s, snippetSplice{start: 0, end: 0, text: "x"})
	text, s, _ = snippetStateMirrored(t, text, s)
	if text != "xx" || !s.selectStop(2) {
		t.Fatalf("adjacent mirror=%q", text)
	}
	active := s.occurrences[s.active]
	if active.start != 2 || active.end != 2 {
		t.Fatalf("co-located next stop lost insertion order: %+v", active)
	}
	text = snippetStateLocal(t, text, s, snippetSplice{start: 2, end: 2, text: "Y"})
	text, s, _ = snippetStateMirrored(t, text, s)
	if text != "xxY" || s.end != 3 {
		t.Fatalf("empty next stop text=%q fallback=%d", text, s.end)
	}
	for _, o := range s.occurrences {
		if o.stop == 0 && (o.start != 3 || o.end != 3) {
			t.Fatalf("final zero moved before accepted insertion: %+v", o)
		}
	}
}

func TestSnippetStateRepeatedEmptyMirrorsAreOneUnambiguousSimultaneousInsert(t *testing.T) {
	text, s := snippetStateFixture(t, "$1$1$1$0", "", "")
	text = snippetStateLocal(t, text, s, snippetSplice{start: 0, end: 0, text: "x"})
	text, _, changes := snippetStateMirrored(t, text, s)
	if text != "xxx" || len(changes) != 1 || changes[0].start != 1 ||
		changes[0].end != 1 || changes[0].text != "xx" {
		t.Fatalf("co-located mirror operations=%+v text=%q", changes, text)
	}
}

func TestSnippetStateEditedChoicesUseLiveTextNotImmutableFirstAlternative(t *testing.T) {
	text, s := snippetStateFixture(t, "${1|red,blue,green|}:$1$0", "", "")
	text = snippetStateReplaceActive(t, text, s, "arbitrary")
	text, s, _ = snippetStateMirrored(t, text, s)
	if text != "arbitrary:arbitrary" || !reflect.DeepEqual(s.occurrences[0].choices, []string{"red", "blue", "green"}) {
		t.Fatalf("choice expansion guessed immutable defaults: %q", text)
	}
}

func TestSnippetStateUnicodeCRLFAnchorsRemainBytesAndUnrelatedSourceIsNotExported(t *testing.T) {
	prefix := strings.Repeat("unrelated\n", 100000) + "é😀"
	text, s := snippetStateFixture(t, "${1:α\r\n\t中} / $1$0", prefix, " suffix")
	text = snippetStateReplaceActive(t, text, s, "👩‍💻e\u0301\r\nZ")
	read := snippetStateRead(text)
	changes, next, err := s.prepareMirrors(func(start, end int) (string, error) {
		if start < len(prefix) || end > len(text)-len(" suffix") {
			t.Fatalf("mirror preparation exported unrelated source: [%d,%d)", start, end)
		}
		return read(start, end)
	})
	if err != nil || next == nil || len(changes) != 1 {
		t.Fatal(err)
	}
	for i := len(changes) - 1; i >= 0; i-- {
		edit := changes[i]
		text = text[:edit.start] + edit.text + text[edit.end:]
	}
	if text != prefix+"👩‍💻e\u0301\r\nZ / 👩‍💻e\u0301\r\nZ suffix" {
		t.Fatal("Unicode/CRLF source bytes changed")
	}
}

func TestSnippetStatePeerPreparationDoesNotMutateSessionOrInitiateMirrors(t *testing.T) {
	_, s := snippetStateFixture(t, "${1:word}:$1$0", "prefix ", " suffix")
	before := s.clone()
	next, err := s.preparePeer([]snippetSplice{{start: 0, end: 0, text: "new "}})
	if err != nil || next == nil || !reflect.DeepEqual(s, before) ||
		next.occurrences[0].start != 11 || next.end != s.end+4 {
		t.Fatal("disjoint peer preparation lost atomic anchor ownership")
	}
	invalid, err := s.preparePeer([]snippetSplice{
		{start: 0, end: 0, text: "new "},
		{start: 11, end: 11, text: "touching"},
	})
	if err == nil || invalid != nil || !reflect.DeepEqual(s, before) {
		t.Fatal("failed later peer splice partially moved private anchors")
	}
	next, err = s.preparePeer(nil) // harmless own ACK / savepoint-only publication
	if err != nil || !reflect.DeepEqual(next, s) {
		t.Fatal("empty publication changed private interaction")
	}
}

func TestSnippetStatePeerBatchUsesCanonicalSimultaneousCoordinates(t *testing.T) {
	_, s := snippetStateFixture(t, "${1:word}:$1$0", "prefix ", " suffix")
	before := s.clone()
	// Both locations belong to the SAME pre-edit document. The second is in
	// the suffix, not in a mirror shifted by applying the first prematurely.
	next, err := s.preparePeer([]snippetSplice{
		{start: 0, end: 0, text: "new "},
		{start: 18, end: 18, text: "late"},
	})
	if err != nil || next == nil || next.occurrences[0].start != 11 ||
		next.end != 20 || !reflect.DeepEqual(s, before) {
		t.Fatalf("simultaneous disjoint peer batch was interpreted sequentially: %+v, %v", next, err)
	}
}

func TestSnippetStateCrossFieldAndMirrorFailureDoNotRollBackAcceptedUserBytes(t *testing.T) {
	text, s := snippetStateFixture(t, "${1:a}:$1$0", "", "")
	edited := text[:1] + "outside" + text[2:]
	if err := s.observeLocal(snippetSplice{start: 1, end: 2, text: "outside"}); err == nil {
		t.Fatal("cross-field edit did not retire interaction")
	}
	if edited != "aoutsidea" {
		t.Fatal("fixture unexpectedly lost user bytes")
	}
	text, s = snippetStateFixture(t, "${1:a}:$1$0", "", "")
	text = snippetStateReplaceActive(t, text, s, "accepted")
	changes, next, err := s.prepareMirrors(func(int, int) (string, error) {
		return "", errors.New("source-read failure")
	})
	if err == nil || changes != nil || next != nil || text != "accepted:a" {
		t.Fatal("failed mirror preparation proposed partial effects or erased local edit")
	}
}

func FuzzSnippetMirrorPlanner(f *testing.F) {
	for _, data := range []string{"", "1abc2xyz", "\x00\x01\xff", "outer", "\xff\xff\x00\x00"} {
		f.Add([]byte(data))
	}
	sources := []string{
		"${1:outer ${2:inner}} / $1 / $2$0",
		"$1$1$1${2:}$0$0",
		"${1:$2} / ${3:$2} / $1 / $3 / ${2:inner}",
		"${1|red,blue|}:$1:${2:nested ${3:x}}/$2$0",
		"${1:a$0b}$1",
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 60 {
			t.Skip()
		}
		source := sources[0]
		if len(data) > 0 {
			source = sources[int(data[0])%len(sources)]
		}
		text, s := snippetStateFixture(t, source, "PREFIX ", " SUFFIX")
		for i := 0; i+2 < len(data); i += 3 {
			stop := int(data[i]%3) + 1
			if !s.selectStop(stop) {
				continue
			}
			value := string(rune(data[i+1])) + "😀" + string(rune(data[i+2]))
			text = snippetStateReplaceActive(t, text, s, value)
			text, s, _ = snippetStateMirrored(t, text, s)
			if !strings.HasPrefix(text, "PREFIX ") || !strings.HasSuffix(text, " SUFFIX") {
				t.Fatal("mirror splice changed unrelated document content")
			}
			for index, o := range s.occurrences {
				if o.parent >= 0 {
					parent := s.occurrences[o.parent]
					if o.parent >= index || o.start < parent.start || o.end > parent.end {
						t.Fatal("rebuilt mirror ancestry escaped its actual parent")
					}
				}
			}
			again, next, err := s.prepareMirrors(snippetStateRead(text))
			if err != nil || len(again) != 0 || !reflect.DeepEqual(s, next) {
				t.Fatal("settled mirror plan was not idempotent")
			}
		}
	})
}

func BenchmarkNestedSnippetMirrorPlan(b *testing.B) {
	text, original := snippetStateFixture(b, "${1:outer ${2:inner}} / $1 / $2$0",
		strings.Repeat("unrelated ", 100000), " suffix")
	original.selectStop(2)
	text = snippetStateReplaceActive(b, text, original, "value")
	read := snippetStateRead(text)
	b.ResetTimer()
	for b.Loop() {
		if _, _, err := original.prepareMirrors(read); err != nil {
			b.Fatal(err)
		}
	}
}

func TestSnippetStateOverlargeEditedValueRefusesMirrorsWithoutLosingText(t *testing.T) {
	text, s := snippetStateFixture(t, "${1:a}:$1$0", "", "")
	value := strings.Repeat("x", snippet.MaxExpandedBytes+1)
	text = snippetStateReplaceActive(t, text, s, value)
	changes, next, err := s.prepareMirrors(snippetStateRead(text))
	if err == nil || changes != nil || next != nil || text != value+":a" {
		t.Fatal("over-limit edited snippet lost accepted text or published partial mirrors")
	}
}
