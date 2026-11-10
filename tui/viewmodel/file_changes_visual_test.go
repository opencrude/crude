package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/types"
	"crude/workspace"
)

// Run sequentially through scripts/tuishot.sh. No model or user project is used.
// Ordinary Delete/Yes keys perform the delete. F4 only observes the result;
// F9 exits and requires that observation to have succeeded.
func TestFileChangesVisualAcceptance(t *testing.T) {
	mode := os.Getenv("CRUDE_VISUAL_FILE_CHANGES")
	if mode != "delete" && mode != "chat" && mode != "progress" {
		t.Skip("dedicated sequential native capture")
	}
	root := t.TempDir()
	var app *App
	var target string
	var release chan struct{}
	switch mode {
	case "progress":
		target = filepath.Join(root, ".claude")
		if err := os.Mkdir(target, 0700); err != nil {
			t.Fatal(err)
		}
		app = newEditorPaintApp(t, root)
		wsOf(app).RootPath = root
		wsOf(app).FileOps = workspace.NewFileOpsService(root, nil, stubLogger{})
		release = make(chan struct{})
		app.ws = &blockedTreeDelete{Workspace: app.ws, release: release}
		app.setActivity(activityFiles, true)
		app.Tree.Model.Select(target)
		app.setFocusTree()
	case "delete":
		target = filepath.Join(root, ".hidden", ".cache")
		if err := os.MkdirAll(target, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(target, "content"), []byte("recoverable content"), 0600); err != nil {
			t.Fatal(err)
		}
		shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, "http://unused.invalid"), root)
		app = shell.app
		runAppCommand(t, app, app.beginClientFileTreeLoad())
		runAppCommand(t, app, app.Tree.Model.Expand(filepath.Dir(target)))
		app.setActivity(activityFiles, true)
		app.Tree.Model.Select(target)
		app.setFocusTree()
	default:
		app = newEditorPaintApp(t, root)
		app.AddOrchestrator("file-changes")
		state := app.ws.AgentMgr().Get("file-changes").State
		state.Messages.AppendMessage(workspace.Message{Role: types.RoleUser, Content: "Show the plan and file changes."})
		for _, item := range []struct{ id, name, args, result string }{
			{"plan", "update_plan", `{}`, `{"id":"plan-1","revision":1,"objective":"Make file changes visible","status":"active","steps":[{"id":"one","description":"Show the full plan","status":"completed"},{"id":"two","description":"Show each file diff","status":"in_progress"}]}`},
			{"write", "write_file", `{"path":"new.go","content":"package demo\n"}`, "```diff\n--- /dev/null\n+++ b/new.go\n@@ -0,0 +1 @@\n+package demo\n```"},
			{"edit", "edit_file", `{"path":"main.go"}`, "```diff\n--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old_value()\n+edited_value()\n```"},
			{"patch", "apply_patch", `{"patch":"*** Begin Patch\n*** Update File: other.go\n*** End Patch"}`, "```diff\n--- a/other.go\n+++ b/other.go\n@@ -1 +1 @@\n-before_patch()\n+after_patch()\n```"},
		} {
			state.ApplyEvent(workspace.AgentEvent{Type: "tool_call", Data: map[string]any{"tool_id": item.id, "internal_id": item.id, "name": item.name, "args": item.args}})
			state.ApplyEvent(workspace.AgentEvent{Type: "tool_result", Data: map[string]any{"tool_id": item.id, "internal_id": item.id, "name": item.name, "result": item.result}})
		}
		for i := range 4 {
			state.Messages.AdvanceToolLifecycles(time.Now().Add(time.Duration(i+1) * time.Second))
		}
		app.syncAgentMessages(state.ID, false)
	}
	app.onboarding.trust.askedOwner = app.ws
	app.onboarding.trust.askedGen = app.workspaceSwitchGen.Load()
	app.ApplyThemePreset("dark")
	model := &fileChangesVisualModel{App: app, t: t, target: target, release: release}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	app.StopAsyncIO()
	if !model.checked || mode == "progress" && !model.progressChecked {
		t.Fatal("visual observation did not succeed")
	}
}

type fileChangesVisualModel struct {
	*App
	t               *testing.T
	target          string
	checked         bool
	release         chan struct{}
	progressChecked bool
}

func (*fileChangesVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *fileChangesVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF3:
			if m.release != nil {
				close(m.release)
				m.release = nil
			}
			return m, nil
		case tea.KeyF4:
			if m.release != nil {
				row := rowText(m.App, m.height-1)
				if !strings.Contains(row, "Keeping safety copy of .claude") {
					m.t.Errorf("running delete progress is absent: %q", row)
				}
				m.progressChecked = true
				return m, nil
			}
			if m.target != "" {
				requireMissing(m.t, m.target)
				assertTreeContainsPath(m.t, m.Tree.Model, m.target, false)
			} else {
				rows, _, _ := m.subPaneProjections.GetExisting("file-changes").renderer.ExportMessageRows(m.width)
				var text strings.Builder
				for _, row := range rows {
					text.WriteString(strings.Join(row.Lines, "\n"))
				}
				for _, want := range []string{"Show the full plan", "package demo", "edited_value()", "after_patch()"} {
					if !strings.Contains(text.String(), want) {
						m.t.Errorf("default transcript did not show %q", want)
					}
				}
			}
			m.checked = true
			return m, nil
		case tea.KeyF9:
			if m.release != nil {
				close(m.release)
				m.release = nil
				m.t.Error("delete was not allowed to finish")
			}
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
