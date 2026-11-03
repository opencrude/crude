package viewmodel

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/screens/statusoverview"
	"crude/tui/screens/toolsetup"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
	"github.com/charmbracelet/x/ansi"
)

type projectToolsTestPort struct {
	shellport.Workspace
	settings types.AppSettings
	preview  types.ProjectToolSetupReview
	reads    int
	explicit []bool
	choices  [][]string
}

func (p *projectToolsTestPort) LoadAppSettings() types.AppSettings { return p.settings }
func (p *projectToolsTestPort) ReviewProjectTools(ctx context.Context, explicit bool) (types.ProjectToolSetupReview, error) {
	if err := ctx.Err(); err != nil {
		return types.ProjectToolSetupReview{}, err
	}
	p.reads++
	p.explicit = append(p.explicit, explicit)
	return p.preview, nil
}
func (p *projectToolsTestPort) DecideProjectTools(ctx context.Context, id string, selected []string) (types.ProjectToolSetupResult, error) {
	if err := ctx.Err(); err != nil {
		return types.ProjectToolSetupResult{}, err
	}
	if id != p.preview.ID {
		return types.ProjectToolSetupResult{}, fmt.Errorf("foreign preview")
	}
	p.choices = append(p.choices, append([]string(nil), selected...))
	return types.ProjectToolSetupResult{DecisionID: "decision", Message: "Your tool choice was saved."}, nil
}

func projectToolsTestApp(t *testing.T, width, height int) (*App, *projectToolsTestPort) {
	t.Helper()
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	port := &projectToolsTestPort{Workspace: app.ws, settings: app.ws.LoadAppSettings(), preview: types.ProjectToolSetupReview{
		ID: "native-preview", NeedsDecision: true,
		Review: types.ProjectToolReview{Project: app.ws.RootPath(), Token: "reviewed", Items: []types.ProjectToolCandidate{
			{ID: "server", Package: "fixture-server", Version: "v1", URL: "https://example.invalid/server.zip",
				File: "server.zip", Command: "fixture-server", Installable: true, Licenses: []string{"MIT"}},
		}},
	}}
	port.settings.Analysis.Trust = types.AnalysisTrusted
	app.ws = port
	app.Update(tea.WindowSizeMsg{Width: width, Height: height})
	return app, port
}

func TestProjectToolsNativeJourneySafeDefaultAndHumanOnlyConsent(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			app, port := projectToolsTestApp(t, size[0], size[1])
			_, cmd := app.Update(statusoverview.NavigateMsg{Action: "lsp:setup"})
			if cmd == nil || port.reads != 0 {
				t.Fatal("request blocked Update or was not scheduled")
			}
			runAppCommand(t, app, cmd)
			if len(port.explicit) != 1 || !port.explicit[0] {
				t.Fatal("Set up tools did not request a fresh explicit review", port.explicit)
			}
			window := app.overlayHost.Window(projectToolsOverlay)
			if window == nil {
				t.Fatal("native review not mounted")
			}
			paint := ansi.Strip(app.View().RenderContent())
			for _, want := range []string{"Set up code intelligence", "Install selected", "Not now"} {
				if !strings.Contains(paint, want) {
					t.Fatalf("missing %q at %v:\n%s", want, size, paint)
				}
			}
			if len(port.choices) != 0 {
				t.Fatal("painting a review installed tools")
			}
			effect, human, err := app.describeLocalControl(shellControlHost{window: window},
				toolsetup.ActionsID, toolsetup.ActionInstall, ui.ControlOperation{Verb: "activate"})
			if err != nil || !human || effect != control.Write {
				t.Fatal("generic control could manufacture human installation consent", effect, human, err)
			}
			_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			runAppCommand(t, app, cmd)
			if len(port.choices) != 1 || len(port.choices[0]) != 0 || app.overlayHost.IsOpen(projectToolsOverlay) {
				t.Fatal("safe default installed tools or failed to save Not now", port.choices)
			}
			_, cmd = app.Update(statusoverview.NavigateMsg{Action: "lsp:setup"})
			runAppCommand(t, app, cmd)
			if len(port.explicit) != 2 || !port.explicit[1] {
				t.Fatal("Set up tools reused automatic review after Not now", port.explicit)
			}
			app.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
			_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			runAppCommand(t, app, cmd)
			if len(port.choices) != 2 || len(port.choices[1]) != 1 || port.choices[1][0] != "server" {
				t.Fatal("native Install did not preserve reviewed selection", port.choices)
			}
		})
	}
}

func TestProjectToolsQuietAndStaleReviewsNeverInterruptOrInstall(t *testing.T) {
	app, port := projectToolsTestApp(t, 100, 30)
	port.preview.Quiet = true
	runAppCommand(t, app, app.requestProjectTools(false))
	if app.overlayHost.IsOpen(projectToolsOverlay) || len(port.choices) != 0 {
		t.Fatal("new candidate interrupted editing or installed without a decision")
	}
	cmd := app.requestProjectTools(true)
	message := cmd()
	app.workspaceSwitchGen.Add(1)
	app.handleProjectToolsMessage(message)
	if app.overlayHost.IsOpen(projectToolsOverlay) || len(port.choices) != 0 {
		t.Fatal("stale review opened in a replacement workspace")
	}
	app.closeProjectTools()
}

func TestProjectToolsReadyTickActuallyRunsNativeReview(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			app, port := projectToolsTestApp(t, size[0], size[1])
			manager := shellport.Unwrap(port.Workspace).LSPMgr
			manager.SetWorkspaceRoot(t.TempDir())
			manager.RefreshAssembly()
			deadline := time.NewTimer(5 * time.Second)
			defer deadline.Stop()
			for manager.Assembly().State != types.AssemblyReady {
				select {
				case <-deadline.C:
					t.Fatal("project discovery did not become ready")
				case <-time.After(time.Millisecond):
				}
			}
			var deliver func(tea.Cmd)
			deliver = func(cmd tea.Cmd) {
				if cmd == nil {
					return
				}
				switch msg := cmd().(type) {
				case tea.BatchMsg:
					for _, child := range msg {
						deliver(child)
					}
				case types.TickMsg:
					// A clock result belongs to the next frame, not this
					// single-generation review acceptance.
				case projectToolsReviewedMsg:
					_, next := app.Update(msg)
					deliver(next)
				}
			}
			_, cmd := app.Update(types.TickMsg(time.Now()))
			if port.reads != 0 {
				t.Fatal("the native review ran synchronously on Update")
			}
			deliver(cmd)
			paint := ansi.Strip(app.View().RenderContent())
			if port.reads != 1 || !app.overlayHost.IsOpen(projectToolsOverlay) ||
				!strings.Contains(paint, "Set up code intelligence") {
				t.Fatalf("ready project did not deliver its native review: reads=%d\n%s", port.reads, paint)
			}
			if len(port.choices) != 0 {
				t.Fatal("automatic review installed tools without a human decision")
			}
			if len(port.explicit) != 1 || port.explicit[0] {
				t.Fatal("a ready tick requested explicit retry authority", port.explicit)
			}
		})
	}
}
