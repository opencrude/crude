package bidi

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"testing"

	gt "github.com/go-text/typesetting/bidi"
)

func readConformance(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile("../../third_party/typesetting/bidi/test/" + name + ".gz")
	if err != nil {
		t.Fatal(err)
	}
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if _, err := out.ReadFrom(r); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// visualOrder arranges code points as units: characters rule X9 removes
// (resolved level "x") are zero-width, so they take no place on the line,
// exactly as the conformance file excludes them from its ordering.
func visualOrder(runes []rune, removed []bool, dir Direction) []int {
	units := make([]Unit, len(runes))
	for i, r := range runes {
		units[i] = Unit{Text: string(r), Width: 1}
		if removed[i] {
			units[i].Width = 0
		}
	}
	l := forceAnalyze(units, dir)
	width := l.Width()
	var order []int
	for _, b := range l.Line(0, width, width).Blocks() {
		order = append(order, b.Unit)
	}
	return order
}

// forceAnalyze resolves even all-left-to-right text, so conformance cases
// that need no arrangement are checked too.
func forceAnalyze(units []Unit, dir Direction) *Layout {
	a := new(analyzer)
	l := a.analyze(units, dir, false, false)
	if l == nil {
		// Trivial: identity order at the resolved level.
		n := len(units)
		l = &Layout{level: make([]gt.Level, n), class: make([]gt.Class, n), col: make([]int32, n+1), source: make([]string, n), display: make([]string, n)}
		col := int32(0)
		for i, u := range units {
			l.col[i] = col
			col += int32(u.Width)
			l.class[i] = gt.LookupClass([]rune(u.Text)[0])
		}
		l.col[n] = col
	}
	return l
}

func TestConformanceBidiCharacterTestOrdering(t *testing.T) {
	scanner := bufio.NewScanner(bytes.NewReader(readConformance(t, "BidiCharacterTest.txt")))
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	cases := 0
	for line := 0; scanner.Scan(); line++ {
		text := strings.TrimSpace(scanner.Text())
		if text == "" || text[0] == '#' {
			continue
		}
		fields := strings.Split(text, ";")
		var runes []rune
		for _, f := range strings.Fields(fields[0]) {
			v, _ := strconv.ParseInt(f, 16, 32)
			runes = append(runes, rune(v))
		}
		dir := Auto
		switch fields[1] {
		case "0":
			dir = LeftToRight
		case "1":
			dir = RightToLeft
		}
		levels := strings.Fields(fields[3])
		removed := make([]bool, len(runes))
		for i, lv := range levels {
			removed[i] = lv == "x"
		}
		var want []int
		for _, f := range strings.Fields(fields[4]) {
			v, _ := strconv.Atoi(f)
			want = append(want, v)
		}
		got := visualOrder(runes, removed, dir)
		if !equalInts(got, want) {
			t.Fatalf("line %d %q: visual order %v, want %v", line+1, fields[0], got, want)
		}
		cases++
	}
	if cases < 90000 {
		t.Fatalf("ran %d cases; the conformance file did not load", cases)
	}
}

var runeForClass = map[string]rune{
	"L": 'a', "R": 'א', "EN": '0', "ES": '+', "ET": '#', "AN": '\u0661', "CS": ',',
	"B": '\u2029', "S": '\t', "WS": ' ', "ON": '!', "BN": '\u00ad', "NSM": '\u0300',
	"AL": '\u0608', "LRO": '\u202d', "RLO": '\u202e', "LRE": '\u202a', "RLE": '\u202b',
	"PDF": '\u202c', "LRI": '\u2066', "RLI": '\u2067', "FSI": '\u2068', "PDI": '\u2069',
}

func TestConformanceBidiTestOrdering(t *testing.T) {
	scanner := bufio.NewScanner(bytes.NewReader(readConformance(t, "BidiTest.txt")))
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	var levels []string
	var reorder []int
	cases := 0
	for scanner.Scan() {
		text := strings.TrimSpace(scanner.Text())
		switch {
		case text == "" || text[0] == '#':
			continue
		case strings.HasPrefix(text, "@Levels:"):
			levels = strings.Fields(strings.TrimPrefix(text, "@Levels:"))
			continue
		case strings.HasPrefix(text, "@Reorder:"):
			reorder = reorder[:0]
			for _, f := range strings.Fields(strings.TrimPrefix(text, "@Reorder:")) {
				v, _ := strconv.Atoi(f)
				reorder = append(reorder, v)
			}
			continue
		}
		fields := strings.Split(text, ";")
		classes := strings.Fields(fields[0])
		// A paragraph separator ends the paragraph in this file's model;
		// Crude never passes one inside a display line.
		if strings.Contains(fields[0], "B") {
			continue
		}
		bitset, _ := strconv.Atoi(strings.TrimSpace(fields[1]))
		runes := make([]rune, len(classes))
		removed := make([]bool, len(classes))
		for i, c := range classes {
			runes[i] = runeForClass[c]
			removed[i] = levels[i] == "x"
		}
		for bit, dir := range []Direction{Auto, LeftToRight, RightToLeft} {
			if bitset&(1<<bit) == 0 {
				continue
			}
			got := visualOrder(runes, removed, dir)
			if !equalInts(got, reorder) {
				t.Fatalf("%s dir %d: visual order %v, want %v", fields[0], dir, got, reorder)
			}
			cases++
		}
	}
	if cases < 100000 {
		t.Fatalf("ran %d cases; the conformance file did not load", cases)
	}
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestTriggerBlocksCoverUnicode(t *testing.T) {
	for r := rune(0); r <= 0x10FFFF; r++ {
		switch gt.LookupClass(r) {
		case gt.R, gt.AL, gt.AN, gt.RLE, gt.RLO, gt.RLI, gt.FSI:
			if !mayTrigger(r) {
				t.Fatalf("%U can reorder text but lies outside the trigger blocks", r)
			}
		}
	}
}

// screen paints a paragraph on a line the way a terminal shows it: the
// arranged glyph of each block in visual order, padding as spaces.
func screen(t *testing.T, s string, dir Direction, span int) string {
	t.Helper()
	l := AnalyzeString(s, dir, false)
	if l == nil {
		return s + strings.Repeat(" ", max(0, span-len([]rune(s))))
	}
	var out strings.Builder
	for _, b := range l.Line(0, l.Width(), span).Blocks() {
		switch {
		case b.Unit < 0:
			out.WriteByte(' ')
		case b.Display != "":
			out.WriteString(b.Display)
		default:
			out.WriteString(b.Source)
		}
	}
	return out.String()
}

func TestPersianArrangesJoinsAndAligns(t *testing.T) {
	// سلام دنیا: letters reversed and joined, right-aligned on its line.
	got := screen(t, "سلام دنیا", Auto, 12)
	want := "   " + "\ufe8e\ufbff\ufee7\ufea9" + " " + "\ufee1\ufe8e\ufee0\ufeb3"
	if got != want {
		t.Fatalf("screen = %q, want %q", got, want)
	}
}

func TestMixedLineKeepsLeftToRightBase(t *testing.T) {
	// An LTR line reverses only the Hebrew run, in place.
	if got := screen(t, "abc אבג def", LeftToRight, 11); got != "abc גבא def" {
		t.Fatalf("screen = %q", got)
	}
	// Numbers inside right-to-left text keep their own order.
	if got := screen(t, "אבג 123 דה", Auto, 10); got != "הד 123 גבא" {
		t.Fatalf("screen = %q", got)
	}
}

func TestBracketsMirrorAtOddLevels(t *testing.T) {
	// "(א)" in a right-to-left paragraph reads as a parenthesized letter.
	if got := screen(t, "(א) b", RightToLeft, 5); got != "b (א)" {
		t.Fatalf("screen = %q", got)
	}
}

func TestLeftToRightTextNeedsNoLayout(t *testing.T) {
	if AnalyzeString("plain ASCII · ✓ 日本語 😀", Auto, false) != nil {
		t.Fatal("left-to-right text produced a layout")
	}
	if NeedsLayout("hello ۱۲۳") {
		t.Fatal("Persian digits alone are European numbers and need no layout")
	}
	if !NeedsLayout("x\u202ey") || !NeedsLayout("שלום") || !NeedsLayout("١") {
		t.Fatal("an RLO, Hebrew or an Arabic number must need layout")
	}
}

func TestPolicyOffKeepsLogicalOrder(t *testing.T) {
	defer SetPresentation(Current())
	SetPresentation(Presentation{Arrange: false, Join: true})
	if AnalyzeString("سلام", Auto, false) != nil {
		t.Fatal("arrangement ran with Arrange off")
	}
	SetPresentation(Presentation{Arrange: true, Join: false})
	if got := screen(t, "سلام", Auto, 4); got != "مالس" {
		t.Fatalf("unjoined arrangement = %q", got)
	}
}

func TestEmptyRightToLeftParagraphPutsEndAtRight(t *testing.T) {
	l := Analyze(nil, Auto, true)
	ln := l.Line(0, 0, 6)
	if ln.Visual(0) != 5 {
		t.Fatalf("the end of an empty right-to-left line is at column %d, want 5", ln.Visual(0))
	}
}

func TestWindowAndColumnMaps(t *testing.T) {
	l := AnalyzeString("ab אבג cd", LeftToRight, false)
	ln := l.Line(0, l.Width(), 12)
	// Logical: a b _ א ב ג _ c d ; visual: a b _ ג ב א _ c d
	for logical, visual := range map[int]int{0: 0, 3: 5, 5: 3, 7: 7, 9: 9, 11: 11} {
		if got := ln.Visual(logical); got != visual {
			t.Errorf("Visual(%d) = %d, want %d", logical, got, visual)
		}
		if got := ln.Logical(visual); got != logical {
			t.Errorf("Logical(%d) = %d, want %d", visual, got, logical)
		}
	}
	// A scrolled window is a display line of the same paragraph.
	w := l.Line(3, 6, 3)
	if w.Visual(0) != 2 || w.Visual(2) != 0 {
		t.Fatalf("windowed Hebrew run not reversed: %v", w.toVis)
	}
}

func TestWideClustersMoveWhole(t *testing.T) {
	// A two-column cluster inside right-to-left text keeps its own columns.
	units := []Unit{{Text: "א", Width: 1}, {Text: "日", Width: 2}, {Text: "ב", Width: 1}}
	l := Analyze(units, RightToLeft, false)
	blocks := l.Line(0, 4, 4).Blocks()
	if blocks[1].Unit != 1 || blocks[1].Visual != 1 || blocks[1].Width != 2 {
		t.Fatalf("wide unit block = %+v", blocks[1])
	}
}

func TestObjectsMoveAsOneBlock(t *testing.T) {
	units := []Unit{{Text: "א", Width: 1}, {Text: ": int", Width: 5, Object: true}, {Text: "ב", Width: 1}}
	l := Analyze(units, Auto, false)
	blocks := l.Line(0, 7, 7).Blocks()
	if len(blocks) != 3 || blocks[1].Unit != 1 || blocks[1].Width != 5 {
		t.Fatalf("object blocks = %+v", blocks)
	}
	if blocks[0].Unit != 2 || blocks[2].Unit != 0 {
		t.Fatalf("object did not stay between its neighbours in RTL order: %+v", blocks)
	}
}

func TestStepMovesOnScreen(t *testing.T) {
	// LTR line "ab אב": visual a b _ ב א [pad]
	l := AnalyzeString("ab אב", LeftToRight, false)
	ln := l.Line(0, 5, 7)
	steps := []struct {
		from  int
		right bool
		want  int
		ok    bool
	}{
		{0, true, 1, true},   // a -> b
		{2, true, 4, true},   // space -> ב (logical 4, visual 3)
		{4, true, 3, true},   // ב -> א
		{3, true, 5, true},   // א -> end of text
		{5, true, 5, false},  // end: the edge continues in text order
		{4, false, 2, true},  // ב -> space
		{0, false, 0, false}, // left edge
	}
	for _, s := range steps {
		got, ok := ln.Step(s.from, s.right)
		if got != s.want || ok != s.ok {
			t.Errorf("Step(%d, right=%v) = %d, %v; want %d, %v", s.from, s.right, got, ok, s.want, s.ok)
		}
	}
}

func TestLogicalOrderInvertsLineOrder(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	alphabet := []rune{'a', 'b', 'א', 'ב', 'ب', '1', '٣', ' ', '(', ')', '\u202b', '\u202a', '\u202c', '\u2067', '\u2069', '.'}
	for trial := 0; trial < 3000; trial++ {
		n := 1 + rng.Intn(14)
		units := make([]Unit, n)
		for i := range units {
			units[i] = Unit{Text: string(alphabet[rng.Intn(len(alphabet))]), Width: 1}
		}
		dir := []Direction{Auto, LeftToRight, RightToLeft}[rng.Intn(3)]
		l := forceAnalyze(units, dir)
		blocks := l.Line(0, l.Width(), l.Width()+rng.Intn(3)).Blocks()
		levels := make([]int8, len(blocks))
		for i, b := range blocks {
			levels[i] = int8(b.Level)
		}
		order := LogicalOrder(levels, nil)
		for i, v := range order {
			if blocks[v].Logical != i {
				t.Fatalf("trial %d: LogicalOrder put block %d at %d", trial, blocks[v].Logical, i)
			}
		}
	}
}

func TestLogicalUndoesGlyphs(t *testing.T) {
	if Logical('\ufeb3', 1, true) != 'س' || Logical(')', 1, false) != '(' || Logical(')', 0, false) != ')' {
		t.Fatal("Logical did not restore the source character")
	}
}
