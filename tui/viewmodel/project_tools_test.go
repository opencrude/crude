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
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
	"github.com/charmbracelet/x/ansi"
)

// The title ✕ closes the setup window with the pointer: it neither drags the
// window nor installs anything, and the original shell gets input back.
func TestProjectToolsCloseButtonClosesWithoutDeciding(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			app, port := projectToolsTestApp(t, size[0], size[1])
			_, cmd := app.Update(statusoverview.NavigateMsg{Action: "lsp:setup"})
			runAppCommand(t, app, cmd)
			if !app.overlayHost.IsOpen(projectToolsOverlay) {
				t.Fatal("setup window not mounted")
			}
			x, y := -1, -1
			for row, line := range strings.Split(ansi.Strip(app.View().RenderContent()), "\n") {
				if col := strings.Index(line, "Set up code intelligence"); col >= 0 {
					if at := strings.Index(line, "✕"); at > col {
						x, y = len([]rune(line[:at])), row
					}
				}
			}
			if x < 0 {
				t.Fatal("the title shows no close button")
			}
			app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
			app.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
			if app.overlayHost.IsOpen(projectToolsOverlay) {
				t.Fatal("the close button left the setup window open")
			}
			if len(port.choices) != 0 || len(port.roles) != 0 {
				t.Fatal("closing the window decided or tasked something", port.choices, port.roles)
			}
		})
	}
}

type projectToolsTestPort struct {
	shellport.Workspace
	settings   types.AppSettings
	preview    types.ProjectToolSetupReview
	reads      int
	explicit   []bool
	selections []types.ProjectToolSetupRequest
	choices    [][]string
	roles      []string
	onEnsure   func()
	// busy answers that many ensure requests with busy admission first.
	busy int
}

// NewAgentSessionAs records the role instead of opening a real session.
func (p *projectToolsTestPort) NewAgentSessionAs(role string) *workspace.NewSessionResult {
	p.roles = append(p.roles, role)
	return &workspace.NewSessionResult{SessionID: "aeron-session", OrchID: "aeron-agent"}
}

// EnsureAeronSession follows the same test path: the setup action must reuse
// the workspace's one Aeron, never create a second identity.
func (p *projectToolsTestPort) EnsureAeronSession() *workspace.NewSessionResult {
	if p.busy > 0 {
		p.busy--
		return &workspace.NewSessionResult{Error: "bounded workspace operation capacity is occupied", Busy: true}
	}
	if p.onEnsure != nil {
		p.onEnsure()
	}
	return p.NewAgentSessionAs("aeron")
}

func (p *projectToolsTestPort) LoadAppSettings() types.AppSettings { return p.settings }
func (p *projectToolsTestPort) ReviewProjectTools(ctx context.Context, selection types.ProjectToolSetupRequest) (types.ProjectToolSetupReview, error) {
	if err := ctx.Err(); err != nil {
		return types.ProjectToolSetupReview{}, err
	}
	p.reads++
	p.explicit = append(p.explicit, selection.Explicit)
	p.selections = append(p.selections, selection)
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
			for _, want := range []string{"Set up code intelligence", "fixture-server", "Set up with Aeron", "Not now"} {
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
			// Aeron does the work: Crude only records that the user answered and
			// installs nothing itself.
			if len(port.choices) != 2 || len(port.choices[1]) != 0 {
				t.Fatal("Set up with Aeron made Crude install tools itself", port.choices)
			}
			if len(port.roles) != 1 || port.roles[0] != "aeron" {
				t.Fatal("Set up with Aeron did not open the workspace's Aeron", port.roles)
			}
			pending := app.pendingFirstInputs["aeron-agent"][0]
			order := pending.Text
			for _, want := range []string{"<set_up>", `package="fixture-server"`, "<leave_alone/>", "lsp.languages.&lt;id&gt;.command"} {
				if !strings.Contains(order, want) {
					t.Fatalf("work order for Aeron lacks %q:\n%s", want, order)
				}
			}
			// The chat shows the request in plain words, not the XML order.
			if pending.DisplayText != "Set up 1 code intelligence tool: fixture-server." {
				t.Fatalf("work order shows %q in the chat", pending.DisplayText)
			}
			if app.overlayHost.IsOpen(projectToolsOverlay) {
				t.Fatal("the setup window stayed open after Set up with Aeron")
			}
		})
	}
}

