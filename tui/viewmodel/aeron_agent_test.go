package viewmodel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/session"
	"crude/tui/ui"
)

func aeronToolReply(w http.ResponseWriter, name string, arguments any, round int) {
	args, _ := json.Marshal(arguments)
	payload, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
		"delta": map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "id": fmt.Sprintf("aeron-call-%d", round), "type": "function",
			"function": map[string]any{"name": name, "arguments": string(args)},
		}}}, "finish_reason": "tool_calls",
	}}})
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", payload)
}

// openAeronRow activates Aeron's row in the Agents panel with the keyboard,
// the same path as a click.
func openAeronRow(t *testing.T, app *App) {
	t.Helper()
	app.setActivity(activityAgents, true)
	app.setFocusTree()
	app.paintShellFrame()
	for _, key := range []rune{tea.KeyHome, tea.KeyDown, tea.KeyEnter} {
		_, cmd := app.Update(tea.KeyPressMsg{Code: key})
		runAppCommand(t, app, cmd)
	}
}

// Aeron opens from its row as a normal agent tab, with the normal composer, and
// runs real commands and settings changes through the connected core. The
// session's system prompt carries the workspace-agent contract.
func TestAeronOpensAsAgentTabAndRunsTools(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			root := t.TempDir()
			commandPath := filepath.Join(root, "fixture-language-server")
			var calls atomic.Int32
			var sawContract atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input struct {
					Messages []struct {
						Role    string
						Content string
					}
				}
				if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
					t.Error(err)
					return
				}
				round := int(calls.Add(1))
				w.Header().Set("Content-Type", "text/event-stream")
				lastTool := ""
				for _, message := range input.Messages {
					if message.Role == "system" && strings.Contains(message.Content, "<workspace_agent>") {
						sawContract.Store(true)
					}
					if message.Role == "tool" {
						lastTool = message.Content
					}
				}
				switch round {
				case 1:
					aeronToolReply(w, "execute_command", map[string]any{"command": "echo AERON_COMMAND_WORKS"}, round)
				case 2:
					if !strings.Contains(lastTool, "AERON_COMMAND_WORKS") {
						t.Errorf("Aeron's command did not run: %s", lastTool)
					}
					aeronToolReply(w, "crude_control", map[string]any{"command": "settings.set",
						"arguments": map[string]any{"scope": "workspace", "path": "lsp.languages.aeron-fixture.command", "value": commandPath}}, round)
				default:
					if !strings.Contains(lastTool, `"state":"committed"`) {
						t.Errorf("Aeron cannot configure the tool it set up: %s", lastTool)
					}
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"AERON_OK\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				}
			}))
			t.Cleanup(server.Close)
			runtime := remoteJourneyRuntimeConfig(t, server.URL, func(cfg *session.Config) {
				profile := cfg.Agents.Profiles["default"]
				role := session.DefaultAgentsConfig().Profiles["default"].Categories[session.CategoryAeron]
				role.AccountID, role.Model, role.ModelSupportsTools = "test", "test-model", true
				role.ModelContextWindow = 200_000
				profile.Categories[session.CategoryAeron] = role
				cfg.Agents.Profiles["default"] = profile
			})
			shell := newRemoteJourneyShell(t, runtime, root)
			remoteJourneyCreate(t, shell)
			app := shell.app
			app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			runAppCommand(t, app, app.ensureWorkspaceAeron())
			remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return app.workspaceAeronAgent() != "" })
			aeron := app.workspaceAeronAgent()
			if !app.AgentView.IsHidden(aeron) {
				t.Fatal("the background Aeron took a tab before the user opened it")
			}

			openAeronRow(t, app)
			if app.AgentView.ActiveID() != aeron || app.Win.ActiveTab() == nil || app.Win.ActiveTab().Title != app.agentTabTitle(aeron) {
				t.Fatal("opening Aeron did not select its agent tab")
			}
			if app.ActiveSession != app.ws.SessionsByAgent()[aeron] {
				t.Fatal("the normal composer does not send to Aeron")
			}
			// The tab, Agents row and tooltip name the same agent, not its
			// generated session name.
			if row := findSidebarAgent(t, app, aeron); row.Label != "Aeron" ||
				app.Win.ActiveTab().DisplayTitle != "Aeron" || !strings.HasPrefix(app.tabTooltip(app.Win.ActiveTab().ID), "Aeron\n") {
				t.Fatalf("Aeron is not named Aeron everywhere: row %q tab %q", row.Label, app.Win.ActiveTab().DisplayTitle)
			}
			app.paintShellFrame()
			for _, gone := range []string{"Give Aeron a task", "Esc back"} {
				if strings.Contains(app.View().Content, gone) {
					t.Fatalf("the removed sidebar page is still painted: %q", gone)
				}
			}

			app.Update(tea.PasteMsg{Content: "Check the command and configure the fixture tool"})
			_, send := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			runAppCommand(t, app, send)
			remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
				return app.aeronStatus() == "finished"
			})
			if got := app.ws.LoadAppSettings().LSP.Languages["aeron-fixture"].Command; got != commandPath {
				t.Fatalf("the configured command did not reach workspace settings: %q", got)
			}
			if !sawContract.Load() {
				t.Fatal("Aeron's system prompt has no workspace-agent contract")
			}
			if calls.Load() != 3 {
				t.Fatalf("the task was submitted more than once: %d requests", calls.Load())
			}
		})
	}
}

// Aeron's question is a modal prompt even when its own tab is open, and the
// user's draft and tab stay as they were.
func TestAeronQuestionIsAModalPrompt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		aeronToolReply(w, "question", map[string]any{"questions": []map[string]any{{
			"header": "Choice", "question": "Choose a fixture answer", "options": []string{"Yes", "No"},
		}}}, 1)
	}))
	t.Cleanup(server.Close)
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, server.URL), t.TempDir())
	app := shell.app
	main := remoteJourneyCreate(t, shell)
	app.Input.SetInput("MAIN DRAFT STAYS")
	runAppCommand(t, app, app.taskWorkspaceAeron("<tool_setup_task>Ask a question</tool_setup_task>", "Set up the fixture tool."))
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return len(app.agentPrompts) == 1 })
	if !app.overlayHost.IsOpen(ui.OverlayAgentPrompt) || app.agentPrompts[0].inline {
		t.Fatal("Aeron's question is not shown as a modal prompt")
	}
	if app.AgentView.ActiveID() != main || app.Input.Value() != "MAIN DRAFT STAYS" {
		t.Fatal("Aeron's question changed the user's tab or draft")
	}
	if fw := app.overlayHost.Window(ui.OverlayAgentPrompt); fw == nil || !strings.Contains(fw.Title, "Questions") {
		t.Fatal("the modal is not the question prompt")
	}

	// Opening Aeron's own tab keeps the question in the modal; it never moves
	// into the tab's deck.
	app.restoreAgent(app.workspaceAeronAgent())
	app.openNextAgentPrompt()
	if !app.overlayHost.IsOpen(ui.OverlayAgentPrompt) || app.agentPrompts[0].inline {
		t.Fatal("Aeron's question moved out of the modal when its tab opened")
	}
}
