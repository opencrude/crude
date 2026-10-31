package editor

import (
	"errors"
	"reflect"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"crude/tui/rendering"
)

func mustInlineProjection(t testing.TB, raw string, ins []InlineInsertion) *inlineProjection {
	t.Helper()
	p, err := buildInlineProjection(raw, ins)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func inlineRunText(p *inlineProjection) string {
	var b strings.Builder
	for _, r := range p.Runs {
		b.WriteString(r.Cluster)
	}
	return b.String()
}

func TestInlineProjectionLiteralGeometry(t *testing.T) {
	p := mustInlineProjection(t, "\t界e\u0301😀Z", []InlineInsertion{
		{ByteOffset: 1, Index: 42, Label: ":T", PaddingLeft: true, PaddingRight: true},
	})
	if p.SourceWidth != 10 || p.DisplayWidth != 14 || inlineRunText(p) != "     :T 界e\u0301😀Z" {
		t.Fatalf("width/text: source=%d display=%d text=%q", p.SourceWidth, p.DisplayWidth, inlineRunText(p))
	}
	// Literal paint expectations: source tab cells 0..3; padding 4,7;
	// label 5,6; CJK 8..9; e+accent 10; emoji 11..12; Z 13.
	wantRuns := []inlineRun{
		{DisplayStart: 0, Width: 1, SourceByte: 0, SourceEnd: 1, SourceCol: 0, AnchorCol: 0, Cluster: " ", HintIndex: -1},
		{DisplayStart: 1, Width: 1, SourceByte: 0, SourceEnd: 1, SourceCol: 1, AnchorCol: 0, Cluster: " ", HintIndex: -1},
		{DisplayStart: 2, Width: 1, SourceByte: 0, SourceEnd: 1, SourceCol: 2, AnchorCol: 0, Cluster: " ", HintIndex: -1},
		{DisplayStart: 3, Width: 1, SourceByte: 0, SourceEnd: 1, SourceCol: 3, AnchorCol: 0, Cluster: " ", HintIndex: -1},
		{DisplayStart: 4, Width: 1, SourceByte: 1, SourceEnd: 1, SourceCol: 4, AnchorCol: 4, Cluster: " ", HintIndex: 42, Padding: true},
		{DisplayStart: 5, Width: 1, SourceByte: 1, SourceEnd: 1, SourceCol: 4, AnchorCol: 4, Cluster: ":", HintIndex: 42},
		{DisplayStart: 6, Width: 1, SourceByte: 1, SourceEnd: 1, SourceCol: 4, AnchorCol: 4, Cluster: "T", HintIndex: 42},
		{DisplayStart: 7, Width: 1, SourceByte: 1, SourceEnd: 1, SourceCol: 4, AnchorCol: 4, Cluster: " ", HintIndex: 42, Padding: true},
		{DisplayStart: 8, Width: 2, SourceByte: 1, SourceEnd: 4, SourceCol: 4, AnchorCol: 4, Cluster: "界", HintIndex: -1},
		{DisplayStart: 10, Width: 1, SourceByte: 4, SourceEnd: 7, SourceCol: 6, AnchorCol: 6, Cluster: "e\u0301", HintIndex: -1},
		{DisplayStart: 11, Width: 2, SourceByte: 7, SourceEnd: 11, SourceCol: 7, AnchorCol: 7, Cluster: "😀", HintIndex: -1},
		{DisplayStart: 13, Width: 1, SourceByte: 11, SourceEnd: 12, SourceCol: 9, AnchorCol: 9, Cluster: "Z", HintIndex: -1},
	}
	if !reflect.DeepEqual(p.Runs, wantRuns) {
		t.Fatalf("paint runs:\n got %#v\nwant %#v", p.Runs, wantRuns)
	}
	wantHits := []inlineHit{
		{SourceByte: 0, SourceCol: 0, HintIndex: -1},
		{SourceByte: 0, SourceCol: 0, HintIndex: -1},
		{SourceByte: 0, SourceCol: 0, HintIndex: -1},
		{SourceByte: 0, SourceCol: 0, HintIndex: -1},
		{SourceByte: 1, SourceCol: 4, HintIndex: 42, Padding: true},
		{SourceByte: 1, SourceCol: 4, HintIndex: 42},
		{SourceByte: 1, SourceCol: 4, HintIndex: 42},
		{SourceByte: 1, SourceCol: 4, HintIndex: 42, Padding: true},
		{SourceByte: 1, SourceCol: 4, HintIndex: -1},
		{SourceByte: 1, SourceCol: 4, HintIndex: -1},
		{SourceByte: 4, SourceCol: 6, HintIndex: -1},
		{SourceByte: 7, SourceCol: 7, HintIndex: -1},
		{SourceByte: 7, SourceCol: 7, HintIndex: -1},
		{SourceByte: 11, SourceCol: 9, HintIndex: -1},
		{SourceByte: 12, SourceCol: 10, HintIndex: -1},
	}
	for cell, want := range wantHits {
		if got := p.displayHit(cell); got != want {
			t.Errorf("hit %d = %+v, want %+v", cell, got, want)
		}
	}
	for col, want := range []int{0, 0, 0, 0, 8, 8, 10, 11, 11, 13, 14} {
		if got := p.sourceToDisplay(col); got != want {
			t.Errorf("caret %d = %d, want %d", col, got, want)
		}
	}
}

func TestInlineProjectionTabUsesSourceColumn(t *testing.T) {
	p := mustInlineProjection(t, "a\tB", []InlineInsertion{{ByteOffset: 1, Index: 3, Label: "xx"}})
	if got := inlineRunText(p); got != "axx   B" || p.SourceWidth != 5 || p.DisplayWidth != 7 {
		t.Fatalf("text=%q source=%d display=%d", got, p.SourceWidth, p.DisplayWidth)
	}
	if p.Runs[6].DisplayStart != 6 || p.Runs[6].Cluster != "B" || p.sourceToDisplay(4) != 6 {
		t.Fatalf("B did not land at cell 6: %+v", p.Runs)
	}
	for cell := 3; cell <= 5; cell++ {
		if got := p.displayHit(cell); got != (inlineHit{SourceByte: 1, SourceCol: 1, HintIndex: -1}) {
			t.Errorf("tab cell %d: %+v", cell, got)
		}
		if got := p.Runs[cell]; got.SourceCol != cell-2 || got.AnchorCol != 1 || got.SourceByte != 1 || got.SourceEnd != 2 {
			t.Errorf("tab paint cell %d: %+v", cell, got)
		}
	}
}

func TestInlineProjectionStableGroupsAndImmutability(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		in         []InlineInsertion
		wantIDs    []int
	}{
		{"response", "aXYZb", []InlineInsertion{{1, 9, "X", false, false}, {1, 2, "YZ", false, false}}, []int{9, 2, 2}},
		{"reverse response", "aYZXb", []InlineInsertion{{1, 2, "YZ", false, false}, {1, 9, "X", false, false}}, []int{2, 2, 9}},
		{"identical labels retained", "aXXb", []InlineInsertion{{1, 9, "X", false, false}, {1, 2, "X", false, false}}, []int{9, 2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := append([]InlineInsertion(nil), tc.in...)
			p := mustInlineProjection(t, "ab", tc.in)
			if got := inlineRunText(p); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
			if p.sourceToDisplay(1) != len(tc.want)-1 {
				t.Fatalf("caret must follow entire group: %d", p.sourceToDisplay(1))
			}
			for i, id := range tc.wantIDs {
				if got := p.displayHit(i + 1); got.HintIndex != id || got.SourceByte != 1 || got.SourceCol != 1 {
					t.Errorf("group cell %d: %+v want index %d", i+1, got, id)
				}
			}
			if !reflect.DeepEqual(tc.in, before) {
				t.Fatal("caller input changed")
			}
		})
	}
	in := []InlineInsertion{{2, 12, "E", false, false}, {0, 13, "H", false, false}, {1, 14, "M", false, false}}
	before := append([]InlineInsertion(nil), in...)
	p := mustInlineProjection(t, "ab", in)
	if inlineRunText(p) != "HaMbE" || !reflect.DeepEqual(in, before) {
		t.Fatalf("sort changed source input/order: %+v, %q", in, inlineRunText(p))
	}
	in[0].Label = "changed after build"
	in[1].Index = 99
	if inlineRunText(p) != "HaMbE" || p.displayHit(0).HintIndex != 13 || p.displayHit(4).HintIndex != 12 {
		t.Fatal("built projection aliases mutable insertion records")
	}
}

