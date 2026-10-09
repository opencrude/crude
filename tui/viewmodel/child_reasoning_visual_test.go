package viewmodel

import (
	"database/sql"
	"encoding/json"
	"net/url"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/provider"
	"crude/types"
	"crude/types/reasoning"
)

// An explicitly selected saved child is read-only evidence. F2 shows the parent,
// F3 the child, F4 appends to its live block, F5 completes it, and F9 exits.
// No provider calls, tools or session writes occur in this acceptance fixture.
func TestChildReasoningVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_REASONING") != "1" {
		t.Skip("requires sequential scripts/tuishot.sh in a dedicated terminal")
	}
	trace := strings.Repeat("Inspect the owner and retained evidence before deciding which source controls the operation.\n\n", 60)
	display := reasoning.Display{Kind: reasoning.Summary}
	if path := os.Getenv("CRUDE_REASONING_REPLAY_DB"); path != "" {
		u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
		db, err := sql.Open("sqlite", u.String())
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		var items []byte
		if err := db.QueryRowContext(t.Context(), "SELECT provider_items FROM messages WHERE length(reasoning_content)>0 ORDER BY length(reasoning_content) DESC LIMIT 1").Scan(&items); err != nil {
			t.Fatal(err)
		}
		var recorded []provider.ProviderItem
		if err := json.Unmarshal(items, &recorded); err != nil {
			t.Fatal(err)
		}
		var output []json.RawMessage
		for _, item := range recorded {
			if item.Standard == "openai-responses" && item.Type == "reasoning" {
				output = append(output, item.Data)
			}
		}
		body, err := json.Marshal(map[string]any{"status": "completed", "output": output})
		if err != nil {
			t.Fatal(err)
		}
		response, err := (&provider.OpenAIResponsesStandard{}).DecodeResponse(body)
		if err != nil {
			t.Fatal(err)
		}
		trace, display = response.Choices[0].Message.ReasoningContent, response.Choices[0].Message.ReasoningDisplay
		if len(trace) < 1000 || display.Kind != reasoning.Summary {
			t.Fatal("selected record does not reproduce verbose summary-labelled reasoning")
		}
		t.Logf("Read-only researcher replay: %d bytes, wire kind %q", len(trace), display.Kind)
	}
	app := newFullTestApp(t)
	app.Logger = stubLogger{}
	app.width, app.height = 160, 40
	app.AddOrchestrator("parent")
	app.AgentMgr.Get("parent").Label = "Rebecca"
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "parent", Type: "thinking", Data: reasoning.Delta{Text: "Reviewing the researchers' findings", Kind: reasoning.Summary}})
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "child", Type: "sub_agent_started", Data: map[string]string{
		"agent_id": "child", "agent_name": "Asha sub Rebecca", "category": "researcher", "parent_id": "parent", "model": "recorded-researcher",
	}})
	app.handleAgentEvent(types.AgentEventMsg{AgentID: "child", Type: "turn_started", Data: types.TurnIdentity{Generation: 1, ProviderID: "gateway", AccountID: "recorded-account", ModelID: "recorded-researcher"}})
	for remaining := trace; remaining != ""; {
		end := min(240, len(remaining))
		for end < len(remaining) && !utf8.RuneStart(remaining[end]) {
			end--
		}
		app.handleAgentEvent(types.AgentEventMsg{AgentID: "child", Type: "thinking", Data: reasoning.Delta{Text: remaining[:end], Kind: display.Kind, BlockID: display.BlockID}})
		remaining = remaining[end:]
	}
	child := app.AgentMgr.Get("child").State
	if app.thinkingPaneID(child) == "" {
		t.Fatal("recorded researcher stream did not promote")
	}
	if thought, _ := app.Get(app.thinkingPaneID(child)).Messages.ActiveThinking(); thought.Content != trace || thought.ThinkingDisplay.Kind != display.Kind {
		t.Fatal("replay altered the provider trace")
	}
	app.switchToAgentView("child")
	model := &childReasoningVisualModel{App: app, display: display}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type childReasoningVisualModel struct {
	*App
	display reasoning.Display
}

func (m *childReasoningVisualModel) Init() tea.Cmd { return nil }

func (m *childReasoningVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2:
			m.switchToAgentView("parent")
			return m, nil
		case tea.KeyF3:
			m.switchToAgentView("child")
			return m, nil
		case tea.KeyF4:
			m.handleAgentEvent(types.AgentEventMsg{AgentID: "child", Type: "thinking", Data: reasoning.Delta{Text: "\n\nLatest evidence continues in the same pane.", Kind: m.display.Kind, BlockID: m.display.BlockID}})
			return m, nil
		case tea.KeyF5:
			m.handleAgentEvent(types.AgentEventMsg{AgentID: "child", Type: "done", Data: map[string]any{"reason": "completed"}})
			return m, nil
		case tea.KeyF9:
			return m, tea.Quit
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}
