package shellchat_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/chatprojection"
	"crude/tui/rendering"
	"crude/tui/screens/shellchat"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/types"
)

// Ordinary row padding must keep the window's fill, not inherit a shaded
// transcript below it. Text, blank rows and selected padding share that rule;
// intentional pane bands and inline styling must still keep their own colors.
func TestWindowedChatPaddingKeepsItsBackground(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, width := range []int{48, 120} {
			t.Run(th.Name+"/"+strconv.Itoa(width), func(t *testing.T) {
				tokens := ui.NewTokenSet(th)
				bg := tokens.Color(ui.ColorBg)
				paneBG := tokens.Color(ui.ColorSubPaneBg)
				screen := shellchat.New(th)
				screen.SetMessages([]chatprojection.MessageRow{{
					ID: "ordinary", Lines: []string{"Assignment", "Read parser.go", "", "Selected"},
				}, {
					ID: "special", Lines: []string{"Thought"},
					Native: []chatprojection.PaintLine{{
						Spans: []chatprojection.RowSpan{{Text: "Thought", Role: ui.ColorFg}}, PaneBackground: true,
					}},
				}, {
					ID: "styled", Lines: []string{"code"},
					Native: []chatprojection.PaintLine{{Spans: []chatprojection.RowSpan{{
						Text: "code", Role: ui.ColorFg, BgSet: true, BgRole: ui.ColorPopupSubtleBg,
					}}}},
				}}, shellchat.PaintParams{
					Compose:      shellchat.ComposeParams{InnerW: width - 6, PadLR: 2, Theme: th},
					HasSelection: true, SelStartLine: 3, SelStartCol: 2, SelEndLine: 3, SelEndCol: 16,
				})
				frame := ui.NewWindowFrameWidget(th, "chat/frame", ui.FrameWindow, "Chat", screen, nil, true, width, 8)
				frame.Frame().SetFill(ui.ColorBg)
				window := surface.NewSurface("chat-window", width, 8, cell.TransparentBlank())
				frame.Render(window, &wm.RenderContext{Theme: th})
				composed := surface.NewCellBuffer(width, 8)
				composed.Fill(cell.BlankCell(paneBG))
				surface.Blit(composed, 0, 0, window.Buffer(), nil)
				for row := 0; row < 6; row++ {
					for x := 1; x < width-1; x++ {
						want := bg
						switch {
						case row == 4:
							want = paneBG
						case row == 3 && x >= 5 && x < 19:
							want = tokens.Color(ui.ColorTextSelection)
						case row == 5 && x >= 3 && x < 7:
							want = tokens.Color(ui.ColorPopupSubtleBg)
						}
						if got := composed.Get(x, row+1).BG; got != want {
							t.Fatalf("row %d column %d background=%v, want %v (window padding must not show the transcript behind it)", row, x, got, want)
						}
					}
				}
			})
		}
	}
}

func TestUserAndAgentHeadersPaintSubtleThemeRulesAcrossViewport(t *testing.T) {
	for _, themeCase := range []struct {
		name string
		th   *theme.Theme
	}{
		{"dark", theme.DefaultDark()},
		{"light", theme.DefaultLight()},
	} {
		for _, width := range []int{80, 24} {
			t.Run(themeCase.name+"/"+strconv.Itoa(width), func(t *testing.T) {
				th := themeCase.th
				renderer := chatprojection.NewRenderer(th)
				renderer.SetSize(width, 16)
				renderer.SetChatAreaBounds(0, 16)
				renderer.SetUsername("You")
				renderer.SyncMessages([]types.ChatMessage{
					{InternalID: "user", Role: types.RoleUser, Content: "first", Time: time.Unix(1000, 0)},
					{InternalID: "agent", Role: types.RoleAssistant, Content: "second", ModelID: "m", Time: time.Unix(1001, 0)},
				}, 1, 0)
				rows, _, height := renderer.ExportMessageRows(width)
				screen := shellchat.New(th)
				screen.SetMessages(rows, shellchat.PaintParams{Compose: shellchat.ComposeParams{InnerW: width - 4, PadLR: 2, Theme: th}})
				screen.SyncScrollMetrics(height, height)
				buf := screen.PaintTestBuffer(width, height)
				tokens := ui.NewTokenSet(th)
				y := 0
				for _, row := range rows {
					for i, line := range row.Lines {
						if i < len(row.Native) && row.Native[i].HeaderRule {
							if strings.Contains(line, "first") || strings.Contains(line, "second") {
								t.Fatalf("body was marked as a header: %q", line)
							}
							for _, x := range []int{0, 1, width - 2, width - 1} {
								cell := buf.Get(x, y)
								if cell.Char != '─' || cell.FG != tokens.Color(ui.ColorBorder) {
									t.Fatalf("header edge (%d,%d) = %+v; want themed rule", x, y, cell)
								}
							}
							if row.Native[i].Spans[0].BgSet || buf.Get(2, y).BG == tokens.Color(ui.ColorPopupSubtleBg) {
								t.Fatalf("name still has label background: %+v", row.Native[i].Spans[0])
							}
						}
						y++
					}
				}
			})
		}
	}
}