func TestProjectToolsQuietAndStaleReviewsNeverInterruptOrInstall(t *testing.T) {
	app, port := projectToolsTestApp(t, 100, 30)
	port.preview.Quiet = true
	runAppCommand(t, app, app.requestProjectTools(types.ProjectToolSetupRequest{}, nil))
	if app.overlayHost.IsOpen(projectToolsOverlay) || len(port.choices) != 0 {
		t.Fatal("new candidate interrupted editing or installed without a decision")
	}
	cmd := app.requestProjectTools(types.ProjectToolSetupRequest{Explicit: true}, nil)
	message := cmd()
	app.workspaceSwitchGen.Add(1)
	app.handleProjectToolsMessage(message)
	if app.overlayHost.IsOpen(projectToolsOverlay) || len(port.choices) != 0 {
		t.Fatal("stale review opened in a replacement workspace")
	}
	app.closeProjectTools()
}

func TestProjectToolsCloseAndUnavailableReview(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			app, port := projectToolsTestApp(t, size[0], size[1])
			for _, closeWithX := range []bool{true, false} {
				runAppCommand(t, app, app.requestProjectTools(types.ProjectToolSetupRequest{Explicit: true}, nil))
				window := app.overlayHost.Window(projectToolsOverlay)
				if window == nil {
					t.Fatal("review did not open")
				}
				// The mounted product path must handle the visible title X, not
				// treat it as a window drag. Escape must also leave no decision.
				if closeWithX {
					x, y := window.Position()
					w, _ := window.Size()
					_, cmd := app.Update(tea.MouseClickMsg{X: x + w - 2, Y: y, Button: tea.MouseLeft})
					runAppCommand(t, app, cmd)
				} else {
					_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
					runAppCommand(t, app, cmd)
				}
				if app.overlayHost.IsOpen(projectToolsOverlay) || app.projectTools.request != nil || len(port.choices) != 0 {
					t.Fatalf("close x=%v retained the review or submitted a choice: %v", closeWithX, port.choices)
				}
			}

			// A tool that Crude cannot download itself is still a real choice:
			// Aeron finds another way to get it, so the review offers it.
			port.preview.Review.Items = []types.ProjectToolCandidate{
				{ID: "manager", Package: "manager-only", Reason: "Install it yourself, then check again."},
			}
			runAppCommand(t, app, app.requestProjectTools(types.ProjectToolSetupRequest{}, nil))
			if !app.overlayHost.IsOpen(projectToolsOverlay) || len(port.choices) != 0 {
				t.Fatal("a tool that only Aeron can get was not offered, or a choice was submitted")
			}
			paint := ansi.Strip(app.View().RenderContent())
			for _, want := range []string{"[x]  manager-only", "Set up with Aeron"} {
				if !strings.Contains(paint, want) {
					t.Fatalf("missing %q:\n%s", want, paint)
				}
			}
			if strings.Contains(paint, "Not available") || strings.Contains(paint, "Install it yourself") {
				t.Fatalf("review tells the user to install the tool by hand:\n%s", paint)
			}
			app.closeProjectTools()
		})
	}
}

