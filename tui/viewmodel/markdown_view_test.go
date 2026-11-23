package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/filetree"
	"crude/tui/screens/markdownview"
	"crude/tui/ui"
)

type markdownSink chan any

func (s markdownSink) Send(msg any) { s <- msg }

// openMarkdownFile opens a Markdown file from the tree and waits for its first rendering.
func openMarkdownFile(t *testing.T, app *App, root, name, text string) (*markdownview.Screen, markdownSink) {
	t.Helper()
	md, sink := startMarkdownFile(t, app, root, name, text)
	pumpMarkdown(t, app, sink, func() bool { return md.Document() != nil })
	return md, sink
}

func startMarkdownFile(t *testing.T, app *App, root, name, text string) (*markdownview.Screen, markdownSink) {
	t.Helper()
	sink := make(markdownSink, 64)
	app.eventBus.SetSink(sink)
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	_, command := app.Update(filetree.FileSelectedMsg{Path: path})
	runAppCommand(t, app, command)
	_ = app.View()
	md := app.activeMarkdown()
	if md == nil {
		t.Fatalf("%s did not open as a Markdown tab", name)
	}
	return md, sink
}

// pumpMarkdown delivers finished renderings through Update until done.
func pumpMarkdown(t *testing.T, app *App, sink markdownSink, done func() bool) {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for !done() {
		select {
		case msg := <-sink:
			_, cmd := app.Update(msg)
			runAppCommand(t, app, cmd)
			_ = app.View()
		case <-deadline:
			t.Fatal("the Markdown rendering did not arrive")
		}
	}
}

func screenText(app *App, r ui.Rect) string { return paneJourneyText(app, r) }

func markdownFixture(sections int) string {
	var b strings.Builder
	b.WriteString("# Guide\n\nIntro paragraph with **bold** text.\n\n")
	for i := range sections {
		fmt.Fprintf(&b, "## Section %d\n\nBody %d says hello.\n\n- item %d\n\n", i, i, i)
	}
	return b.String()
}

// A Markdown file opens as a rendered document: no raw markup, a mode strip,
// and keys that scroll the document instead of editing its hidden source.
func TestMarkdownFileOpensRenderedAndReadOnly(t *testing.T) {
	app, ws := newRealShellApp(t)
	paneJourneySize(app, 120, 32)
	md, sink := startMarkdownFile(t, app, ws.RootPath, "guide.md", markdownFixture(40))
	ed := app.EditorState.Editor
	revision := ed.Revision()
	paneJourneyKey(app, 'x', 0)
	app.Update(tea.PasteMsg{Content: "hidden edit"})
	if md.Document() != nil || ed.Revision() != revision || ed.Modified() {
		t.Fatal("input modified the hidden source before the first preview arrived")
	}
	pumpMarkdown(t, app, sink, func() bool { return md.Document() != nil })
	if md.Mode() != markdownview.Preview {
		t.Fatalf("a Markdown file opened in %q, want the preview", md.Mode())
	}
	_ = app.View()
	preview := app.mainScreenRect(md.PreviewRect())
	text := screenText(app, preview)
	if !strings.Contains(text, "Guide") || strings.Contains(text, "# Guide") || strings.Contains(text, "**bold**") {
		t.Fatalf("the preview shows raw Markdown:\n%s", text)
	}
	for i, label := range []string{"Preview", "Side by side", "Source"} {
		mode := []markdownview.Mode{markdownview.Preview, markdownview.Split, markdownview.Source}[i]
		button := app.rootHost.Component(ui.NewID(ui.NewID(md.ID(), "toolbar"), string(mode)))
		if button == nil || button.Semantics().Label != label || button.Semantics().Selected != (i == 0) {
			t.Fatalf("toolbar %s does not expose the selected state", label)
		}
	}
	if !md.SourceRect().Empty() {
		t.Fatal("the source editor is on screen in the preview")
	}
	paneJourneyKey(app, 'x', 0)
	paneJourneyKey(app, tea.KeyBackspace, 0)
	if ed.Revision() != revision || ed.Modified() {
		t.Fatal("a key in the preview edited the hidden source")
	}
	before := md.Preview().ScrollModel().Offset()
	paneJourneyKey(app, tea.KeyPgDown, 0)
	if md.Preview().ScrollModel().Offset() <= before {
		t.Fatal("Page Down did not scroll the rendered document")
	}
	paneJourneyKey(app, tea.KeyHome, 0)
	if md.Preview().ScrollModel().Offset() != 0 {
		t.Fatal("Home did not return to the top")
	}
}

