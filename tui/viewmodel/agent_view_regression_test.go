package viewmodel

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	ttypes "crude/tui/types"
)

// TestAgentView_LongInputDoesNotOverlapStatus: a long/multiline input whose
// shell would exceed the vertical budget must be clamped to the space above
// the status bar — the status separator and status content rows must never
// be overwritten. Regression: InputLineCount was unclamped; the old
// layer-surface world masked the overflow with the status surface's higher
// z-order, but the root host paints status FIRST, so the input's extra rows
// painted over the status separator and the status content ("input pushes
// the right half of the bottom status and its separator out of screen").
func TestAgentView_LongInputDoesNotOverlapStatus(t *testing.T) {
	app := newTransitionApp(t)
	t.Cleanup(func() {
		app.Workspace.Close()
	})
	_, _ = app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	app.Input.SetInput(strings.Repeat("this is a very long line of text that keeps going and going and wrapping ", 12))
	app.paintShellFrame()

	if app.inputRect.Empty() {
		t.Fatal("precondition: input visible")
	}
	if app.inputRect.Y+app.inputRect.H > app.height-2 {
		t.Fatalf("input shell overlaps the status bar: rect=%+v height=%d", app.inputRect, app.height)
	}

	// The input's last visible row must end inside its slot, and the status
	// separator row must be a clean full-width divider.
	sep := rowText(app, app.height-2)
	for x := 0; x < len([]rune(sep)); x++ {
		c := []rune(sep)[x]
		if c != ' ' && c != '─' && c != '│' {
			t.Fatalf("status separator row polluted by input overflow at col %d: %q", x, sep)
		}
	}
	// The status content row must not contain input text.
	content := rowText(app, app.height-1)
	if strings.Contains(content, "wrapping this is a very long line") {
		t.Fatalf("status content row polluted by input overflow: %q", content)
	}
}

// TestAgentView_TextAreaBgDoesNotOverwriteStatus: the TextArea's background
// fill must not double-count its paint origin. Regression: TextArea.Paint
// passed the already-translated (ox, oy) to FillRect, which adds r.X/r.Y
// again — the white PopupBg fill landed at originY+2*r.Y (the status
// separator row) and originX+2*r.X (col SidebarW+2*pad+2), erasing the
// right portion of the status divider and leaving a white band over the
// status content. Visible as "input pushes the right half of the bottom
// status and its separator out of screen".
func TestAgentView_TextAreaBgDoesNotOverwriteStatus(t *testing.T) {
	app := newTransitionApp(t)
	t.Cleanup(func() {
		app.Workspace.Close()
	})
	app.ApplyThemePreset("light") // PopupBg #ffffff contrasts the divider
	_, _ = app.Update(tea.WindowSizeMsg{Width: 240, Height: 60})
	app.paintShellFrame()

	sepY := app.height - 2
	buf := app.mainSurf.Buffer()
	borderBG := rendering.ThemeColorToCell(app.Theme.Colors.Bg)
	popupBG := rendering.ThemeColorToCell(app.Theme.Colors.PopupBg)

	dashCount := 0
	whiteLeaked := 0
	for x := 0; x < buf.Width(); x++ {
		c := buf.Get(x, sepY)
		if c.Char == '─' {
			dashCount++
		}
		if c.BG.Equal(popupBG) && !c.BG.Equal(borderBG) {
			whiteLeaked++
		}
	}
	if dashCount != buf.Width() {
		t.Fatalf("status separator has only %d dashes (want %d): TextArea bg overwrote the divider", dashCount, buf.Width())
	}
	if whiteLeaked > 0 {
		t.Fatalf("status separator row has %d cells with leaked PopupBg (textarea double-origin fill)", whiteLeaked)
	}

	// The status content row must also be free of textarea bg leaks.
	contentY := app.height - 1
	for x := 0; x < buf.Width(); x++ {
		c := buf.Get(x, contentY)
		if c.BG.Equal(popupBG) && !c.BG.Equal(borderBG) {
			t.Fatalf("status content row leaked PopupBg at col %d: textarea double-origin fill", x)
		}
	}
}

func rowText(app *App, y int) string {
	buf := app.mainSurf.Buffer()
	var b strings.Builder
	for x := 0; x < buf.Width(); x++ {
		c := buf.Get(x, y)
		if c.Width > 0 && c.Char != 0 {
			b.WriteRune(c.Char)
		} else {
			b.WriteRune(' ')
		}
	}
	return b.String()
}

// TestAgentView_ChatPaintsInsideSlot: the agent chat message list must paint
// INSIDE its slot rect. Regression: Host.paintNode paints children at
// (parent origin + child bounds); shellchat passed its absolute slot rect as
// child bounds, so the list double-counted the origin and painted content
// shifted right by SidebarW and down by ChatAreaTop — the shifted rows then
// overwrote the input prompt and the status bar.
func TestAgentView_ChatPaintsInsideSlot(t *testing.T) {
	app := newTransitionApp(t)
	t.Cleanup(func() {
		app.Workspace.Close()
	})
	_, _ = app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	app.paintShellFrame()

	lay := app.computeLayout()
	if lay.SidebarW != 31 {
		t.Fatalf("precondition: sidebar 31 wide, got %d", lay.SidebarW)
	}
	if !app.shellSlotActive(shellChatSlot) {
		t.Fatal("precondition: chat slot active")
	}

	// Short chat: gravity anchors the content at the bottom of the slot, so
	// the message header sits at (chatRect bottom - contentH) + 1 — never at
	// the double-origin position (row ChatAreaTop+1, col SidebarW*2+PadLR).
	contentH := app.chatScreen.ScrollModel().Content()
	if contentH < 1 {
		t.Fatal("precondition: message content exists")
	}
	headerY := app.chatRect.Y + app.chatRect.H - contentH + 1
	headerRow := rowText(app, headerY)
	headerCol := lay.SidebarW + lay.PadLR
	got := strings.TrimSpace(headerRow[headerCol:])
	if !strings.HasPrefix(got, "❯") {
		t.Fatalf("chat header not at col %d row %d (bottom-anchored); row=%q", headerCol, headerY, headerRow)
	}

	// Rows above the chat content (the gravity pad) must be clean (no stale
	// glyphs) across the chat slot's content columns (skip the sidebar
	// divider + pad).
	emptyRow := rowText(app, app.chatRect.Y+3)
	if strings.TrimSpace(emptyRow[app.chatRect.X+lay.PadLR:]) != "" {
		t.Fatalf("chat area rows above content not clean: %q", emptyRow)
	}
}

