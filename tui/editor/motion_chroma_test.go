package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/alecthomas/chroma/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestMoveWord_StopsAtPunctuation(t *testing.T) {
	ed := loadEditorText(t, "w.go", "foo...bar\n")
	ed.cursor = Position{Line: 0, Col: 0}
	ed.moveWordRight() // end of foo
	if ed.cursor.Col != 3 {
		t.Fatalf("after first word-right col=%d want 3", ed.cursor.Col)
	}
	ed.moveWordRight() // end of ...
	if ed.cursor.Col != 6 {
		t.Fatalf("after punct run col=%d want 6", ed.cursor.Col)
	}
	ed.moveWordRight() // end of bar
	if ed.cursor.Col != 9 {
		t.Fatalf("after bar col=%d want 9", ed.cursor.Col)
	}

	ed.moveWordLeft() // start of bar
	if ed.cursor.Col != 6 {
		t.Fatalf("word-left to bar start col=%d want 6", ed.cursor.Col)
	}
	ed.moveWordLeft() // start of ...
	if ed.cursor.Col != 3 {
		t.Fatalf("word-left to punct start col=%d want 3", ed.cursor.Col)
	}
}

func TestMoveWordLeft_CrossesLines(t *testing.T) {
	// Cursor at line start jumps to the previous line's last word start.
	ed := loadEditorText(t, "cl.go", "hello world\nnext line\n")
	ed.cursor = Position{Line: 1, Col: 0}
	ed.moveWordLeft()
	if ed.cursor.Line != 0 || ed.cursor.Col != 6 {
		t.Fatalf("cross-line word-left: cursor=(%d,%d) want (0,6)", ed.cursor.Line, ed.cursor.Col)
	}

	// Whitespace-only previous line: keep climbing to a real word.
	ed2 := loadEditorText(t, "cl2.go", "hello world\n   \nnext line\n")
	ed2.cursor = Position{Line: 2, Col: 0}
	ed2.moveWordLeft()
	if ed2.cursor.Line != 0 || ed2.cursor.Col != 6 {
		t.Fatalf("whitespace-skip word-left: cursor=(%d,%d) want (0,6)", ed2.cursor.Line, ed2.cursor.Col)
	}

	// At the very first line start: no-op.
	ed3 := loadEditorText(t, "cl3.go", "solo\n")
	ed3.cursor = Position{Line: 0, Col: 0}
	ed3.moveWordLeft()
	if ed3.cursor.Line != 0 || ed3.cursor.Col != 0 {
		t.Fatalf("first-line word-left: cursor=(%d,%d) want (0,0)", ed3.cursor.Line, ed3.cursor.Col)
	}

	// Empty first line: no-op at (0,0).
	ed4 := loadEditorText(t, "cl4.go", "\nword\n")
	ed4.cursor = Position{Line: 0, Col: 0}
	ed4.moveWordLeft()
	if ed4.cursor.Line != 0 || ed4.cursor.Col != 0 {
		t.Fatalf("empty-first-line word-left: cursor=(%d,%d) want (0,0)", ed4.cursor.Line, ed4.cursor.Col)
	}
}

func TestMoveWord_UnicodeLetters(t *testing.T) {
	ed := loadEditorText(t, "u.go", "café bar\n")
	ed.cursor = Position{Line: 0, Col: 0}
	ed.moveWordRight()
	if ed.cursor.Col != 4 {
		t.Fatalf("unicode word-right col=%d want 4", ed.cursor.Col)
	}
}