// Alt+V moves through the three views; side by side shows the editor and the
// preview, edits appear in the preview, and both sides keep the same part of
// the file in view.
func TestMarkdownSideBySideFollowsEditsAndScrolling(t *testing.T) {
	app, ws := newRealShellApp(t)
	paneJourneySize(app, 140, 32)
	md, sink := openMarkdownFile(t, app, ws.RootPath, "notes.md", markdownFixture(60))
	paneJourneyKey(app, 'v', tea.ModAlt)
	if md.Mode() != markdownview.Split {
		t.Fatalf("Alt+V from the preview showed %q, want side by side", md.Mode())
	}
	if md.SourceRect().Empty() || md.PreviewRect().Empty() {
		t.Fatal("side by side does not show both the source and the preview")
	}
	ed := app.EditorState.Editor
	ed.ScrollToLine(120)
	_ = app.View()
	doc := md.Document()
	want := int(doc.RowForLine(ed.TopLine()) + .5)
	if got := md.Preview().ScrollModel().Offset(); got < want-1 || got > want+1 {
		t.Fatalf("the preview is at row %d; the editor's top line maps to row %d", got, want)
	}
	// The preview leads when the user scrolls it.
	md.Preview().ScrollModel().ScrollTo(10)
	_ = app.View()
	if want, line := doc.LineForRow(10), ed.TopLine(); line < want-1 || line > want+1 {
		t.Fatalf("scrolling the preview to row 10 put the editor at line %g, want %g", line, want)
	}
	// Typing in the source shows in the preview without blanking it.
	ed.ScrollToLine(0)
	_ = app.View()
	for _, r := range "Fresh words" {
		paneJourneyKey(app, r, 0)
	}
	if md.Document() == nil {
		t.Fatal("the preview went blank during an edit")
	}
	pumpMarkdown(t, app, sink, func() bool { return md.Document().Revision == ed.Revision() })
	_ = app.View()
	if text := screenText(app, app.mainScreenRect(md.PreviewRect())); !strings.Contains(text, "Fresh words") {
		t.Fatalf("the edit did not reach the preview:\n%s", text)
	}
	paneJourneyKey(app, 'v', tea.ModAlt)
	if md.Mode() != markdownview.Source || !md.PreviewRect().Empty() || md.SourceRect().Empty() {
		t.Fatalf("Alt+V from side by side showed %q", md.Mode())
	}
	paneJourneyKey(app, 'v', tea.ModAlt)
	if md.Mode() != markdownview.Preview {
		t.Fatalf("Alt+V from the source showed %q", md.Mode())
	}
}

// Leaving the preview keeps the reading position: the source opens at the
// part of the file that was on screen, and the preview returns to it.
func TestMarkdownModeSwitchKeepsThePosition(t *testing.T) {
	app, ws := newRealShellApp(t)
	paneJourneySize(app, 120, 30)
	md, _ := openMarkdownFile(t, app, ws.RootPath, "long.md", markdownFixture(80))
	md.Preview().ScrollModel().ScrollTo(200)
	_ = app.View()
	line := md.Document().LineForRow(float64(md.Preview().ScrollModel().Offset()))
	if !app.setMarkdownMode(markdownview.Source) {
		t.Fatal("the Markdown tab did not change mode")
	}
	_ = app.View()
	if got := app.EditorState.Editor.TopLine(); got < line-2 || got > line+2 {
		t.Fatalf("the source opened at line %g; the preview showed line %g", got, line)
	}
	app.setMarkdownMode(markdownview.Preview)
	_ = app.View()
	if got := md.Preview().ScrollModel().Offset(); got < 195 || got > 205 {
		t.Fatalf("the preview returned to row %d, want about 200", got)
	}
}

// Ctrl+F in the preview searches the rendered document with the shared find
// window, named after the file.
func TestMarkdownPreviewFind(t *testing.T) {
	app, ws := newRealShellApp(t)
	paneJourneySize(app, 120, 30)
	md, _ := openMarkdownFile(t, app, ws.RootPath, "find.md", markdownFixture(30))
	paneJourneyKey(app, 'f', tea.ModCtrl)
	if app.findChat != md.Preview() || app.findChatTitle != "find.md" {
		t.Fatalf("Ctrl+F did not search the preview: %v %q", app.findChat == md.Preview(), app.findChatTitle)
	}
	app.closeFindReplace()
}