func TestInlineProjectionHeadEOFAndClamps(t *testing.T) {
	p := mustInlineProjection(t, "a", []InlineInsertion{
		{ByteOffset: 1, Index: 0, Label: "E", PaddingRight: true},
		{ByteOffset: 0, Index: 7, Label: "H", PaddingLeft: true},
	})
	if got := inlineRunText(p); got != " HaE " || p.sourceToDisplay(0) != 2 || p.sourceToDisplay(1) != 5 {
		t.Fatalf("text=%q carets=%d,%d", got, p.sourceToDisplay(0), p.sourceToDisplay(1))
	}
	for _, col := range []int{-100, -1} {
		if p.sourceToDisplay(col) != 2 || p.displayHit(col) != (inlineHit{HintIndex: -1}) {
			t.Errorf("negative clamp %d", col)
		}
	}
	for _, col := range []int{5, 6, 100} {
		if p.sourceToDisplay(col) != 5 || p.displayHit(col) != (inlineHit{SourceByte: 1, SourceCol: 1, HintIndex: -1}) {
			t.Errorf("past-end clamp %d", col)
		}
	}
	if p.displayHit(1).HintIndex != 7 || p.displayHit(3).HintIndex != 0 || !p.displayHit(4).Padding {
		t.Fatal("head/EOF identities lost")
	}
	empty := mustInlineProjection(t, "", nil)
	if empty.SourceWidth != 0 || empty.DisplayWidth != 0 || empty.sourceToDisplay(10) != 0 || empty.displayHit(0).HintIndex != -1 {
		t.Fatalf("empty line: %+v", empty)
	}
	hinted := mustInlineProjection(t, "", []InlineInsertion{{Index: 8, Label: "X"}})
	if hinted.sourceToDisplay(0) != 1 || hinted.displayHit(0).HintIndex != 8 || hinted.displayHit(1).HintIndex != -1 {
		t.Fatalf("empty source with insertion: %+v", hinted)
	}
	onlyPadding := mustInlineProjection(t, "", []InlineInsertion{{Index: 4, PaddingLeft: true, PaddingRight: true}})
	if inlineRunText(onlyPadding) != "  " || !onlyPadding.displayHit(1).Padding {
		t.Fatal("empty label padding lost")
	}
}

