package viewmodel

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/sessiondb"
	"crude/tui/rendering"
	"crude/tui/screens/activitybar"
	ttypes "crude/tui/types"
	"crude/workspace"
)

// A long input must clamp to the terminal/frame and scroll internally. The
// former status rows are intentionally content now.
func TestAgentView_LongInputDoesNotOverlapStatus(t *testing.T) {
	app := newTransitionApp(t)
	t.Cleanup(func() {
		app.Close()
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
		app.Close()
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
		app.Close()
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

	// Short chat anchors at the slot bottom. Its leading inter-object spacer
	// remains inside that bottom-anchored block, never at a double origin.
	contentH := app.chatScreen.ScrollModel().Content()
	if contentH < 1 {
		t.Fatal("precondition: message content exists")
	}
	headerY := app.chatRect.Y + app.chatScreen.ScrollModel().Viewport() - contentH + 1
	headerRow := rowText(app, headerY)
	headerCol := lay.SidebarW + lay.PadLR
	got := strings.TrimSpace(rowTextFrom(app, headerY, headerCol))
	if strings.HasPrefix(got, "❯") || got == "" {
		t.Fatalf("chat header not at col %d row %d (bottom-anchored); row=%q", headerCol, headerY, headerRow)
	}

	// Rows above the bottom-anchored content must repaint cleanly.
	emptyY := app.chatRect.Y + 3
	emptyRow := rowText(app, emptyY)
	if strings.TrimSpace(rowTextFrom(app, emptyY, app.chatRect.X+lay.PadLR)) != "" {
		t.Fatalf("chat area rows above short content not clean: %q", emptyRow)
	}
}

// TestAgentView_ShortChatAnchorsToBottom guards short conversations against a
// large variable gap between their last message and the input prompt.
func TestAgentView_ShortChatAnchorsToBottom(t *testing.T) {
	app := newTransitionApp(t)
	t.Cleanup(func() {
		app.Close()
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

	lastRow := app.chatRect.Y + app.chatScreen.ScrollModel().Viewport() - 1
	last := rowText(app, lastRow)
	if !strings.Contains(last, "hello from the agent view") {
		t.Fatalf("last message not anchored to the slot bottom row; row=%q", last)
	}
	if strings.TrimSpace(rowTextFrom(app, app.chatRect.Y+2, app.chatRect.X+lay.PadLR)) != "" {
		t.Fatalf("gravity pad rows above the message not blank: %q", rowText(app, app.chatRect.Y+2))
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
		app.Close()
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

func TestAgentView_ResumeContextOnlyMessagesDoNotCreateBlankTail(t *testing.T) {
	for _, turns := range []int{1, 6} {
		t.Run(fmt.Sprintf("%d_turns", turns), func(t *testing.T) {
			app := newTransitionApp(t)
			t.Cleanup(func() { app.Close() })
			db := sessiondb.OpenAndMigrateAt(t.TempDir())
			t.Cleanup(func() { _ = db.Close() })
			if err := sessiondb.CreateSessionRow(db, "resume"); err != nil {
				t.Fatal(err)
			}
			var messages []provider.ChatMessage
			var metas []sessiondb.DisplayMeta
			for turn := 0; turn < turns; turn++ {
				runtimeContext := provider.NewTextMessage("user", "<turn_context>private recalled memory</turn_context>")
				runtimeContext.Origin = provider.OriginRuntimeContext
				runtimeContext.Visibility = provider.VisibilityContextOnly
				messages = append(messages, runtimeContext)
				// This is the production memory envelope: it has no display snapshot.
				metas = append(metas, sessiondb.DisplayMeta{ContextOnly: true, DisplayRole: "hidden_runtime_context"})
				for _, role := range []string{"user", "assistant"} {
					text := "hi"
					if role == "assistant" {
						text = "Hi! Ready when you are.\n\nEND-OF-CHAT"
					}
					messages = append(messages, provider.NewTextMessage(role, text))
					metas = append(metas, sessiondb.DisplayMeta{SnapshotVersion: sessiondb.ConversationSnapshotVersion,
						Snapshots: []workspace.Message{{InternalID: fmt.Sprintf("turn-%d-%s", turn, role), Role: role, Content: text,
							Time: time.Unix(1000+int64(turn), 0)}}})
				}
			}
			if _, err := sessiondb.ReplaceMessagesWithMeta(db, "resume", messages, nil, metas); err != nil {
				t.Fatal(err)
			}
			stored, err := sessiondb.LoadMessagesWithMeta(db)
			if err != nil {
				t.Fatal(err)
			}
			views := workspace.ConvertMessagesForDisplay(stored)
			// Resume merges display snapshots by their display timestamps, while
			// unprojected envelopes carry the later DB commit timestamp.
			slices.SortStableFunc(views, func(a, b workspace.ChatMessageView) int { return a.Time.Compare(b.Time) })
			app.hydrateActiveMessages(ChatMessagesFromViews(views))
			for _, width := range []int{120, 80} {
				_, _ = app.Update(tea.WindowSizeMsg{Width: width, Height: 40})
				for frame := 0; frame < 2; frame++ {
					app.paintShellFrame()
					bottom := rowText(app, app.chatRect.Y+app.chatScreen.ScrollModel().Viewport()-1)
					if !strings.Contains(bottom, "END-OF-CHAT") {
						t.Fatalf("width=%d frame=%d: resumed chat left a blank tail; bottom=%q", width, frame, bottom)
					}
				}
			}
			if got := len(stored); got != turns*3 {
				t.Fatalf("provider history was changed: got %d messages", got)
			}
		})
	}
}