// Clicking a toolbar icon switches the current document view.
func TestMarkdownToolbarClickSwitchesTheView(t *testing.T) {
	app, ws := newRealShellApp(t)
	paneJourneySize(app, 120, 30)
	md, _ := openMarkdownFile(t, app, ws.RootPath, "click.md", markdownFixture(5))
	r, ok := app.rootHost.ScreenRect(ui.NewID(ui.NewID(md.ID(), "toolbar"), string(markdownview.Source)))
	if !ok || r.Empty() || r.X+r.W != app.mainScreenRect(md.Bounds()).X+md.Bounds().W-1 {
		t.Fatalf("source icon not mounted at the right edge: %+v", r)
	}
	app.Update(tea.MouseClickMsg{X: r.X + r.W/2, Y: r.Y, Button: tea.MouseLeft})
	app.Update(tea.MouseReleaseMsg{X: r.X + r.W/2, Y: r.Y, Button: tea.MouseLeft})
	_ = app.View()
	if md.Mode() != markdownview.Source {
		t.Fatalf("clicking Source showed %q", md.Mode())
	}
	if app.focusTarget() != FocusEditor || app.markdownPreviewOwnsKeys() {
		t.Fatal("the source did not take the keys after the switch")
	}
}

func TestMarkdownPreviewLeavesChatBoundaryBeforeScrollbar(t *testing.T) {
	app, ws := newRealShellApp(t)
	paneJourneySize(app, 80, 24)
	md, _ := openMarkdownFile(t, app, ws.RootPath, "boundary.md", strings.Repeat(strings.Repeat("word ", 80)+"\n\n", 20))
	preview, ok := app.screenRectOf(md.Preview().ID())
	if !ok {
		t.Fatal("preview did not mount")
	}
	bar, ok := app.screenRectOf(ui.NewID(md.Preview().ID(), "scrollbar"))
	if !ok || bar.Empty() {
		t.Fatal("long document did not paint a scrollbar")
	}
	left := preview.X + app.Theme.Spacing.PadLR
	if md.Document().Width != bar.X-left-1 {
		t.Fatalf("wrap ends at column %d; scrollbar starts at %d, want one blank column", left+md.Document().Width, bar.X)
	}
	l := ui.Rect{X: bar.X - 1, Y: bar.Y, W: 1, H: bar.H}
	if got := strings.TrimSpace(paneJourneyText(app, l)); got != "" {
		t.Fatalf("text entered the chat boundary: %q", got)
	}
}

func TestMarkdownSelectedIconStaysLitAfterPointerLeaves(t *testing.T) {
	for _, width := range []int{80, 160} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			app, ws := newRealShellApp(t)
			paneJourneySize(app, width, 32)
			md, _ := openMarkdownFile(t, app, ws.RootPath, "modes.md", markdownFixture(6))
			modes := []markdownview.Mode{markdownview.Split, markdownview.Source, markdownview.Preview}
			button := func(mode markdownview.Mode) ui.ComponentID {
				return ui.NewID(ui.NewID(md.ID(), "toolbar"), string(mode))
			}
			for _, mode := range modes {
				r, ok := app.rootHost.ScreenRect(button(mode))
				if !ok {
					t.Fatalf("no %s button", mode)
				}
				app.Update(tea.MouseClickMsg{X: r.X + r.W/2, Y: r.Y, Button: tea.MouseLeft})
				app.Update(tea.MouseReleaseMsg{X: r.X + r.W/2, Y: r.Y, Button: tea.MouseLeft})
				app.Update(tea.MouseMotionMsg{X: r.X, Y: r.Y + 1})
				_ = app.View()
				for _, candidate := range modes {
					box, _ := app.rootHost.ScreenRect(button(candidate))
					lit := app.mainSurf.Buffer().Get(box.X, box.Y).BG == app.rootHost.Tokens().Color(ui.ColorAccent)
					if lit != (candidate == mode) || md.Mode() != mode {
						t.Fatalf("clicked %s, %s highlight=%v, current=%s", mode, candidate, lit, md.Mode())
					}
				}
			}
		})
	}
}

func TestShortMarkdownUsesBothContentRails(t *testing.T) {
	app, ws := newRealShellApp(t)
	paneJourneySize(app, 80, 24)
	md, sink := openMarkdownFile(t, app, ws.RootPath, "short.md", "One paragraph.")
	width := md.PreviewRect().W
	pumpMarkdown(t, app, sink, func() bool { return md.Document().Width == width-2 })
	if md.Preview().ScrollModel().MaxOffset() != 0 {
		t.Fatal("short document gained a scrollbar")
	}
}