func TestInlineProjectionStrictPlacement(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		offset    int
	}{
		{"inside combining", "e\u0301", 1},
		{"inside ZWJ", "👩‍💻", 4},
		{"inside modifier", "👍🏽", 4},
		{"inside flag", "🇺🇳", 4},
		{"orphan at head", "\u0301a", 0},
		{"after orphan", "\u0301a", 2},
		{"zero width space", "a\u200bb", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := buildInlineProjection(tc.raw, []InlineInsertion{{ByteOffset: tc.offset, Index: 1, Label: "X"}})
			if p != nil || !errors.Is(err, ErrInlinePlacement) {
				t.Fatalf("got p=%+v err=%v, want placement refusal", p, err)
			}
		})
	}
	for _, label := range []string{"\u0301", "\u200b"} {
		p, err := buildInlineProjection("a", []InlineInsertion{{ByteOffset: 1, Index: 1, Label: label}})
		if p != nil || !errors.Is(err, ErrInlinePlacement) {
			t.Errorf("zero-cell label %q: p=%+v err=%v", label, p, err)
		}
	}
	// The shared terminal profile gives a standalone ZWJ a positive fallback
	// width. It is not a zero-cell unit and must not receive a private correction.
	cluster, width := rendering.FirstGrapheme("\u200d")
	p, err := buildInlineProjection("a", []InlineInsertion{{ByteOffset: 1, Index: 1, Label: "\u200d"}})
	if width > 0 {
		if err != nil || cluster != "\u200d" || p.DisplayWidth != 1+width ||
			p.sourceToDisplay(1) != 1+width || p.displayHit(width).HintIndex != 1 {
			t.Fatalf("shared positive-width ZWJ: width=%d p=%+v err=%v", width, p, err)
		}
	} else if p != nil || !errors.Is(err, ErrInlinePlacement) {
		t.Fatalf("shared zero-cell ZWJ: p=%+v err=%v", p, err)
	}
	if p, err := buildInlineProjection("\u0301", nil); p != nil || !errors.Is(err, ErrInlinePlacement) {
		t.Fatalf("ambiguous source without insertions: p=%+v err=%v", p, err)
	}
	// A UTF-16 surrogate-half byte "offset" is not a valid UTF-8 byte boundary.
	if p, err := buildInlineProjection("😀", []InlineInsertion{{ByteOffset: 2, Index: 1, Label: "X"}}); p != nil || err == nil {
		t.Fatalf("inside emoji byte accepted: p=%+v err=%v", p, err)
	}
}

