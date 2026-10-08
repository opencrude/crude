package gitdiff

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

func lines(n int, replace map[int]string) string {
	var text strings.Builder
	for line := 1; line <= n; line++ {
		if value, ok := replace[line]; ok {
			text.WriteString(value + "\n")
			continue
		}
		fmt.Fprintf(&text, "line %d\n", line)
	}
	return text.String()
}

func comparisonItem(path, before, after string, area workspace.GitChangeArea) workspace.GitChangeComparison {
	return workspace.GitChangeComparison{
		Change: workspace.GitChange{Path: path, AbsolutePath: "/repo/" + path, Area: area, Code: "M"},
		Comparison: workspace.GitComparison{Path: path, AbsolutePath: "/repo/" + path, Area: area, Before: before, After: after,
			BeforePresent: true, AfterPresent: true, Fingerprint: "fp:" + path},
	}
}

func paint(document *Document, width, height int) string {
	document.Layout(ui.LayoutContext{}, ui.Rect{W: width, H: height})
	buf := surface.NewCellBuffer(width, height)
	document.Paint(buf, 0, 0, ui.PaintContext{})
	var text strings.Builder
	for row := 0; row < height; row++ {
		for column := 0; column < width; column++ {
			current := buf.Get(column, row)
			switch {
			case current.Content != "":
				text.WriteString(current.Content)
			case current.Char == 0:
				text.WriteByte(' ')
			case current.Width > 0:
				text.WriteRune(current.Char)
			}
		}
		text.WriteByte('\n')
	}
	return text.String()
}

func TestBlocksKeepThreeContextLinesAndMergeTouchingChanges(t *testing.T) {
	files := PrepareChanges([]workspace.GitChangeComparison{
		comparisonItem("a.txt", lines(100, nil), lines(100, map[int]string{10: "ten", 15: "fifteen", 60: "sixty"}), workspace.GitAreaWorking),
	})
	file := files[0]
	if len(file.blocks) != 2 {
		t.Fatalf("blocks=%d want 2 (10 and 15 share context, 60 stands alone)", len(file.blocks))
	}
	first := file.blocks[0]
	if first.oldStart != 6 || first.oldEnd != 18 {
		t.Fatalf("first block old range=[%d,%d) want [6,18)", first.oldStart, first.oldEnd)
	}
	if file.Added != 3 || file.Removed != 3 {
		t.Fatalf("stats +%d -%d", file.Added, file.Removed)
	}
	if file.FirstChangedLine() != 10 {
		t.Fatalf("first changed line=%d", file.FirstChangedLine())
	}
}

func TestDocumentFoldsUnchangedRunsAndExpandsOnClick(t *testing.T) {
	document := New("doc", ui.NewTokenSet(theme.DefaultDark()))
	document.SetFiles(PrepareChanges([]workspace.GitChangeComparison{
		comparisonItem("a.txt", lines(100, nil), lines(100, map[int]string{50: "fifty"}), workspace.GitAreaWorking),
	}))
	text := paint(document, 100, 30)
	for _, want := range []string{"a.txt", "+1 −1", "46 unchanged lines", "@@ −47,7 +47,7 @@", "fifty", "47 unchanged lines"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "line 20") {
		t.Fatal("unchanged line outside context was painted")
	}
	gapRow := -1
	for index, r := range document.rows {
		if r.kind == rowGap {
			gapRow = index
			break
		}
	}
	document.Handle(ui.EventContext{LocalX: 10, LocalY: gapRow}, ui.Event{Kind: ui.EventMouseClick, MouseClick: tea.MouseClickMsg{Button: tea.MouseLeft}})
	if text := paint(document, 100, 60); !strings.Contains(text, "line 20") {
		t.Fatalf("expanded gap did not reveal hidden lines:\n%s", text)
	}
}

func TestDocumentStacksFilesWithStickyHeaderAndFold(t *testing.T) {
	document := New("doc", ui.NewTokenSet(theme.DefaultDark()))
	document.SetFiles(PrepareChanges([]workspace.GitChangeComparison{
		comparisonItem("one.txt", lines(30, nil), lines(30, map[int]string{5: "a", 12: "b", 20: "c"}), workspace.GitAreaWorking),
		comparisonItem("two.txt", "x\n", "y\n", workspace.GitAreaStaged),
	}))
	if document.files[0].Key != ChangeKey(workspace.GitChange{Path: "two.txt", Area: workspace.GitAreaStaged}) {
		t.Fatal("staged files must lead the document, matching the Git panel")
	}
	document.RevealFile(document.files[1].Key)
	document.scroll.ScrollBy(3)
	text := paint(document, 100, 6)
	if firstLine := strings.SplitN(text, "\n", 2)[0]; !strings.Contains(firstLine, "one.txt") {
		t.Fatalf("scrolled body lost its sticky file header: %q", firstLine)
	}
	document.Handle(ui.EventContext{}, ui.Event{Kind: ui.EventKey, Key: tea.KeyPressMsg{Code: 'z', Text: "z"}})
	if !document.collapsed[document.files[1].Key] {
		t.Fatal("z did not fold the current file")
	}
	if text := paint(document, 100, 20); strings.Contains(text, "line 5") || !strings.Contains(text, "one.txt") {
		t.Fatalf("folded file still shows its body:\n%s", text)
	}
}

