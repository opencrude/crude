package viewmodel

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/workspace"
)

func TestComposerTallDraftPreservesChatAndBounds(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}, {60, 16}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			app := newFullTestApp(t)
			wsOf(app).Logger = stubLogger{}
			app.width, app.height = size[0], size[1]
			app.AddOrchestrator("owner")
			app.maybeHydrateComposerDraft()
			app.ws.AgentMgr().Get("owner").State.Messages.AppendMessage(workspace.Message{
				Role: workspace.MessageRoleAssistant, Content: "The transcript must remain visible while drafting.",
			})
			app.syncAgentMessages("owner", false)
			draft := strings.Repeat("A draft line with substantial detail.\n", 60) + "EXACT-DRAFT-END"
			app.Input.SetInput(draft)
			for frame := 0; frame < 2; frame++ {
				app.paintShellFrame()
				if app.inputRect.H > 7 || app.inputRect.H > (app.height-app.chromeTopRow()-app.chromeBottomRows())/3 {
					t.Fatalf("composer swallowed the conversation: input=%+v chat=%+v", app.inputRect, app.chatRect)
				}
				painted := bufferPlain(app.mainSurf.Buffer())
				if app.chatRect.H < 5 || !strings.Contains(painted, "transcript") || !strings.Contains(painted, "drafting.") {
					t.Fatal("growing draft hid the readable transcript")
				}
				if app.inputRect.Y+app.inputRect.H > app.height-app.chromeBottomRows() || app.Input.Value() != draft {
					t.Fatal("composer overflowed footer or changed draft")
				}
				if !strings.Contains(bufferPlain(app.mainSurf.Buffer()), "EXACT-DRAFT-END") {
					t.Fatal("limited textarea did not keep cursor row visible")
				}
			}
			_, _ = app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
			app.paintShellFrame()
			if app.Input.Value() != draft+"x" || app.inputRect.H > 7 {
				t.Fatal("typing in clipped draft changed content or regrew the shell")
			}
		})
	}
}