// TestAgentView_ShortChatAnchorsToBottom: with fewer message lines than the
// viewport, the last message line must sit on the chat slot's BOTTOM row
// (gravity), and the rows above it must be blank. Regression: the root-host
// VirtualList painted items from row 0, so short chats appeared at the TOP
// of the chat area with a huge empty gap above the input prompt.
func TestAgentView_ShortChatAnchorsToBottom(t *testing.T) {
	app := newTransitionApp(t)
	t.Cleanup(func() {
		app.Workspace.Close()
	})
	_, _ = app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	app.paintShellFrame()

	lay := app.computeLayout()
	if !app.shellSlotActive(shellChatSlot) {
		t.Fatal("precondition: chat slot active")
	}
	if app.ChatAreaHeight < 5 {
		t.Skipf("viewport too small for the gravity assertion (chatH=%d)", app.ChatAreaHeight)
	}

	lastRow := app.chatRect.Y + app.chatRect.H - 1
	last := rowText(app, lastRow)
	if !strings.Contains(last, "hello from the agent view") {
		t.Fatalf("last message not anchored to the slot bottom row; row=%q", last)
	}
	topRow := rowText(app, app.chatRect.Y+2)
	if strings.TrimSpace(topRow[app.chatRect.X+lay.PadLR:]) != "" {
		t.Fatalf("gravity pad rows above the message not blank: %q", topRow)
	}
}

// TestAgentView_LongChatDoesNotOverflowInputOrStatus: with enough message
// content to fill the viewport, the chat's bottom rows must stop at the chat
// slot's bottom — the input prompt row and the status bar rows must show
// ONLY their own content. Regression: the double-origin offset pushed the
// viewport down by ChatAreaTop, so the chat's last rows painted over the
// input separator, the prompt, and the footer.
func TestAgentView_LongChatDoesNotOverflowInputOrStatus(t *testing.T) {
	app := newTransitionApp(t)
	t.Cleanup(func() {
		app.Workspace.Close()
	})
	_, _ = app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	app.paintShellFrame()

	// Build a chat long enough to overflow the viewport.
	longBody := strings.Repeat("line of the long message body that wraps across the chat width over and over again. ", 8)
	for i := 0; i < 12; i++ {
		app.appendActiveMessage(ttypes.ChatMessage{
			Role:    ttypes.RoleAssistant,
			Content: longBody + " message-" + string(rune('A'+i)),
			Time:    time.Now(),
		})
	}
	app.syncAgentMessages("agent-1", false)
	app.paintShellFrame()

	lay := app.computeLayout()
	if app.ChatAreaHeight < 5 {
		t.Skipf("viewport too small for the overflow assertion (chatH=%d)", app.ChatAreaHeight)
	}

	inputY := app.inputRect.Y
	statusY := app.height - 2

	// The chat slot's bottom row must be painted (list reaches the bottom),
	// while the input separator row shows only separator glyphs, the prompt
	// row only the prompt, and the status rows only border+hint.
	bottom := rowText(app, app.chatRect.Y+app.chatRect.H-1)
	if strings.TrimSpace(bottom[lay.SidebarW+lay.PadLR:]) == "" {
		t.Fatalf("chat content does not fill the slot bottom row: %q", bottom)
	}

	sep := rowText(app, inputY)
	for x := lay.SidebarW; x < lay.SidebarW+lay.ContentW && x < len(sep); x++ {
		c := []rune(sep)[x]
		if c != ' ' && c != '─' {
			t.Fatalf("input separator row polluted by chat overflow at col %d: %q", x, sep)
		}
	}

	prompt := rowText(app, inputY+1)
	// Beyond the prompt glyph and its space, everything must be blank.
	// Slice by RUNES: the prompt glyph is wide (multi-byte UTF-8), and a
	// byte-wise slice can land mid-rune.
	promptRunes := []rune(prompt)
	checkCol := lay.SidebarW + lay.PadLR + 2
	if checkCol >= len(promptRunes) {
		checkCol = len(promptRunes) - 1
	}
	if strings.TrimSpace(string(promptRunes[checkCol:])) != "" {
		t.Fatalf("input prompt row polluted by chat overflow: %q", prompt)
	}

	status := rowText(app, statusY+1)
	// The status hint must be present and the status row must NEVER contain
	// chat body text (with the double-origin bug, the shifted chat rows
	// overwrote the right half of the status bar). The body line wraps and
	// gets truncated at the screen edge, so match on a fragment that
	// survives truncation ("long" may be cut mid-word).
	if strings.Contains(status, "message body that wraps") {
		t.Fatalf("status bar polluted by chat overflow: %q", status)
	}
	if !strings.Contains(status, "Enter send") && !strings.Contains(status, "Esc exit") {
		t.Fatalf("status hint row missing after long chat: %q", status)
	}
}