func TestDoubleClickOpensTheExactLine(t *testing.T) {
	document := New("doc", ui.NewTokenSet(theme.DefaultDark()))
	document.SetFiles(PrepareChanges([]workspace.GitChangeComparison{
		comparisonItem("a.txt", lines(20, nil), lines(20, map[int]string{10: "ten"}), workspace.GitAreaWorking),
	}))
	paint(document, 100, 30)
	target := -1
	for index, r := range document.rows {
		if r.kind == rowLine && r.lk == lineAdded {
			target = index
		}
	}
	click := ui.Event{Kind: ui.EventMouseClick, MouseClick: tea.MouseClickMsg{Button: tea.MouseLeft}}
	if result := document.Handle(ui.EventContext{LocalX: 20, LocalY: target}, click); result.Action != nil {
		t.Fatal("single click must not open the editor")
	}
	result := document.Handle(ui.EventContext{LocalX: 20, LocalY: target}, click)
	intent, ok := result.Action.Payload.(FileIntent)
	if !ok || intent.Op != OpOpen || intent.Line != 10 || intent.Change.AbsolutePath != "/repo/a.txt" {
		t.Fatalf("double click intent=%+v", result.Action)
	}
	document.lastClickAt = time.Time{}
}

func TestHeaderActionsMatchAreaAndReadOnly(t *testing.T) {
	document := New("doc", ui.NewTokenSet(theme.DefaultDark()))
	document.SetFiles(PrepareChanges([]workspace.GitChangeComparison{
		comparisonItem("w.txt", "a\n", "b\n", workspace.GitAreaWorking),
	}))
	text := paint(document, 100, 10)
	if !strings.Contains(text, "Stage") || !strings.Contains(text, "Discard") || !strings.Contains(text, "Open") {
		t.Fatalf("working header actions:\n%s", text)
	}
	actions, _ := document.headerLayout(&document.files[0], 100)
	stage := actions[0]
	result := document.Handle(ui.EventContext{LocalX: stage.x + 1, LocalY: 0}, ui.Event{Kind: ui.EventMouseClick, MouseClick: tea.MouseClickMsg{Button: tea.MouseLeft}})
	if intent, ok := result.Action.Payload.(FileIntent); !ok || intent.Op != OpStage || intent.Comparison.Fingerprint != "fp:w.txt" {
		t.Fatalf("stage click=%+v", result.Action)
	}
	document.SetReadOnly(true)
	if text := paint(document, 100, 10); strings.Contains(text, "Discard") {
		t.Fatalf("read-only document offers mutations:\n%s", text)
	}
}

func TestSplitPresentationAlignsSidesAndFallsBackWhenNarrow(t *testing.T) {
	document := New("doc", ui.NewTokenSet(theme.DefaultDark()))
	document.SetFiles(PrepareChanges([]workspace.GitChangeComparison{
		comparisonItem("a.txt", lines(10, nil), lines(10, map[int]string{5: "five"}), workspace.GitAreaWorking),
	}))
	document.SetPresentation(Split)
	text := paint(document, 140, 20)
	if document.Presented() != Split {
		t.Fatal("wide document did not split")
	}
	for _, row := range strings.Split(text, "\n") {
		if strings.Contains(row, "five") && !strings.Contains(row, "line 5") {
			t.Fatalf("split row does not pair old and new: %q", row)
		}
	}
	paint(document, 80, 20)
	if document.Presented() != Unified || document.Presentation() != Split {
		t.Fatal("narrow document must present unified while keeping the split preference")
	}
}

func twoHunkDocument(t *testing.T, area workspace.GitChangeArea) *Document {
	t.Helper()
	document := New("doc", ui.NewTokenSet(theme.DefaultDark()))
	document.SetFiles(PrepareChanges([]workspace.GitChangeComparison{
		comparisonItem("a.txt", lines(60, nil), lines(60, map[int]string{10: "ten", 40: "forty"}), area),
	}))
	paint(document, 120, 40)
	return document
}

func rowOf(t *testing.T, document *Document, text string) int {
	t.Helper()
	for index := range document.rows {
		if line, ok := document.lineText(index, sideUnified); ok && strings.TrimSpace(line) == text {
			if document.rows[index].lk != lineDeleted {
				return index
			}
		}
	}
	t.Fatalf("no row %q", text)
	return -1
}

