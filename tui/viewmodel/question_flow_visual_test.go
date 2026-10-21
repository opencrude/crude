package viewmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/screens/question"
)

// Native input is supplied only by sequential scripts/tuishot.ps1/.sh. The
// fixture uses the real framed core, question tool, replicas, App, and renderer;
// only provider text is deterministic. F2 starts a mixed question request, F3
// an extreme-length request, and F9 verifies the reviewed response and exits.
func TestQuestionFlowVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_QUESTIONS") != "1" {
		t.Skip("requires the dedicated sequential tuishot terminal")
	}
	var continuations atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		answered, extreme := false, false
		for _, message := range input.Messages {
			answered = answered || message.Role == "tool"
			extreme = extreme || message.Role == "user" && strings.Contains(fmt.Sprint(message.Content), "extreme-length")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if answered {
			continuations.Add(1)
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"REVIEWED ANSWERS RECEIVED. The request continued only after explicit Submit.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		items := []question.Item{
			{Header: "Release", Question: "Which release should we prepare?", Options: []string{"Stable", "Preview"}},
			{Header: "Deployment scope", Question: "Should the improved script only upload already-built release files, keeping configuration and unrelated files untouched, or should it also build the release and restart services? Please consider rollback safety, availability, production secrets, and independent application configuration before choosing. This entire question must remain readable inside its bounded dock.", Options: []string{"Upload already-built releases only, preserving service state and all existing configuration files.", "Upload and restart services after verifying the uploaded release and retaining the previous release for rollback.", "Build, upload, and restart services; keep every configuration file and unrelated deployment artifact unchanged."}, Multiple: true},
			{Header: "Release notes", Question: "Write any additional requirements. Multiple paragraphs and long responses are welcome; review them before sending."},
		}
		if extreme {
			items = []question.Item{{Header: "Long reading", Question: "QUESTION_BEGIN\n" + strings.Repeat("This is complete question content, not an ellipsis or hidden overflow.\n", 600) + "QUESTION_END", Options: []string{"OPTION_BEGIN " + strings.Repeat("a long option with spaces and a full readable ending ", 150) + "OPTION_END", "Short answer"}}}
		}
		args, _ := json.Marshal(map[string]any{"questions": items})
		payload, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "native-question", "type": "function", "function": map[string]any{"name": "question", "arguments": string(args)}}}}, "finish_reason": "tool_calls"}}})
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", payload)
	}))
	defer server.Close()
	runtime := remoteJourneyRuntime(t, server.URL)
	shell := newRemoteJourneyShell(t, runtime, t.TempDir())
	id := remoteJourneyCreate(t, shell)
	shell.app.Input.SetInput("Private composer draft stays intact")
	model := &questionFlowVisualModel{App: shell.app, t: t, agentID: id, continuations: &continuations}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	model.SetProgram(program)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		for {
			select {
			case msg := <-shell.fifo:
				program.Send(msg)
			case <-ctx.Done():
				return
			}
		}
	}()
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
}

type questionFlowVisualModel struct {
	*App
	t             *testing.T
	agentID       string
	continuations *atomic.Int32
	seededDraft   bool
}

func (m *questionFlowVisualModel) Init() tea.Cmd { return nil }

func (m *questionFlowVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.Code {
		case tea.KeyF2, tea.KeyF3:
			goal := "Ask the mixed question fixture"
			if key.Code == tea.KeyF3 {
				goal = "Ask the extreme-length question fixture"
			}
			return m, m.submitInput(m.agentID, goal, nil)
		case tea.KeyF9:
			text := "QUESTION_VISUAL_SUCCESS"
			if m.continuations.Load() != 1 || len(m.agentPrompts) != 0 || m.Input.Value() != "Private composer draft stays intact" {
				m.t.Errorf("question flow incomplete: continuations=%d pending=%d draft=%q", m.continuations.Load(), len(m.agentPrompts), m.Input.Value())
				text = "QUESTION_VISUAL_FAILED"
			}
			if path := os.Getenv("CRUDE_QUESTION_VISUAL_PROOF"); path != "" {
				if err := os.WriteFile(path, []byte(text), 0600); err != nil {
					m.t.Error(err)
				}
			}
			return m, tea.Quit
		}
	}
	_, command := m.App.Update(msg)
	if !m.seededDraft && len(m.agentPrompts) > 0 {
		// Seed an unrelated unsubmitted draft after the fixture input's real
		// admission consumed its initial composer. Question lifecycle must not
		// own or rewrite this new draft.
		m.Input.SetInput("Private composer draft stays intact")
		m.seededDraft = true
	}
	return m, command
}