func TestMessageSyncPreservesUnchangedCaches(t *testing.T) {
	store := chatprojection.NewMessageSync()
	messages := []types.ChatMessage{{InternalID: "domain-1", Role: types.RoleAssistant, Content: "hello"}}
	store.SyncMessages(messages, 1, 0)
	store.Messages[0].CachedLines = []string{"rendered"}

	_, ok := store.SyncMessages(messages, 2, 0)
	if !ok {
		t.Fatal("expected sync to apply")
	}
	if got := store.Messages[0].CachedLines; len(got) != 1 || got[0] != "rendered" {
		t.Fatalf("unchanged cache discarded: %#v", got)
	}
}

func TestParallelBatchPaintsChildActionsInsteadOfEscapedJSON(t *testing.T) {
	th := theme.DefaultDark()
	renderer := chatprojection.NewRenderer(th)
	renderer.SetSize(116, 18)
	renderer.SetChatAreaBounds(0, 18)
	renderer.SyncMessages([]types.ChatMessage{{InternalID: "batch", Role: types.RoleTool, ToolName: "multi_tool_use.parallel", ToolInput: `{"tool_uses":[{"recipient_name":"functions.execute_command","parameters":{"command":"git status --short"}},{"recipient_name":"functions.read_file","parameters":{"path":"main.go"}}]}`, ToolStatus: "success", ToolPreviewPhase: "done", Content: `{"results":[{"recipient_name":"execute_command","result":"M main.go\nM go.mod","error":false},{"recipient_name":"read_file","result":"package main","error":false}]}`}}, 1, 0)
	renderer.ToggleDisclosure("activity:batch:0")
	rows, _, height := renderer.ExportMessageRows(116)
	screen := shellchat.New(th)
	screen.SetMessages(rows, shellchat.PaintParams{Compose: shellchat.ComposeParams{InnerW: 116, PadLR: 2, Theme: th}})
	screen.SyncScrollMetrics(height, 18)
	screen.Layout(ui.LayoutContext{}, ui.Rect{W: 120, H: 18})
	buf := screen.PaintTestBuffer(120, 18)
	var painted strings.Builder
	for y := range 18 {
		painted.WriteString(shellchat.PlainRow(buf, y, 120))
		painted.WriteByte('\n')
	}
	for _, want := range []string{"Ran 1 command", "git status --short", "main.go"} {
		if !strings.Contains(painted.String(), want) {
			t.Fatalf("painted chat missing %q:\n%s", want, painted.String())
		}
	}
	if strings.Contains(painted.String(), `{"results"`) || strings.Contains(painted.String(), `\nM`) {
		t.Fatalf("painted chat leaked raw batch response:\n%s", painted.String())
	}
}

