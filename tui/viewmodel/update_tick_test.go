package viewmodel

import (
	"crude/tui/rendering"
	"crude/types/command"
	"crude/types/setting"
	"crude/workspace"
	"crude/workspace/fileio"
	"strings"
	"testing"
	"time"
)

// The idle launcher previously scheduled the 16 ms streaming tick forever
// (AgentMgr.Count()==0 && ActiveView==""), repainting full frames at ~60 Hz
// even with nothing animating: the "floating terminal burns 26% CPU" storm.
// The fast tick is reserved for real streaming/reveal activity.
func TestWantsFastTick(t *testing.T) {
	app := newTestApp(t)
	if app.wantsFastTick() {
		t.Fatal("idle launcher must use the normal 400ms tick, not the 16ms streaming tick")
	}

	app.reveals = map[string]revealAnim{"popup": {}}
	if !app.wantsFastTick() {
		t.Fatal("mid-flight popup reveal must keep the 16ms streaming tick")
	}
	delete(app.reveals, "popup")
	if app.wantsFastTick() {
		t.Fatal("after the reveal completes the fast tick must stop")
	}
}

func TestOnlyMountedRunningJobsRequestArcCadence(t *testing.T) {
	app := newFullTestApp(t)
	session := &workspace.Session{SessionID: "bar", Settings: workspace.NewSessionSettingsState(setting.Settings{}, setting.Settings{}, nil)}
	selectDeckSession(t, app, session, "bar-agent")
	app.width, app.height = 100, 30
	app.compositor.Resize(100, 30)
	app.Tree.Visible = false
	app.View()
	if app.wantsFastTick() {
		t.Fatal("baseline unexpectedly animating")
	}
	now := time.Now()
	job := fileio.AsyncCmdInfo{Handle: "test", Command: "go test", Status: fileio.AsyncRunning, StartedAt: now}
	show := func() string {
		app.presentAgentNotice(workspace.AgentNotice{Type: workspace.NoticeAsyncCommandStatus, AgentID: "bar-agent", Commands: []fileio.AsyncCmdInfo{job}})
		app.forceFrame()
		return rendering.StripAnsi(app.View().RenderContent())
	}
	onBoundary := time.UnixMilli(1600)
	show()
	if app.wantsFastTick() || app.nextTickDelay(onBoundary) != workspace.IdleTickInterval {
		t.Fatal("an ordinary shell command should use the shared blink cadence")
	}
	job.TaskID, job.Handle, job.Command = "fixture", "auto-1", "Tool"
	job.Progress = command.Progress{Phase: "Downloading skill"}
	show()
	if app.wantsFastTick() || app.nextTickDelay(onBoundary) != 160*time.Millisecond || app.nextTickDelay(onBoundary.Add(60*time.Millisecond)) != 100*time.Millisecond {
		t.Fatal("visible arc did not request discrete steps of the shared clock")
	}
	job.Progress.Current, job.Progress.Total = 37, 100
	if painted := show(); !strings.Contains(painted, "37%") {
		t.Fatalf("new measured frame did not display: %s", painted)
	}
	if app.wantsFastTick() || app.nextTickDelay(onBoundary) != 160*time.Millisecond {
		t.Fatal("a measured transfer changed the arc cadence")
	}
	job.Progress = command.Progress{Phase: "Verifying skill files"}
	show()
	if app.nextTickDelay(onBoundary) != workspace.IdleTickInterval {
		t.Fatal("verification kept the download cadence")
	}
	job.Progress = command.Progress{Phase: "Connecting"}
	show()
	if app.nextTickDelay(onBoundary) != 160*time.Millisecond {
		t.Fatal("a visible connection did not request an arc")
	}
	app.openSessionSettings()
	app.forceFrame()
	app.View()
	if app.nextTickDelay(onBoundary) != workspace.IdleTickInterval {
		t.Fatal("hidden job kept the arc clock")
	}
}