func TestProjectToolsMixedSelectionMountedJourney(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			app, port := projectToolsTestApp(t, size[0], size[1])
			port.preview.Review.Items = append(port.preview.Review.Items,
				types.ProjectToolCandidate{ID: "second", Package: "second-server", Version: "v2", URL: "https://example.invalid/second.zip", Command: "second", Installable: true},
				types.ProjectToolCandidate{ID: "missing", Package: "missing-server", Reason: "Install this tool yourself, then check again."})
			runAppCommand(t, app, app.requestProjectTools(types.ProjectToolSetupRequest{Explicit: true}, nil))
			window := app.overlayHost.Window(projectToolsOverlay)
			if window == nil {
				t.Fatal("review did not open")
			}
			app.View()
			widget, ok := window.Content.(*ui.WindowFrameWidget)
			if !ok {
				t.Fatal("review is not a native window")
			}
			list, ok := widget.Host().ScreenRect(toolsetup.ListID)
			if !ok || list.H < 3 {
				t.Fatalf("candidate list is not visible: %+v", list)
			}
			x, y := window.Position()
			// A pointer click deselects the second tool without changing the
			// first. The row, not an internal form value, owns the interaction.
			_, cmd := app.Update(tea.MouseClickMsg{X: x + list.X + 3, Y: y + list.Y + 1, Button: tea.MouseLeft})
			runAppCommand(t, app, cmd)
			app.Update(tea.MouseReleaseMsg{X: x + list.X + 3, Y: y + list.Y + 1, Button: tea.MouseLeft})
			if text := ansi.Strip(app.View().RenderContent()); !strings.Contains(text, "[ ]  second-server") ||
				!strings.Contains(text, "[x]  fixture-server") || !strings.Contains(text, "[x]  missing-server") {
				t.Fatalf("the click did not clear exactly the second tool:\n%s", text)
			}
			// Tab reaches the complete reader and then the action bar.
			app.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			app.Update(tea.KeyPressMsg{Code: tea.KeyPgDown})
			app.Update(tea.KeyPressMsg{Code: tea.KeyTab})
			app.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
			_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			runAppCommand(t, app, cmd)
			// Aeron gets the exact selection; Crude records the answer only.
			if len(port.choices) != 1 || len(port.choices[0]) != 0 || len(port.roles) != 1 {
				t.Fatalf("Set up with Aeron did not hand the work to Aeron: choices=%v roles=%v", port.choices, port.roles)
			}
			order := app.pendingFirstInputs["aeron-agent"][0].Text
			setUp, leaveAlone, ok := strings.Cut(order, "<leave_alone>")
			if !ok || !strings.Contains(setUp, `package="fixture-server"`) || !strings.Contains(setUp, `package="missing-server"`) ||
				strings.Contains(setUp, `package="second-server"`) || !strings.Contains(leaveAlone, `package="second-server"`) {
				t.Fatalf("work order does not match the selection:\n%s", order)
			}
		})
	}
}

func TestProjectToolsOpenFromCombinedSettings(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			app, port := projectToolsTestApp(t, size[0], size[1])
			app.openApplicationSettings()
			settingsWindow := app.overlayHost.Window(ui.OverlayWorkspaceSettings)
			body := app.settingsSurface(settingsWindow)
			if body == nil {
				t.Fatal("Settings was not mounted")
			}
			body.ShowLanguageTools()
			frame := settingsWindow.Content.(*ui.WindowFrameWidget)
			host := frame.Host()
			host.MarkLayout()
			app.View()
			barID := ui.ComponentID("settings/language-servers/actions")
			bar, ok := host.Component(barID).(*ui.ActionBar)
			if !ok {
				t.Fatal("language tools action bar not mounted")
			}
			barRect, ok := host.ScreenRect(barID)
			if !ok {
				t.Fatal("language tools actions have no bounds")
			}
			button, ok := bar.ButtonBounds("lsp.setup")
			if !ok || button.Empty() {
				t.Fatal("Set up button is not visible")
			}
			x, y := settingsWindow.Position()
			_, cmd := app.Update(tea.MouseClickMsg{X: x + barRect.X + button.X + 1, Y: y + barRect.Y + button.Y, Button: tea.MouseLeft})
			runAppCommand(t, app, cmd)
			if !app.overlayHost.IsOpen(projectToolsOverlay) || len(port.explicit) != 1 || !port.explicit[0] {
				t.Fatal("Set up did not open the explicit native review")
			}
			_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
			runAppCommand(t, app, cmd)
			if app.overlayHost.IsOpen(projectToolsOverlay) || app.overlayHost.Window(ui.OverlayWorkspaceSettings) != settingsWindow || len(port.choices) != 0 {
				t.Fatal("closing review consumed a choice or replaced Settings")
			}
		})
	}
}

