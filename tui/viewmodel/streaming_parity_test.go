package viewmodel

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"

	"crude/agent"
	"crude/terminalvt"
	"crude/tui/chatprojection"
	"crude/types"
	"crude/types/recovery"
	"crude/workspace"
)

const streamParityAnswer = "Original pixels stay sharp in supported terminals.\n\n### What changed\n- ALPHA pixels stay original.\n- BRAVO support is detected.\n- CHARLIE windows move and resize.\n\nSettings select where attachments open.\n\n### Verified\nDELTA desktop and narrow terminals.\n\nFor tmux:\n```tmux\nset -g allow-passthrough on\n```\n\nECHO rebuild and restart."

func TestNativeStreamingAssistantMatchesPresentedFrame(t *testing.T) {
	for _, width := range []int{80, 140} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			const height = 40
			app := newFullTestApp(t)
			wsOf(app).Logger = stubLogger{}
			wsOf(app).StartTime = time.Time{}
			app.Update(tea.WindowSizeMsg{Width: width, Height: height})
			app.Tree.Visible = false
			app.AddOrchestrator("root")
			for _, message := range seedBudgetSession(80, 0) {
				app.appendActiveMessage(message)
			}
			engine, err := terminalvt.New(terminalvt.Options{Cols: width, Rows: height})
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close()
			var output bytes.Buffer
			renderer := uv.NewTerminalRenderer(&output, []string{"TERM=xterm-kitty"})
			renderer.SetColorProfile(colorprofile.TrueColor)
			renderer.SetFullscreen(true)
			renderer.SetRelativeCursor(false)
			renderer.SetScrollOptim(true)
			renderer.SetTabStops(-1)
			frame := uv.NewScreenBuffer(width, height)
			present := func(stage string) {
				t.Helper()
				app.frameGate.lastPresented = time.Time{}
				app.presentImmediateFrame()
				view := app.View()
				want := uv.NewScreenBuffer(width, height)
				view.Surface.Draw(want, want.Bounds())
				frame.Replace(want.Buffer)
				renderer.Render(frame.RenderBuffer)
				if err := renderer.Flush(); err != nil {
					t.Fatal(err)
				}
				engine.Feed(output.Bytes())
				wire := output.String()
				output.Reset()
				actual, err := engine.Rows(terminalvt.RowRequest{Start: -1})
				if err != nil {
					t.Fatal(err)
				}
				for y, row := range actual.Cells {
					for x, got := range row {
						if expected := want.CellAt(x, y); strings.TrimSpace(got.Text) != strings.TrimSpace(expected.Content) {
							t.Fatalf("%s terminal text (%d,%d): got %q want %q; bytes=%q", stage, x, y, got.Text, expected.Content, wire)
						}
					}
				}
			}
			emit := func(kind string, data any) {
				app.Update(agent.AgentEvent{AgentID: "root", Type: kind, Data: data})
				present(kind)
			}
			present("dense history")
			emit("turn_started", types.TurnIdentity{Generation: 1, ModelID: "model"})
			emit("phase", map[string]any{"name": "waiting_provider"})
			for at := 0; at < len(streamParityAnswer); at += 5 {
				emit("stream_token", streamParityAnswer[at:min(at+5, len(streamParityAnswer))])
				app.Update(types.TickMsg(time.Now()))
				present("cursor tick")
			}
			emit("done", map[string]any{"content": streamParityAnswer, "reason": "completed"})
		})
	}
}

func TestStreamingAssistantRetainedPaintMatchesSource(t *testing.T) {
	for _, width := range []int{80, 140} {
		for _, settlement := range []string{"ordinary", "late-upload", "recovered"} {
			t.Run(fmt.Sprintf("width=%d/%s", width, settlement), func(t *testing.T) {
				app := newFullTestApp(t)
				app.Tree.Visible = false
				app.width, app.height = width, 60
				app.AddOrchestrator("root")
				state := app.ws.AgentMgr().Get("root").State
				emit := func(kind string, data any) {
					t.Helper()
					app.handleAgentDomainEvent(workspace.AgentEvent{AgentID: "root", Type: kind, Data: data})
					app.paintShellFrame()
				}
				emit("turn_started", types.TurnIdentity{Generation: 1, ModelID: "model"})
				status := recovery.Status{ID: "incident", AgentID: "root", Generation: 1, State: recovery.Recovering, StartedAt: time.Now(), UpdatedAt: time.Now(), Summary: "Retrying unfinished run"}
				if settlement == "recovered" {
					emit("recovery", status)
				}
				emit("phase", map[string]any{"name": "waiting_provider"})
				var accepted string
				for at := 0; at < len(streamParityAnswer); at += 13 {
					chunk := streamParityAnswer[at:min(at+13, len(streamParityAnswer))]
					accepted += chunk
					emit("stream_token", chunk)
					messages := state.Messages.Messages()
					if messages[len(messages)-1].Role != types.RoleAssistant || messages[len(messages)-1].Content != accepted {
						t.Fatalf("stream source differs at byte %d: %+v", at, messages)
					}
					assertStreamParityRows(t, app.activeMessageList().ExportMessageRowsSnapshot(app.chatReflowInnerW), accepted)
				}
				switch settlement {
				case "late-upload":
					emit("phase", map[string]any{"name": "waiting_provider"})
				case "recovered":
					status.State, status.Summary, status.UpdatedAt = recovery.Recovered, "Run recovered", time.Now()
					emit("recovery", status)
				}
				emit("done", map[string]any{"content": streamParityAnswer, "reason": "completed"})
				messages := state.Messages.Messages()
				var answers []string
				for _, message := range messages {
					if message.Role == types.RoleAssistant {
						answers = append(answers, message.Content)
					}
				}
				if len(answers) != 1 || answers[0] != streamParityAnswer {
					t.Fatalf("completion duplicated accepted response: %q", answers)
				}
				assertStreamParityRows(t, app.activeMessageList().ExportMessageRowsSnapshot(app.chatReflowInnerW), streamParityAnswer)
				if got := strings.Count(chatAreaText(app), "What changed"); got != 1 {
					t.Fatalf("painted heading count = %d, want one; chat=%q", got, chatAreaText(app))
				}
			})
		}
	}
}

func assertStreamParityRows(t *testing.T, snapshot chatprojection.MessageRowsSnapshot, accepted string) {
	t.Helper()
	var plain, native strings.Builder
	for _, row := range snapshot.Rows {
		plain.WriteString(strings.Join(row.Lines, "\n"))
		for _, line := range row.Native {
			native.WriteString(line.PlainText())
			native.WriteByte('\n')
		}
	}
	for _, marker := range []string{"What changed", "ALPHA", "BRAVO", "CHARLIE", "Verified", "DELTA", "ECHO"} {
		if strings.Contains(accepted, marker) {
			for _, text := range []string{plain.String(), native.String()} {
				if got := strings.Count(text, marker); got != 1 {
					t.Fatalf("rendered %q count=%d, want one; source=%q rendered=%q", marker, got, accepted, text)
				}
			}
		}
	}
}