func TestDragSelectsAndCopiesSourceTextOnly(t *testing.T) {
	document := twoHunkDocument(t, workspace.GitAreaWorking)
	start, end := rowOf(t, document, "line 8"), rowOf(t, document, "ten")
	gutter := document.unifiedGutter()
	click := ui.Event{Kind: ui.EventMouseClick, MouseClick: tea.MouseClickMsg{Button: tea.MouseLeft}}
	document.Handle(ui.EventContext{LocalX: gutter + 5, LocalY: start}, click)
	document.Handle(ui.EventContext{LocalX: gutter + 3, LocalY: end}, ui.Event{Kind: ui.EventMouseMotion})
	if got := document.SelectedText(); got != "8\nline 9\nline 10\nten" {
		t.Fatalf("selection=%q", got)
	}
	result := document.Handle(ui.EventContext{LocalX: gutter + 3, LocalY: end}, ui.Event{Kind: ui.EventMouseRelease})
	if result.Cmd == nil || document.selection.active {
		t.Fatal("release must copy and drop the highlight")
	}
}

func TestKeyboardSelectionAndCopyChord(t *testing.T) {
	document := twoHunkDocument(t, workspace.GitAreaWorking)
	document.Handle(ui.EventContext{}, ui.Event{Kind: ui.EventKey, Key: tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift}})
	document.Handle(ui.EventContext{}, ui.Event{Kind: ui.EventKey, Key: tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModShift}})
	if got := document.SelectedText(); strings.Count(got, "\n") != 1 {
		t.Fatalf("two whole lines expected, got %q", got)
	}
	result := document.Handle(ui.EventContext{}, ui.Event{Kind: ui.EventKey, Key: tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}})
	if result.Cmd == nil {
		t.Fatal("Ctrl+C did not copy the selection")
	}
	document.Handle(ui.EventContext{}, ui.Event{Kind: ui.EventKey, Key: tea.KeyPressMsg{Code: tea.KeyEsc}})
	if document.selection.active {
		t.Fatal("Esc kept the selection")
	}
}

func TestFindMatchesAcrossHunksAndSteps(t *testing.T) {
	document := twoHunkDocument(t, workspace.GitAreaWorking)
	document.SetQuery("FORTY")
	if document.SearchStatus() != "1 / 1 matches" {
		t.Fatalf("status=%q", document.SearchStatus())
	}
	document.SetQuery("line 1")
	total := len(document.search.matches)
	if total < 3 {
		t.Fatalf("matches=%d", total)
	}
	document.NextMatch(false)
	document.NextMatch(true)
	document.NextMatch(true)
	if document.search.active != total-1 {
		t.Fatalf("previous wrapped to %d, want %d", document.search.active, total-1)
	}
	document.SetQuery("nothing here")
	if !strings.HasPrefix(document.SearchStatus(), "No matches") {
		t.Fatalf("status=%q", document.SearchStatus())
	}
}

func TestHunkKeysTargetTheCurrentHunkRange(t *testing.T) {
	document := twoHunkDocument(t, workspace.GitAreaWorking)
	paint(document, 120, 8)
	document.NavigateHunk(1)
	document.NavigateHunk(1)
	result := document.Handle(ui.EventContext{}, ui.Event{Kind: ui.EventKey, Key: tea.KeyPressMsg{Code: '+', Text: "+"}})
	intent, ok := result.Action.Payload.(FileIntent)
	if !ok || intent.Op != OpStageHunk || intent.Hunk.OldStart != 36 || intent.Hunk.OldEnd != 43 {
		t.Fatalf("stage hunk intent=%+v", result.Action)
	}
	result = document.Handle(ui.EventContext{}, ui.Event{Kind: ui.EventKey, Key: tea.KeyPressMsg{Code: 'x', Text: "x"}})
	if intent := result.Action.Payload.(FileIntent); intent.Op != OpDiscardHunk {
		t.Fatalf("discard hunk intent=%+v", intent)
	}
	staged := twoHunkDocument(t, workspace.GitAreaStaged)
	result = staged.Handle(ui.EventContext{}, ui.Event{Kind: ui.EventKey, Key: tea.KeyPressMsg{Code: '+', Text: "+"}})
	if intent := result.Action.Payload.(FileIntent); intent.Op != OpUnstageHunk {
		t.Fatalf("staged + must unstage the hunk, got %s", intent.Op)
	}
	if result := staged.Handle(ui.EventContext{}, ui.Event{Kind: ui.EventKey, Key: tea.KeyPressMsg{Code: 'x', Text: "x"}}); result.Action != nil {
		t.Fatal("staged hunks cannot be discarded from the working tree")
	}
	if text := paint(document, 120, 8); !strings.Contains(text, "Stage hunk") || !strings.Contains(text, "Discard hunk") {
		t.Fatalf("hunk header actions missing:\n%s", text)
	}
}

func TestUnchangedRereadKeepsSelection(t *testing.T) {
	document := twoHunkDocument(t, workspace.GitAreaWorking)
	document.selectCurrentFile()
	document.SetFiles(PrepareChanges([]workspace.GitChangeComparison{
		comparisonItem("a.txt", lines(60, nil), lines(60, map[int]string{10: "ten", 40: "forty"}), workspace.GitAreaWorking),
	}))
	if !document.selection.active {
		t.Fatal("an identical re-read cleared the selection")
	}
}
