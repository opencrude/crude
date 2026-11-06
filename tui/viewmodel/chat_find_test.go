package viewmodel

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/chatprojection"
	"crude/tui/screens/shellchat"
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
	app.paintShellFrame()
	return app, screen
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

func TestChatFindJourneyFromKeyboardToMarkedMatch(t *testing.T) {
	app, screen := findChatApp(t)
	if screen.FindOpen() {
		t.Fatal("find must start closed")
	}
	rowsBefore := screen.ScrollModel().Viewport()

	pressKey(app, 'f', "", tea.ModCtrl)
	if !screen.FindOpen() || app.rootHost.Focused() != screen.FindID() {
		t.Fatalf("Ctrl+F must open find and focus its field; open=%v focus=%q", screen.FindOpen(), app.rootHost.Focused())
	}
	if got := screen.ScrollModel().Viewport(); got != rowsBefore-1 {
		t.Fatalf("the bar must take one transcript row: %d -> %d", rowsBefore, got)
	}

	typeText(app, "needle")
	if got := app.Input.Value(); got != "" {
		t.Fatalf("typing in find leaked into the composer: %q", got)
	}
	if !strings.Contains(shellText(app), "Find in chat") && !strings.Contains(shellText(app), "needle") {
		t.Fatal("the find field is not painted")
	}
	if !strings.Contains(shellText(app), "of 2") {
		t.Fatalf("the bar must show the match count:\n%s", shellText(app))
	}
	if !strings.Contains(shellText(app), "one more needle at the end") {
		t.Fatal("the newest match, nearest the bottom, must be in view")
	}

	pressKey(app, tea.KeyUp, "", 0)
	if !strings.Contains(shellText(app), "the Needle is in the haystack") {
		t.Fatalf("Up must reveal the older match:\n%s", shellText(app))
	}
	if screen.ScrollModel().AutoScroll() {
		t.Fatal("revealing history must stop following new messages")
	}

	pressKey(app, tea.KeyEsc, "", 0)
	if screen.FindOpen() || app.rootHost.Focused() == screen.FindID() {
		t.Fatal("Esc must close find and give the keyboard back")
	}
	typeText(app, "ok")
	if got := app.Input.Value(); got != "ok" {
		t.Fatalf("after closing find the composer must get the keys, got %q", got)
	}
}

func TestChatFindEscDoesNotTouchTheDraft(t *testing.T) {
	app, screen := findChatApp(t)
	typeText(app, "half-written note")
	pressKey(app, 'f', "", tea.ModCtrl)
	typeText(app, "needle")
	pressKey(app, tea.KeyEsc, "", 0)
	if screen.FindOpen() {
		t.Fatal("find still open")
	}
	if got := app.Input.Value(); got != "half-written note" {
		t.Fatalf("Esc in the find bar must not reach the draft, got %q", got)
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
	if !strings.Contains(shellText(app), "1 of 1") {
		t.Fatalf("pasted query was not searched:\n%s", shellText(app))
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
	if !strings.Contains(shellText(app), "loading") {
		t.Fatalf("setup: history was fully drawn, so find has nothing to wait for:\n%s", shellText(app))
	}

	renderer.MaterializeBudget = 0 // normal budget from here on
	for i := 0; i < 600 && (screen.FindPending() || screen.FindWaiting() || renderer.Materializing()); i++ {
		app.paintShellFrame()
	}
	text := shellText(app)
	if strings.Contains(text, "loading") || !strings.Contains(text, "of 60") {
		t.Fatalf("find must finish the whole history and count all 60 replies:\n%s", text)
	}
}
