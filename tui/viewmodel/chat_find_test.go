package viewmodel

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/chatprojection"
	"crude/tui/screens/shellchat"
	"crude/tui/ui"
	"crude/tui/wm"
	ttypes "crude/types"
)

// findChatApp shows a chat of numbered messages; two of them carry "needle".
func findChatApp(t *testing.T) (*App, *shellchat.Screen) {
	t.Helper()
	rows := make([]chatprojection.MessageRow, 40)
	for i := range rows {
		rows[i] = chatprojection.MessageRow{ID: fmt.Sprint("seed", i), Lines: []string{fmt.Sprintf("message number %02d with nothing special", i)}}
	}
	app, screen := setupChatRoutingApp(t, 10, rows)
	app.Input.ClearInput()
	app.appendActiveMessage(ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: "the Needle is in the haystack", Time: time.Unix(2, 0)})
	for i := 0; i < 30; i++ {
		app.appendActiveMessage(ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: fmt.Sprintf("filler reply %d", i), Time: time.Unix(3+int64(i), 0)})
	}
	app.appendActiveMessage(ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: "one more needle at the end", Time: time.Unix(100, 0)})
	app.windowManager = wm.NewWindowManager()
	app.overlayHost = ui.NewOverlayHost(app.windowManager)
	app.paintShellFrame()
	return app, screen
}

// chatFindFrame is the window a chat search must use: the same one the editor
// uses, not a second design.
func chatFindFrame(t *testing.T, app *App) (*wm.FloatingWindow, *ui.ComponentWidget) {
	t.Helper()
	fw := app.windowManager.Window(string(ui.OverlayFindReplace))
	if fw == nil {
		t.Fatal("no find window")
	}
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok || cw == nil {
		t.Fatal("find window content is not a frame")
	}
	ww, hh := fw.Size()
	cw.Host().Resize(ww, hh)
	cw.Host().Layout()
	return fw, cw
}

func pressKey(app *App, code rune, text string, mod tea.KeyMod) {
	app.dispatchKeyPress(tea.KeyPressMsg{Code: code, Text: text, Mod: mod})
	app.paintShellFrame()
}

func typeText(app *App, text string) {
	for _, r := range text {
		pressKey(app, r, string(r), 0)
	}
}

func shellText(app *App) string {
	lines := make([]string, app.mainSurf.Buffer().Height())
	for y := range lines {
		lines[y] = rowText(app, y)
	}
	return strings.Join(lines, "\n")
}

func TestChatFindUsesTheEditorFindWindow(t *testing.T) {
	app, screen := findChatApp(t)
	pressKey(app, 'f', "", tea.ModCtrl)
	if !screen.FindOpen() {
		t.Fatal("Ctrl+F did not start a search in the chat")
	}
	fw, cw := chatFindFrame(t, app)
	if app.windowManager.Window("find-replace") != fw {
		t.Fatal("the chat search must use the shared find window")
	}
	labels := ui.FindReplaceButtonLabels(cw)
	if strings.Join(labels, ",") != "↑,↓" {
		t.Fatalf("action row labels %v, want the shared ↑/↓ pair", labels)
	}
	for i, want := range []string{"Aa", "ab", ".*"} {
		r, ok := ui.FindReplaceFrameToggleBounds(cw, i)
		if !ok || r.Empty() {
			t.Fatalf("filter pill %q is missing", want)
		}
	}
	// Top-right of the transcript, the editor window's own anchor.
	w, _ := fw.Size()
	x, y := fw.Position()
	if got := app.chatRect.X + app.chatRect.W - w - 2; x != got {
		t.Fatalf("window x=%d, want %d (top-right of the chat)", x, got)
	}
	if y != app.chatRect.Y+1 {
		t.Fatalf("window y=%d, want %d", y, app.chatRect.Y+1)
	}
	if title := ui.FindReplaceFrameTitle(cw); title != "Main Agent" {
		t.Fatalf("title %q must name the searched surface, as a buffer name does", title)
	}
}

func TestChatFindCountsAndStepsThroughMatches(t *testing.T) {
	app, screen := findChatApp(t)
	pressKey(app, 'f', "", tea.ModCtrl)
	typeText(app, "needle")
	if got := app.Input.Value(); got != "" {
		t.Fatalf("typing in find leaked into the composer: %q", got)
	}
	_, cw := chatFindFrame(t, app)
	if got := ui.FindReplaceFrameStatus(cw); got != "2 of 2" {
		t.Fatalf("counter %q, want the newest match selected", got)
	}
	if !strings.Contains(shellText(app), "one more needle at the end") {
		t.Fatal("the selected match must be in view")
	}
	pressKey(app, tea.KeyEnter, "", 0)
	if got := ui.FindReplaceFrameStatus(chatFindFrameOrFail(t, app)); got != "1 of 2" {
		t.Fatalf("Enter must go to the next match, counter=%q", got)
	}
	if !strings.Contains(shellText(app), "the Needle is in the haystack") {
		t.Fatalf("the older match must be revealed:\n%s", shellText(app))
	}
	if screen.ScrollModel().AutoScroll() {
		t.Fatal("revealing history must stop following new messages")
	}
	pressKey(app, tea.KeyEnter, "", tea.ModShift)
	if got := ui.FindReplaceFrameStatus(chatFindFrameOrFail(t, app)); got != "2 of 2" {
		t.Fatalf("Shift+Enter must go back, counter=%q", got)
	}
}