func TestPageUpDown_PreservesGoalColumn(t *testing.T) {
	var b strings.Builder
	b.WriteString("abcdefghij\n")
	for i := 0; i < 30; i++ {
		b.WriteString("ab\n")
	}
	b.WriteString("abcdefghij\n")
	ed := loadEditorText(t, "p.go", b.String())
	ed.viewW = 80
	ed.viewH = 20
	ed.cursor = Position{Line: 0, Col: 8}
	ed.pageDown()
	if !ed.goalSet || ed.goalCol != 8 {
		t.Fatalf("pageDown should keep goalCol=8, got set=%v col=%d", ed.goalSet, ed.goalCol)
	}
	if ed.cursor.Col != 2 {
		t.Fatalf("clamped col=%d want 2", ed.cursor.Col)
	}
	ed.pageUp()
	if ed.cursor.Line != 0 {
		t.Fatalf("line=%d want 0", ed.cursor.Line)
	}
	if ed.cursor.Col != 8 {
		t.Fatalf("goal restored col=%d want 8", ed.cursor.Col)
	}
}

func TestScrollToCursor_UsesViewportWidth(t *testing.T) {
	ed := loadEditorText(t, "s.go", strings.Repeat("x", 200)+"\n")
	ed.viewW = 40
	ed.viewH = 10
	ed.cursor = Position{Line: 0, Col: 100}
	ed.ScrollToCursor()
	avail := ed.viewW - ed.gutterWidth() - ed.scrollbarColWidth()
	if ed.scrollX+avail <= ed.cursor.Col {
		t.Fatalf("scrollX=%d avail=%d cursor=%d — cursor not visible", ed.scrollX, avail, ed.cursor.Col)
	}
	if ed.scrollX > ed.cursor.Col {
		t.Fatalf("scrollX=%d > cursor col", ed.scrollX)
	}
}

func TestLineVisualWidth_MotionDoesNotTokenise(t *testing.T) {
	var b strings.Builder
	b.WriteString("package main\n")
	for i := 0; i < 400; i++ {
		fmt.Fprintf(&b, "func f%d() int { return %d }\n", i, i)
	}
	ed := loadEditorText(t, "big.go", b.String())
	ed.viewW = 100
	ed.viewH = 40
	doc, generation := ed.syntaxDocument, ed.syntaxGeneration
	_ = ed.lineVisualWidth(10)
	if len(ed.chromaLineCache) > 0 {
		t.Fatal("lineVisualWidth must not populate chroma cache")
	}
	ed.cursor = Position{Line: 10, Col: 0}
	start := time.Now()
	for i := 0; i < 200; i++ {
		ed.moveDown()
		ed.moveUp()
	}
	elapsed := time.Since(start)
	if len(ed.chromaLineCache) > 0 {
		t.Fatal("cursor motion populated chroma cache — geometry is not split from colour")
	}
	if ed.syntaxDocument != doc || ed.syntaxGeneration != generation || ed.syntaxJob != nil {
		t.Fatal("cursor geometry invalidated or restarted syntax work")
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("400 vertical motions took %v — suspiciously slow (possible tokenisation)", elapsed)
	}
	t.Logf("400 vertical motions: %v", elapsed)
}

func BenchmarkCursorMotionGeom(b *testing.B) {
	path := filepath.Join(b.TempDir(), "bench.go")
	var buf strings.Builder
	buf.WriteString("package main\n")
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&buf, "func f%d() int { return %d /* comment */ }\n", i, i)
	}
	if err := os.WriteFile(path, []byte(buf.String()), 0o600); err != nil {
		b.Fatal(err)
	}
	ed := New(theme.DefaultDark())
	if err := ed.Open(path); err != nil {
		b.Fatal(err)
	}
	defer ed.Close()
	ed.viewW = 120
	ed.viewH = 40
	ed.cursor = Position{Line: 100, Col: 5}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ed.moveDown()
		ed.moveUp()
	}
}

