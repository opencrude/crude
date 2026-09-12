package viewmodel

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	"crude/tui/screens/activitybar"
	ttypes "crude/tui/types"
)

// A long input must clamp to the terminal/frame and scroll internally. The
// former status rows are intentionally content now.
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
	if app.inputRect.Y+app.inputRect.H > app.height-app.chromeBottomRows() {
		t.Fatalf("input shell overlaps footer: rect=%+v height=%d", app.inputRect, app.height)
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

	buf := app.mainSurf.Buffer()
	borderBG := rendering.ThemeColorToCell(app.Theme.Colors.Bg)
	popupBG := rendering.ThemeColorToCell(app.Theme.Colors.PopupBg)

	whiteLeaked := 0
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			if app.inputRect.Contains(x, y) {
				continue
			}
			c := buf.Get(x, y)
			if c.BG.Equal(popupBG) && !c.BG.Equal(borderBG) {
				whiteLeaked++
			}
		}
	}
	if whiteLeaked > 0 {
		t.Fatalf("textarea background leaked into %d cells outside its slot", whiteLeaked)
	}
}

func rowText(app *App, y int) string {
	return rowTextFrom(app, y, 0)
}

func rowTextFrom(app *App, y, startX int) string {
	buf := app.mainSurf.Buffer()
	var b strings.Builder
	for x := startX; x < buf.Width(); x++ {
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
	if lay.SidebarW != 31+activitybar.Width {
		t.Fatalf("precondition: sidebar %d wide, got %d", 31+activitybar.Width, lay.SidebarW)
	}
	if !app.shellSlotActive(shellChatSlot) {
		t.Fatal("precondition: chat slot active")
	}

	// Short chat starts at the slot top. Its leading inter-object spacer owns
	// row zero and the header follows immediately, never at a double origin.
	contentH := app.chatScreen.ScrollModel().Content()
	if contentH < 1 {
		t.Fatal("precondition: message content exists")
	}
	headerY := app.chatRect.Y + 1
	headerRow := rowText(app, headerY)
	headerCol := lay.SidebarW + lay.PadLR
	got := strings.TrimSpace(rowTextFrom(app, headerY, headerCol))
	if strings.HasPrefix(got, "❯") || got == "" {
		t.Fatalf("chat header not at col %d row %d (top-anchored); row=%q", headerCol, headerY, headerRow)
	}

	// Unused rows follow short content; resume must never synthesize a variable
	// blank region before the first message.
	emptyY := app.chatRect.Y + contentH + 1
	emptyRow := rowText(app, emptyY)
	if strings.TrimSpace(rowTextFrom(app, emptyY, app.chatRect.X+lay.PadLR)) != "" {
		t.Fatalf("chat area rows below short content not clean: %q", emptyRow)
	}
}

// TestAgentView_ShortChatStartsAtTopWithoutResumeGap guards resumed short
// conversations against variable blank padding above their first message.
func TestAgentView_ShortChatStartsAtTopWithoutResumeGap(t *testing.T) {
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

	foundAt := -1
	for y := app.chatRect.Y; y < min(app.chatRect.Y+6, app.chatRect.Y+app.chatRect.H); y++ {
		if strings.Contains(rowText(app, y), "hello from the agent view") {
			foundAt = y
			break
		}
	}
	if foundAt < 0 {
		t.Fatalf("short resumed chat did not start near slot top; chat=%q", rowText(app, app.chatRect.Y))
	}
	lastRow := app.chatRect.Y + app.chatRect.H - 1
	if strings.TrimSpace(rowTextFrom(app, lastRow, app.chatRect.X+lay.PadLR)) != "" {
		t.Fatalf("short chat still bottom-gravity padded content into last row: %q", rowText(app, lastRow))
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

	footer := rowText(app, app.height-app.frameBottomRow()-1)
	if !strings.Contains(footer, "Help") {
		t.Fatalf("footer Help entry missing after long chat: %q", footer)
	}
}