func chatFindFrameOrFail(t *testing.T, app *App) *ui.ComponentWidget {
	t.Helper()
	_, cw := chatFindFrame(t, app)
	return cw
}

func TestChatFindFilterPillsChangeTheSearch(t *testing.T) {
	app, _ := findChatApp(t)
	pressKey(app, 'f', "", tea.ModCtrl)
	typeText(app, "needle")
	if got := ui.FindReplaceFrameStatus(chatFindFrameOrFail(t, app)); got != "2 of 2" {
		t.Fatalf("setup counter %q", got)
	}
	pressKey(app, 'c', "", tea.ModAlt) // Aa: case sensitive
	if got := ui.FindReplaceFrameStatus(chatFindFrameOrFail(t, app)); got != "1 of 1" {
		t.Fatalf("case-sensitive counter %q, want only the lowercase match", got)
	}
	pressKey(app, 'c', "", tea.ModAlt) // back to folded
	pressKey(app, 'w', "", tea.ModAlt) // ab: whole word
	if got := ui.FindReplaceFrameStatus(chatFindFrameOrFail(t, app)); got != "2 of 2" {
		t.Fatalf("whole-word counter %q", got)
	}
}

func TestChatFindEscClosesAndKeepsTheDraft(t *testing.T) {
	app, screen := findChatApp(t)
	typeText(app, "half-written note")
	pressKey(app, 'f', "", tea.ModCtrl)
	typeText(app, "needle")
	pressKey(app, tea.KeyEsc, "", 0)
	if screen.FindOpen() || app.windowManager.Window("find-replace") != nil {
		t.Fatal("Esc did not close the chat search")
	}
	if count, _ := screen.FindCounts(); count != 0 {
		t.Fatal("closing find must drop its counts")
	}
	if got := app.Input.Value(); got != "half-written note" {
		t.Fatalf("Esc in find must not reach the draft, got %q", got)
	}
	typeText(app, "ok")
	if got := app.Input.Value(); got != "half-written noteok" {
		t.Fatalf("the composer must get the keys again, got %q", got)
	}
}

func TestChatFindPasteGoesToTheFindField(t *testing.T) {
	app, _ := findChatApp(t)
	pressKey(app, 'f', "", tea.ModCtrl)
	app.dispatchPasteMsg(tea.PasteMsg{Content: "haystack"})
	app.paintShellFrame()
	if app.Input.Value() != "" {
		t.Fatalf("paste leaked into the composer: %q", app.Input.Value())
	}
	if got := ui.FindReplaceFrameStatus(chatFindFrameOrFail(t, app)); got != "1 of 1" {
		t.Fatalf("pasted query counter %q", got)
	}
}

func TestChatFindCtrlFIsOnlyForChatFocus(t *testing.T) {
	app, screen := findChatApp(t)
	app.setFocusTabBar()
	if app.focusTarget() != FocusTabBar {
		t.Fatalf("setup: focus target %v", app.focusTarget())
	}
	pressKey(app, 'f', "", tea.ModCtrl)
	if screen.FindOpen() {
		t.Fatal("Ctrl+F in the tab bar must not open chat find")
	}
}

func TestChatFindKeepsTheShellAwakeOnlyWhileCounting(t *testing.T) {
	app, screen := findChatApp(t)
	pressKey(app, 'f', "", tea.ModCtrl)
	typeText(app, "needle")
	if app.chatFindPending() || screen.FindPending() {
		t.Fatal("a short chat is counted within its first frame")
	}
}

// A resize while history is still being drawn leaves the rest undrawn until the
// user scrolls to it. Find must not stop at the part that was drawn: it has to
// finish the history, in the background, and then report the true count.
func TestChatFindFinishesHistoryThatAResizeLeftUndrawn(t *testing.T) {
	rows := []chatprojection.MessageRow{{ID: "seed", Lines: []string{"seed"}}}
	app, screen := setupChatRoutingApp(t, 10, rows)
	app.Input.ClearInput()
	app.windowManager = wm.NewWindowManager()
	app.overlayHost = ui.NewOverlayHost(app.windowManager)
	renderer := app.activeMessageList()
	renderer.MaterializeBudget = time.Nanosecond // history draws slower than the first frames
	for i := 0; i < 60; i++ {
		text := fmt.Sprintf("reply %d about the viewport and the scroll model", i)
		app.appendActiveMessage(ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: text, Time: time.Unix(10+int64(i), 0)})
	}
	app.paintShellFrame()
	app.width = 100 // the terminal was resized: a width-only reflow
	app.paintShellFrame()

	pressKey(app, 'f', "", tea.ModCtrl)
	typeText(app, "viewport")
	status := ui.FindReplaceFrameStatus(chatFindFrameOrFail(t, app))
	if !strings.Contains(status, "loading") && status != "Searching…" {
		t.Fatalf("setup: every row was drawn, so find has nothing to wait for; status=%q", status)
	}

	renderer.MaterializeBudget = 0 // normal budget from here on
	for i := 0; i < 600 && (screen.FindPending() || screen.FindWaiting() || renderer.Materializing()); i++ {
		app.paintShellFrame()
	}
	status = ui.FindReplaceFrameStatus(chatFindFrameOrFail(t, app))
	if strings.Contains(status, "loading") || !strings.HasSuffix(status, "of 60") {
		t.Fatalf("find must finish the whole history and count all 60 replies, status=%q", status)
	}
}