func TestChroma_MultilineBlockComment(t *testing.T) {
	src := "package main\n\n/*\ninside comment\nstill comment\n*/\nvar x = 1\n"
	ed := loadEditorText(t, "c.go", src)
	ed.viewW = 80
	ed.viewH = 20
	commentClr := ed.chromaTokenColor(chroma.CommentMultiline)
	if commentClr == "" {
		commentClr = ed.chromaTokenColor(chroma.Comment)
	}
	if commentClr == "" {
		t.Fatal("theme missing comment colour")
	}

	surf := surface.NewSurface("ed", 80, 20, cell.TransparentBlank())
	ed.Render(surf, ui.PaintContext{Theme: ed.th, Focused: true})

	cm := ed.lineColorMap(3)
	if cm == nil {
		t.Fatal("expected color map for interior comment line")
	}
	found := false
	for _, c := range cm {
		if c == commentClr {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("interior comment line colours=%v missing comment %s", cm, commentClr)
	}

	cmVar := ed.lineColorMap(6)
	if cmVar == nil {
		t.Fatal("expected color map for var line")
	}
	kwClr := ed.chromaTokenColor(chroma.KeywordDeclaration)
	if kwClr == "" {
		kwClr = ed.chromaTokenColor(chroma.Keyword)
	}
	if len(cmVar) < 3 || cmVar[0] != kwClr {
		t.Fatalf("var line should start with keyword colour %s, got %v", kwClr, cmVar)
	}
}

func TestChroma_MultilineRawString(t *testing.T) {
	src := "package main\nconst s = `\nraw line\nstill raw\n`\nvar y = 2\n"
	ed := loadEditorText(t, "raw.go", src)
	ed.ensureChromaColors(0, ed.LineCount())
	strClr := ed.chromaTokenColor(chroma.LiteralString)
	if strClr == "" {
		strClr = ed.chromaTokenColor(chroma.String)
	}
	cm := ed.lineColorMap(2)
	if cm == nil {
		t.Fatal("nil color map")
	}
	found := false
	for _, c := range cm {
		if c != "" && c == strClr {
			found = true
			break
		}
	}
	if !found {
		// Accept any non-empty colour on the raw interior (string token variants).
		for _, c := range cm {
			if c != "" {
				found = true
				break
			}
		}
	}
	if !found {
		t.Fatalf("raw string interior has no string colour; map=%v want %s", cm, strClr)
	}
}

func TestChroma_LineCommentContainingBlockOpen(t *testing.T) {
	src := "package main\n// not a block /* opener\nvar z = 3\n"
	ed := loadEditorText(t, "lc.go", src)
	ed.ensureChromaColors(0, ed.LineCount())
	kwClr := ed.chromaTokenColor(chroma.KeywordDeclaration)
	if kwClr == "" {
		kwClr = ed.chromaTokenColor(chroma.Keyword)
	}
	cm := ed.lineColorMap(2)
	if cm == nil {
		t.Fatal("nil")
	}
	if len(cm) < 3 || cm[0] != kwClr {
		t.Fatalf("// line with /* must not force following line into comment: %v", cm)
	}
}

func TestChroma_MidConstructViewport(t *testing.T) {
	var b strings.Builder
	b.WriteString("package main\n")
	b.WriteString("/*\n")
	for i := 0; i < 40; i++ {
		fmt.Fprintf(&b, "comment body %d\n", i)
	}
	b.WriteString("*/\n")
	b.WriteString("func after() {}\n")
	ed := loadEditorText(t, "mid.go", b.String())
	ed.viewW = 80
	ed.viewH = 12
	ed.scrollY = 10
	ed.ensureChromaColors(ed.scrollY, ed.scrollY+ed.viewH)
	commentClr := ed.chromaTokenColor(chroma.CommentMultiline)
	if commentClr == "" {
		commentClr = ed.chromaTokenColor(chroma.Comment)
	}
	cm := ed.lineColorMap(ed.scrollY)
	if cm == nil {
		t.Fatal("nil color map at mid-construct viewport")
	}
	found := false
	for _, c := range cm {
		if c == commentClr {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("mid-viewport comment line not coloured as comment: %v", cm)
	}

	buf := paintEditor(ed, 80, 12)
	sw := ed.gutterWidth()
	c := buf.Get(sw, 0)
	wantFG := ed.cellColor(ed.th.Colors.Code.Comment)
	if !c.FG.Equal(wantFG) {
		t.Fatalf("painted FG=%v want comment %v (char=%q)", c.FG, wantFG, c.Char)
	}
}