func TestSafeInlineLabelLiteralEscapes(t *testing.T) {
	raw := "A\n\r\t\x00\x07\x1b[31m\x7f\u0085\u009b\u2028\u2029\\\"界e\u0301👩‍💻"
	want := "A\\n\\r\\t\\x00\\x07\\x1b[31m\\x7f\\u0085\\u009b\\u2028\\u2029\\\"界e\u0301👩‍💻"
	if got := safeInlineLabel(raw); got != want {
		t.Fatalf("escape got %q want %q", got, want)
	}
	if got := safeInlineLabel("normal \\ path \"quote\" e\u0301👩‍💻"); got != "normal \\ path \"quote\" e\u0301👩‍💻" {
		t.Fatalf("ordinary text changed: %q", got)
	}
	if got := safeInlineLabel("\xff"); got != `\xff` {
		t.Fatalf("invalid standalone byte not escaped: %q", got)
	}
	p := mustInlineProjection(t, "s", []InlineInsertion{{ByteOffset: 1, Index: 4, Label: raw}})
	var text strings.Builder
	for _, run := range p.Runs[1:] {
		text.WriteString(run.Cluster)
		if run.Width <= 0 || !utf8.ValidString(run.Cluster) {
			t.Fatalf("invalid paint run: %+v", run)
		}
		for _, r := range run.Cluster {
			if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
				t.Fatalf("terminal effect in run %+v", run)
			}
		}
	}
	if text.String() != want {
		t.Fatalf("build bypassed escaping: %q", text.String())
	}
	// Exercise every C0, DEL, C1 and Unicode line separator, not just examples.
	for r := rune(0); r <= 0x9f; r++ {
		if r > 0x1f && r < 0x7f {
			continue
		}
		safe := safeInlineLabel(string(r))
		if strings.ContainsRune(safe, r) || rendering.VisibleWidth(safe) <= 0 {
			t.Errorf("control U+%04X not made visible: %q", r, safe)
		}
	}
}

