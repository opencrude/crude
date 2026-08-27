package viewmodel

import (
	"strings"
	"testing"

	"crude/provider"
	"crude/tui/rendering"
)

func TestSlashMutationsRejectActiveTurnWithoutStartingAsyncWork(t *testing.T) {
	app := newFullTestApp(t)
	app.Logger = stubLogger{}
	app.StatusBar.SetWidth(120)
	app.AddOrchestrator(
		"agent",
		make(chan provider.UserInput, 1),
		make(chan provider.UserInput, 1),
		make(chan struct{}, 1),
		make(chan string, 1),
	)
	node := app.AgentMgr.Get("agent")
	node.State.Running = true
	node.State.Thinking = true

	for _, command := range []string{"/undo", "/compact"} {
		if cmd := app.executeSlashCommand(command); cmd != nil {
			t.Fatalf("%s scheduled backend work during an active turn", command)
		}
		status := rendering.StripAnsi(app.StatusBar.View())
		if !strings.Contains(status, "Stop the active turn") {
			t.Fatalf("%s status = %q", command, status)
		}
	}
}

func TestYoloSlashRemainsAvailableDuringActiveTurn(t *testing.T) {
	app := newFullTestApp(t)
	app.Logger = stubLogger{}
	app.AddOrchestrator(
		"agent",
		make(chan provider.UserInput, 1),
		make(chan provider.UserInput, 1),
		make(chan struct{}, 1),
		make(chan string, 1),
	)
	node := app.AgentMgr.Get("agent")
	node.State.Running = true
	node.State.Thinking = true

	if cmd := app.executeSlashCommand("/YOLO"); cmd != nil {
		t.Fatal("/yolo prompt should open synchronously")
	}
	projection := app.activeSessionProjection()
	if projection == nil || projection.deck.Active() == nil || projection.deck.Active().ID != sessionYoloPanelID {
		t.Fatal("/yolo did not open its confirmation during the active turn")
	}
}
