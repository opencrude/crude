package capabilitypicker

import (
	"strings"
	"testing"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	captypes "crude/types/capability"
)

func TestPickerBrowsesWithoutExecutionAndRequiresCurrentPreview(test *testing.T) {
	snapshot := captypes.Snapshot{Entries: []captypes.Entry{{ID: "vendor:review", Name: "Review", Kind: captypes.KindSkill, State: "available"}}, Connections: []captypes.ConnectionState{{ID: "docs", State: "connected", Resources: []captypes.Resource{{URI: "docs://guide", Name: "Guide"}}, Prompts: []captypes.Prompt{{Name: "explain", Arguments: []captypes.PromptArgument{{Name: "topic", Required: true}}}}}}}
	screen := New(theme.DefaultDark(), snapshot)
	host := ui.NewHost(theme.DefaultDark(), screen)
	for _, size := range []ui.Size{{W: 60, H: 18}, {W: 90, H: 24}} {
		host.Resize(size.W, size.H)
		host.Layout()
		host.Paint(surface.NewCellBuffer(size.W, size.H))
		if screen.busy {
			test.Fatal("browsing started acquisition or tool execution")
		}
	}
	selected := screen.HandleAction(ui.EventContext{}, ui.Action{ID: "capability-picker.select"})
	if selected.Action == nil || selected.Action.Payload.(Selection).ID != "vendor:review" {
		test.Fatal("skill selection lost exact source-qualified identity")
	}
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "tabs.activate", Payload: "prompts"})
	screen.HandleAction(ui.EventContext{}, ui.Action{ID: "capability-picker.select"})
	if _, valid := screen.FetchAction(); valid {
		test.Fatal("missing required argument fetched a prompt")
	}
	screen.model.SetValue("arg/topic", ui.StringValue("review"))
	request, valid := screen.FetchAction()
	if !valid || request.Arguments["topic"] != "review" || request.Content != "explain" {
		test.Fatal("prompt identity/arguments were not preserved")
	}
	screen.model.SetValue("arg/topic", ui.StringValue("changed"))
	screen.Apply(`{"Content":[{"Text":"old answer"}]}`, nil)
	if screen.StageAction().Content != "" {
		test.Fatal("late preview attached after argument edit")
	}
	_, valid = screen.FetchAction()
	if !valid {
		test.Fatal("retry was unavailable")
	}
	screen.Apply(`{"Content":[{"Text":"external answer"}]}`, nil)
	if screen.StageAction().Content == "" || !strings.Contains(screen.reader.Value(), "external answer") {
		test.Fatal("successful exact preview could not be staged")
	}
}