func TestProjectToolsSettingsRequestRejectsStaleOwner(t *testing.T) {
	app, port := projectToolsTestApp(t, 160, 40)
	app.openApplicationSettings()
	window := app.overlayHost.Window(ui.OverlayWorkspaceSettings)
	body := app.settingsSurface(window)
	body.ShowLanguageTools()
	message := projectToolsSettingsMessage(t, body)
	if !body.ProjectToolsRequestRelevant(message) {
		t.Fatal("current Settings request was not accepted")
	}
	app.overlayHost.Close(ui.OverlayWorkspaceSettings)
	app.openApplicationSettings()
	_, cmd := app.Update(message)
	runAppCommand(t, app, cmd)
	if port.reads != 0 || app.overlayHost.IsOpen(projectToolsOverlay) {
		t.Fatal("stale Settings request opened a review")
	}
}

func TestProjectToolsSettingsReviewIsDiscardedAfterNavigation(t *testing.T) {
	app, port := projectToolsTestApp(t, 80, 24)
	app.openApplicationSettings()
	body := app.settingsSurface(app.overlayHost.Window(ui.OverlayWorkspaceSettings))
	body.ShowLanguageTools()
	message := projectToolsSettingsMessage(t, body)
	_, cmd := app.Update(message)
	if cmd == nil {
		t.Fatal("current review request was not scheduled")
	}
	app.overlayHost.Close(ui.OverlayWorkspaceSettings)
	app.openApplicationSettings()
	runAppCommand(t, app, cmd)
	if app.overlayHost.IsOpen(projectToolsOverlay) || app.projectTools.request != nil || len(port.choices) != 0 {
		t.Fatal("review opened after its Settings owner was replaced")
	}
}

func projectToolsSettingsMessage(t *testing.T, body *settingsui.WorkspaceSettingsSurface) settingsui.OpenProjectToolsMsg {
	t.Helper()
	for _, child := range body.Children() {
		if child.ID() != "settings/language-servers" {
			continue
		}
		handler, ok := child.(ui.ActionHandler)
		if !ok {
			t.Fatal("Language Tools has no action handler")
		}
		result := handler.HandleAction(ui.EventContext{}, ui.Action{ID: "lsp.setup"})
		if result.Cmd == nil {
			t.Fatal("Set up tools did not create a request")
		}
		message, ok := result.Cmd().(settingsui.OpenProjectToolsMsg)
		if !ok {
			t.Fatal("Set up tools produced a different message")
		}
		return message
	}
	t.Fatal("Language Tools is not mounted")
	return settingsui.OpenProjectToolsMsg{}
}

func TestProjectToolsCarriesExactSingleToolRequest(t *testing.T) {
	app, port := projectToolsTestApp(t, 80, 24)
	selection := types.ProjectToolSetupRequest{Explicit: true, LanguageID: "fixture", AssemblyGeneration: 23}
	runAppCommand(t, app, app.requestProjectTools(selection, nil))
	if len(port.selections) != 1 || port.selections[0] != selection || !app.overlayHost.IsOpen(projectToolsOverlay) {
		t.Fatalf("single-tool request lost its scope: %+v", port.selections)
	}
	app.closeProjectTools()
	if len(port.choices) != 0 {
		t.Fatal("closing the single-tool review recorded a decision")
	}
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