func TestRetainedToolLeavesPaintQuietlyWithColoredActivity(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight(), theme.DefaultWarm(), theme.DefaultMidnight(), theme.DefaultHighContrast()} {
		for _, width := range []int{48, 120} {
			r := chatprojection.NewRenderer(th)
			r.SetSize(width-4, 40)
			r.SetChatAreaBounds(0, 40)
			r.SyncMessages([]types.ChatMessage{
				{InternalID: "read", ToolRoundID: "previous", Role: types.RoleTool, ToolName: "read_file", ToolArgs: "src/source.go", ToolStatus: types.ToolStatusSuccess, ToolPreviewPhase: types.ToolPreviewDone},
				{InternalID: "command", ToolRoundID: "previous", Role: types.RoleTool, ToolName: "execute_command", ToolCommand: "go test ./tui/chatprojection ./tui/screens/shellchat", ToolStatus: types.ToolStatusSuccess, ToolPreviewPhase: types.ToolPreviewDone},
				{InternalID: "active", ToolRoundID: "current", Role: types.RoleTool, ToolName: "execute_command", ToolCommand: "go test ./...", ToolStatus: types.ToolStatusCalling, ToolPreviewPhase: types.ToolPreviewStreaming},
				{InternalID: "failed", ToolRoundID: "current", Role: types.RoleTool, ToolName: "exec_command", ToolCommand: "fixture --fail", ToolStatus: types.ToolStatusError, ToolPreviewPhase: types.ToolPreviewError, Content: `{"exit_code":2,"output":"fixture failed"}`},
			}, 1, 0)
			rows, _, height := r.ExportMessageRows(width - 4)
			s := shellchat.New(th)
			s.SetMessages(rows, shellchat.PaintParams{Compose: shellchat.ComposeParams{InnerW: width - 4, PadLR: 2, Theme: th}})
			s.SyncScrollMetrics(height, 40)
			s.Layout(ui.LayoutContext{}, ui.Rect{W: width, H: 40})
			buf := s.PaintTestBuffer(width, 40)
			tokens := ui.NewTokenSet(th)
			if tokens.Color(ui.ColorToolTitle) == tokens.Color(ui.ColorToolOperand) || tokens.Color(ui.ColorToolOperand) == tokens.Color(ui.ColorMuted) || tokens.Color(ui.ColorToolOperand) == tokens.Color(ui.ColorFg) || tokens.Color(ui.ColorToolTitle) == tokens.Color(ui.ColorError) {
				t.Fatalf("theme %s lost distinct tool roles", th.Name)
			}
			completed, active, failed := 0, 0, 0
			for y := range 40 {
				text := shellchat.PlainRow(buf, y, width)
				if !strings.Contains(text, "├─") && !strings.Contains(text, "└─") && !strings.Contains(text, "Command failed") {
					continue
				}
				for _, label := range []string{"Read ", "Ran ", "Running ", "Command failed "} {
					pos := strings.Index(text, label)
					if pos < 0 {
						continue
					}
					want := ui.ColorToolTitle
					dotWant := ui.ColorToolTitle
					switch label {
					case "Running ":
						active++
					case "Command failed ":
						want, dotWant = ui.ColorError, ui.ColorError
						failed++
					default:
						completed++
					}
					x := rendering.VisibleWidth(text[:pos])
					if got := buf.Get(x, y).FG; got != tokens.Color(want) {
						t.Fatalf("width %d, %q painted %v instead of %v", width, label, got, tokens.Color(want))
					}
					operandX := rendering.VisibleWidth(text[:pos+len(label)])
					if got := buf.Get(operandX, y).FG; got != tokens.Color(ui.ColorToolOperand) {
						t.Fatalf("theme %s width %d operand recolored by lifecycle: %s", th.Name, width, text)
					}
					dot := strings.IndexAny(text, "●○×")
					if dot < 0 || buf.Get(rendering.VisibleWidth(text[:dot]), y).FG != tokens.Color(dotWant) {
						t.Fatalf("width %d lost colored outcome/activity dot: %s", width, text)
					}
					if (label == "Command failed ") != strings.Contains(text, "×") {
						t.Fatalf("failure icon missing or used for normal work: %s", text)
					}
				}
			}
			if completed != 2 || active != 1 || failed != 1 {
				t.Fatalf("width %d: expected two completed, one active and one failed leaf, got %d/%d/%d", width, completed, active, failed)
			}
		}
	}
}

func TestMessageAtLocalYResolvesBottomGravityAndScroll(t *testing.T) {
	th := theme.DefaultDark()
	s := shellchat.New(th)
	rows := []chatprojection.MessageRow{{ID: "user-1", Lines: []string{"one", "two"}}, {ID: "assistant", Lines: []string{"three"}}}
	s.SetMessages(rows, shellchat.PaintParams{Compose: shellchat.ComposeParams{InnerW: 30, PadLR: th.Spacing.PadLR, Theme: th}})
	s.SyncScrollMetrics(6, 3)
	s.Layout(ui.LayoutContext{}, ui.Rect{W: 34, H: 6})
	if _, ok := s.MessageAtLocalY(1); ok {
		t.Fatal("blank gravity row resolved to a message")
	}
	row, ok := s.MessageAtLocalY(3)
	if !ok || row.ID != "user-1" {
		t.Fatalf("row=%+v ok=%v", row, ok)
	}
	row, ok = s.MessageAtLocalY(5)
	if !ok || row.ID != "assistant" {
		t.Fatalf("row=%+v ok=%v", row, ok)
	}
}