func TestInlineProjectionLabelClustersAndClipMetadata(t *testing.T) {
	// These represent provider parts concatenated BEFORE segmentation.
	label := strings.Join([]string{"e", "\u0301", "👩", "\u200d", "💻", "界", "\t"}, "")
	p := mustInlineProjection(t, "\t界", []InlineInsertion{{ByteOffset: 1, Index: 81, Label: label}})
	if len(p.Runs) != 10 || p.DisplayWidth != 13 {
		t.Fatalf("runs=%d width=%d: %+v", len(p.Runs), p.DisplayWidth, p.Runs)
	}
	for _, tc := range []struct {
		index, start, width int
		cluster             string
	}{
		{4, 4, 1, "e\u0301"},
		{5, 5, 2, "👩‍💻"},
		{6, 7, 2, "界"},
		{7, 9, 1, `\`},
		{8, 10, 1, "t"},
		{9, 11, 2, "界"},
	} {
		run := p.Runs[tc.index]
		if run.DisplayStart != tc.start || run.Width != tc.width || run.Cluster != tc.cluster {
			t.Errorf("atomic clip metadata got %+v want %+v", run, tc)
		}
	}
	for _, cell := range []int{5, 6, 7, 8} {
		if got := p.displayHit(cell); got != (inlineHit{SourceByte: 1, SourceCol: 4, HintIndex: 81}) {
			t.Errorf("wide label continuation hit %d: %+v", cell, got)
		}
	}
	if p.displayHit(12).HintIndex != -1 || p.displayHit(12).SourceByte != 1 {
		t.Fatal("source wide continuation inherited label identity")
	}
	// An inserted leading accent must not attach to source or to a prior label.
	_, err := buildInlineProjection("a", []InlineInsertion{{1, 1, "e", false, false}, {1, 2, "\u0301", false, false}})
	if !errors.Is(err, ErrInlinePlacement) {
		t.Fatalf("independent labels were merged: %v", err)
	}
}

func TestInlineProjectionInputBounds(t *testing.T) {
	oversizedSet := make([]InlineInsertion, 1025)
	for i := range oversizedSet {
		oversizedSet[i].Index = i
	}
	aggregate := make([]InlineInsertion, 17)
	for i := range aggregate {
		aggregate[i] = InlineInsertion{Index: i, Label: strings.Repeat("x", 4096)}
	}
	for _, tc := range []struct {
		name, raw string
		in        []InlineInsertion
	}{
		{"source newline", "a\nb", nil},
		{"source CR", "a\rb", nil},
		{"source invalid UTF8", "\xff", nil},
		{"source too large", strings.Repeat("a", (2<<20)+1), nil},
		{"too many inserts", "", oversizedSet},
		{"label too large", "", []InlineInsertion{{Index: 0, Label: strings.Repeat("x", 4097)}}},
		{"aggregate labels too large", "", aggregate},
		{"label invalid UTF8", "", []InlineInsertion{{Index: 0, Label: "\xff"}}},
		{"negative index", "a", []InlineInsertion{{Index: -1}}},
		{"duplicate index", "a", []InlineInsertion{{Index: 0}, {Index: 0, Label: "X"}}},
		{"negative byte", "a", []InlineInsertion{{ByteOffset: -1, Index: 0}}},
		{"past EOF", "a", []InlineInsertion{{ByteOffset: 2, Index: 0}}},
		{"inside UTF8", "界", []InlineInsertion{{ByteOffset: 1, Index: 0}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if p, err := buildInlineProjection(tc.raw, tc.in); p != nil || err == nil {
				t.Fatalf("invalid input accepted: p=%+v err=%v", p, err)
			}
		})
	}
	// Exact insertion/label bounds are accepted, including empty labels.
	p := mustInlineProjection(t, "", oversizedSet[:1024])
	if p.DisplayWidth != 0 || p.displayHit(0).HintIndex != -1 {
		t.Fatal("empty labels invented painted identities")
	}
	p = mustInlineProjection(t, "", aggregate[:16])
	if p.DisplayWidth != 65536 || len(p.Runs) != 65536 {
		t.Fatalf("exact 64 KiB label aggregate: width=%d runs=%d", p.DisplayWidth, len(p.Runs))
	}
	// Escape expansion is bounded by bytes of INPUT, not by label display size.
	p = mustInlineProjection(t, "", []InlineInsertion{{Index: 0, Label: strings.Repeat("\x1b", 4096)}})
	if p.DisplayWidth != 16384 || len(p.Runs) != 16384 {
		t.Fatalf("control expansion: width=%d runs=%d", p.DisplayWidth, len(p.Runs))
	}
}

func TestInlineProjectionRunBound(t *testing.T) {
	p := mustInlineProjection(t, strings.Repeat("a", 131072), nil)
	if len(p.Runs) != 131072 || p.DisplayWidth != 131072 ||
		len(p.boundaries) != 131073 || p.sourceToDisplay(131072) != 131072 {
		t.Fatalf("exact independent run bound: runs=%d width=%d boundaries=%d", len(p.Runs), p.DisplayWidth, len(p.boundaries))
	}
	for _, raw := range []string{strings.Repeat("a", 131073), strings.Repeat("\t", 32769)} {
		projected, err := buildInlineProjection(raw, nil)
		if projected != nil || !errors.Is(err, ErrInlinePlacement) || !strings.Contains(err.Error(), "run bound") {
			t.Fatalf("accepted overbudget projection: source=%d p-nil=%t err=%v", len(raw), projected == nil, err)
		}
	}
}

var inlineBenchmarkProjection *inlineProjection
var inlineBenchmarkHit inlineHit
var inlineBenchmarkCol int

func TestInlineProjectionWarmLookupsAllocateNothing(t *testing.T) {
	p := mustInlineProjection(t, "\t界e\u0301😀Z", []InlineInsertion{{1, 42, ":T", true, true}})
	if p.sourceToDisplay(4) != 8 || p.displayHit(12).SourceByte != 7 || p.displayHit(5).HintIndex != 42 {
		t.Fatal("literal lookup precondition failed")
	}
	if got := testing.AllocsPerRun(1000, func() {
		inlineBenchmarkCol = p.sourceToDisplay(4)
		inlineBenchmarkHit = p.displayHit(12)
	}); got != 0 {
		t.Fatalf("warm lookup allocations=%v want 0", got)
	}
}

func BenchmarkInlineProjectionBuild(b *testing.B) {
	for _, tc := range []struct {
		name, raw string
		in        []InlineInsertion
		wantWidth int
	}{
		{"literal", "\t界e\u0301😀Z", []InlineInsertion{{1, 42, ":T", true, true}}, 14},
		{"dense line", strings.Repeat("a\t界👩‍💻", 256), []InlineInsertion{{1, 42, "e\u0301:T", true, true}}, 2053},
	} {
		b.Run(tc.name, func(b *testing.B) {
			p := mustInlineProjection(b, tc.raw, tc.in)
			if p.DisplayWidth != tc.wantWidth {
				b.Fatalf("literal width got %d want %d", p.DisplayWidth, tc.wantWidth)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				var err error
				inlineBenchmarkProjection, err = buildInlineProjection(tc.raw, tc.in)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkInlineProjectionWarmLookup(b *testing.B) {
	p := mustInlineProjection(b, strings.Repeat("a\t界👩‍💻", 256), []InlineInsertion{{1, 42, "e\u0301:T", true, true}})
	// Last source tab: bytes 4081:4082, source cols 2041..2043,
	// shifted by the five inserted cells to display cols 2046..2048.
	if p.DisplayWidth != 2053 || p.sourceToDisplay(1) != 6 || p.displayHit(2047).SourceByte != 4081 {
		b.Fatalf("literal lookup precondition failed: width=%d caret=%d hit=%+v", p.DisplayWidth, p.sourceToDisplay(1), p.displayHit(2047))
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		inlineBenchmarkCol = p.sourceToDisplay(i % 2049)
		inlineBenchmarkHit = p.displayHit(i % 2054)
	}
}

func FuzzInlineProjection(f *testing.F) {
	f.Add("\t界e\u0301😀Z", ":T", uint16(1))
	f.Add("a\tB", "\n\x1b", uint16(1))
	f.Add("", "👩‍💻", uint16(0))
	f.Add("e\u0301", "x", uint16(1))
	f.Fuzz(func(t *testing.T, raw, label string, position uint16) {
		if len(raw) > 2048 || len(label) > 256 {
			t.Skip()
		}
		offset := int(position) % (len(raw) + 1)
		ins := []InlineInsertion{{offset, 0, label, true, true}, {offset, 1, "x", false, false}}
		p, err := buildInlineProjection(raw, ins)
		if err != nil {
			if p != nil {
				t.Fatal("failed build exposed a partial projection")
			}
			return
		}
		if !utf8.ValidString(raw) || !utf8.ValidString(label) || strings.ContainsAny(raw, "\r\n") {
			t.Fatal("malformed source or label admitted")
		}
		display, consumed := 0, 0
		var recovered strings.Builder
		for _, run := range p.Runs {
			if run.DisplayStart != display || run.Width <= 0 ||
				run.SourceByte < 0 || run.SourceEnd < run.SourceByte || run.SourceEnd > len(raw) {
				t.Fatalf("invalid run: %+v", run)
			}
			display += run.Width
			for cell := run.DisplayStart; cell < display; cell++ {
				hit := p.displayHit(cell)
				if hit != (inlineHit{run.SourceByte, run.AnchorCol, run.HintIndex, run.Padding}) {
					t.Fatalf("paint/hit disagreement: %+v %+v", run, hit)
				}
			}
			if run.HintIndex >= 0 {
				if run.SourceByte != offset || run.SourceEnd != offset ||
					p.sourceToDisplay(run.AnchorCol) < display {
					t.Fatal("hint invented source or a caret inside its group")
				}
				for _, r := range run.Cluster {
					if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
						t.Fatal("hint run contains terminal control")
					}
				}
				continue
			}
			if run.SourceEnd > consumed {
				if run.SourceByte != consumed {
					t.Fatal("source unit skipped or reordered")
				}
				recovered.WriteString(raw[run.SourceByte:run.SourceEnd])
				consumed = run.SourceEnd
			}
			leading := run.DisplayStart - (run.SourceCol - run.AnchorCol)
			if p.sourceToDisplay(run.AnchorCol) != leading ||
				VisualColToByteOffset(raw, run.AnchorCol, 4) != run.SourceByte {
				t.Fatal("source caret/hit disagrees with independent source geometry")
			}
		}
		if recovered.String() != raw || consumed != len(raw) || display != p.DisplayWidth ||
			p.sourceToDisplay(p.SourceWidth) != display || p.displayHit(display).HintIndex != -1 {
			t.Fatal("projection lost source, extent or EOF affinity")
		}
	})
}