func TestContextBoundaryPaintsAccentRulesOnChatBackground(t *testing.T) {
	th := theme.DefaultDark()
	s := shellchat.New(th)
	lines := []string{"", "Context  Compaction", "Stage  Capturing state", "Source  420K", ""}
	native := make([]chatprojection.PaintLine, len(lines))
	for i, line := range lines {
		native[i] = chatprojection.PaintLine{Spans: []chatprojection.RowSpan{{Text: line, Role: ui.ColorFg}}}
	}
	row := chatprojection.MessageRow{ID: "compact", Lines: lines, Native: native, ContextBoundary: true}
	s.SetMessages([]chatprojection.MessageRow{row}, shellchat.PaintParams{Compose: shellchat.ComposeParams{InnerW: 36, PadLR: 2, Theme: th}})
	s.SyncScrollMetrics(5, 5)
	buf := s.PaintTestBuffer(40, 5)
	tokens := ui.NewTokenSet(th)
	wantBG := tokens.Color(ui.ColorBg)
	for y := 0; y < 5; y++ {
		for x := 0; x < 40; x++ {
			if got := buf.Get(x, y).BG; got != wantBG {
				t.Fatalf("cell (%d,%d) background=%v want context band", x, y, got)
			}
		}
	}
	for _, y := range []int{0, 4} {
		for x := 0; x < 40; x++ {
			got := buf.Get(x, y)
			if got.Char != '─' || got.FG != tokens.Color(ui.ColorAccent) {
				t.Fatalf("rule cell (%d,%d)=%+v want active-rail accent separator", x, y, got)
			}
		}
	}
}

func TestContextBoundaryPaintHonorsTextSelection(t *testing.T) {
	th := theme.DefaultDark()
	s := shellchat.New(th)
	lines := []string{"", "", "✓ Workspace restored", "2 files restored", "Conversation retained", ""}
	native := make([]chatprojection.PaintLine, len(lines))
	for i, line := range lines {
		native[i] = chatprojection.PaintLine{Spans: []chatprojection.RowSpan{{Text: line, Role: ui.ColorFg}}}
	}
	row := chatprojection.MessageRow{ID: "restore", Lines: lines, Native: native, ContextBoundary: true, ContextBoundaryStart: 1}
	s.SetMessages([]chatprojection.MessageRow{row}, shellchat.PaintParams{
		Compose:      shellchat.ComposeParams{InnerW: 36, PadLR: 2, Theme: th},
		HasSelection: true, SelStartLine: 1, SelStartCol: 1, SelEndLine: 3, SelEndCol: 8,
	})
	s.SyncScrollMetrics(len(lines), len(lines))
	buf := s.PaintTestBuffer(40, len(lines))
	tokens := ui.NewTokenSet(th)
	want := tokens.Color(ui.ColorTextSelection)
	// The top rule and a body row both visibly participate in one selection.
	if got := buf.Get(2+1, 1).BG; got != want {
		t.Fatalf("selected boundary rule bg=%v want selection", got)
	}
	if got := buf.Get(2+2, 2).BG; got != want {
		t.Fatalf("selected boundary body bg=%v want selection", got)
	}
}

func TestShellChatScrollModelStickToBottom(t *testing.T) {
	th := theme.DefaultDark()
	s := shellchat.New(th)
	scroll := s.ScrollModel()
	scroll.SetViewport(5)
	scroll.SetContent(20)
	scroll.SetAutoScroll(true)

	rows := make([]chatprojection.MessageRow, 5)
	for i := 0; i < 5; i++ {
		ch := string(rune('a' + i))
		rows[i] = chatprojection.MessageRow{
			ID:    ch,
			Lines: []string{ch},
			Native: []chatprojection.PaintLine{
				{Spans: []chatprojection.RowSpan{{Text: ch, Role: ui.ColorFg}}},
			},
		}
	}
	s.SetMessages(rows, shellchat.PaintParams{Compose: shellchat.ComposeParams{InnerW: 30, PadLR: th.Spacing.PadLR, Theme: th}})
	if scroll.Offset() != scroll.MaxOffset() {
		t.Fatalf("auto-scroll offset=%d want max=%d", scroll.Offset(), scroll.MaxOffset())
	}

	scroll.SetAutoScroll(false)
	scroll.ScrollTo(0)
	if handled := s.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnd})); !handled.Handled {
		t.Fatal("End key should be handled")
	}
	if !scroll.AutoScroll() || scroll.Offset() != scroll.MaxOffset() {
		t.Fatal("End should stick to bottom")
	}
}

func TestShellChatPgUpScrollsBoundModel(t *testing.T) {
	s := shellchat.New(theme.DefaultDark())
	scroll := s.ScrollModel()
	scroll.SetViewport(4)
	scroll.SetContent(20)
	scroll.StickToBottom()

	res := s.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: tea.KeyPgUp}))
	if !res.Handled {
		t.Fatal("PgUp should be handled")
	}
	if scroll.Offset() >= scroll.MaxOffset() {
		t.Fatalf("PgUp should scroll up from bottom, offset=%d max=%d", scroll.Offset(), scroll.MaxOffset())
	}
	if scroll.AutoScroll() {
		t.Fatal("PgUp should disarm auto-scroll")
	}
}
