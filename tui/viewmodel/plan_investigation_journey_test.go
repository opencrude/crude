package viewmodel

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/types"
	"github.com/charmbracelet/x/ansi"
)

// The HTTP responder supplies deterministic model decisions, not tool outcomes.
// Every file write, host decision, process, revision and read goes through Crude.
type investigationJourneyCall struct {
	name string
	args map[string]any
}

func investigationJourneyProvider(t *testing.T, calls []investigationJourneyCall, proposal string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Tools    []struct{ Function struct{ Name string } }
			Messages []struct {
				Role    string
				Content any
			}
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		index := int(n.Add(1)) - 1
		delta := map[string]any{}
		finish := "stop"
		if index < len(calls) {
			c := calls[index]
			found := false
			for _, tool := range input.Tools {
				found = found || tool.Function.Name == c.name
			}
			if !found {
				t.Errorf("step %d: %s not in offered tools", index, c.name)
			}
			args, _ := json.Marshal(c.args)
			delta["tool_calls"] = []any{map[string]any{"index": 0, "id": fmt.Sprintf("probe-%d", index), "type": "function", "function": map[string]string{"name": c.name, "arguments": string(args)}}}
			finish = "tool_calls"
		} else if index == len(calls) {
			delta["content"] = "<proposed_plan>\n" + proposal + "\n</proposed_plan>"
		} else {
			delta["content"] = "Done."
		}
		frame, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta, "finish_reason": finish}}})
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", frame)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

const (
	investigationOriginal = "package sample\nfunc value() int { return 3 }\n"
	investigationCheck    = "package sample\nimport \"testing\"\nfunc TestValue(t *testing.T){if value()!=5{t.Fatalf(\"expected five, got %d\",value())}}\n"
	investigationTestPath = "value_test.go"
	investigationPrompt   = "/plan Reproduce the incorrect value before proposing the change."
)

// investigationFixture is a Git project whose value is wrong, a scripted model
// that reproduces the failure in a Plan investigation, fixes it there, and
// proposes a plan, and a connected shell with YOLO on.
type investigationFixture struct {
	root   string
	shell  remoteJourneyShell
	app    *App
	id     string
	shells []remoteJourneyShell
}

func newInvestigationFixture(t *testing.T) investigationFixture {
	t.Helper()
	root := t.TempDir()
	const testCmd = "go test -count=1 -run TestValue ."
	const proposal = "# Correct the value\n\nKeep the regression test."
	for file, contents := range map[string]string{"go.mod": "module example.invalid/investigation\n\ngo 1.22\n", "value.go": investigationOriginal} {
		if err := os.WriteFile(filepath.Join(root, file), []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=Test", "-c", "user.email=test@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "baseline"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git: %v: %s", err, out)
		}
	}
	// Keep the test hermetic even when HOME points to the isolated shell profile.
	t.Setenv("GOWORK", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	cache, err := exec.Command("go", "env", "GOCACHE").Output()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOCACHE", strings.TrimSpace(string(cache)))
	model, _ := investigationJourneyProvider(t, []investigationJourneyCall{{"open_plan_investigation", map[string]any{}}, {"write_file", map[string]any{"path": investigationTestPath, "content": investigationCheck}}, {"execute_command", map[string]any{"command": testCmd}}, {"write_file", map[string]any{"path": "value.go", "content": strings.Replace(investigationOriginal, "return 3", "return 5", 1)}}, {"execute_command", map[string]any{"command": testCmd}}}, proposal)
	shell := newRemoteJourneyShell(t, remoteJourneyRuntime(t, model.URL), root)
	f := investigationFixture{root: root, shell: shell, app: shell.app, shells: []remoteJourneyShell{shell}}
	f.id = remoteJourneyCreate(t, shell)
	if !f.app.ws.SetYoloForAgent(f.id, true) {
		t.Fatal("YOLO setup failed")
	}
	remoteJourneyUntil(t, f.shells, func() bool { return f.app.ws.YoloForAgent(f.id) })
	return f
}

// sourceUnchanged reports how the source differs from its baseline, if at all.
func (f investigationFixture) sourceUnchanged() error {
	if body, err := os.ReadFile(filepath.Join(f.root, "value.go")); err != nil || string(body) != investigationOriginal {
		return fmt.Errorf("the source value.go changed: %q, %v", body, err)
	}
	if _, err := os.Stat(filepath.Join(f.root, investigationTestPath)); !os.IsNotExist(err) {
		return fmt.Errorf("the investigation test reached the source: %v", err)
	}
	return nil
}

func TestPlanInvestigationThroughConnectedShell(t *testing.T) {
	f := newInvestigationFixture(t)
	app, id, shells, root, path := f.app, f.id, f.shells, f.root, investigationTestPath
	_, _ = app.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	app.Input.SetInput(investigationPrompt)
	app.setFocusInput()
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	remoteJourneyUntilWithin(t, shells, 30*time.Second, func() bool { return len(app.agentPrompts) > 0 })
	consent := app.agentPrompts[0]
	if consent.request.operation != types.PlanInvestigationHostOperation || consent.offerAutoReview {
		t.Fatalf("wrong prompt: %+v", consent.request)
	}
	paint := frameText(app)
	for _, want := range []string{"outside a sandbox", "Command: go test", "Allow for this plan (y)", "Do not run (n)"} {
		if !strings.Contains(paint, want) {
			t.Fatalf("at 80×24, consent did not show %q:\n%s", want, paint)
		}
	}
	if app.ws.ResolveConfirmation(id, consent.request.blockID, "always") {
		t.Fatal("ordinary approval granted experiment consent")
	}
	if _, err := os.Stat(filepath.Join(root, path)); !os.IsNotExist(err) {
		t.Fatal("test file reached source before approval")
	}
	pressKeys(t, app, tea.KeyPressMsg{Code: 'y', Text: "y"})
	var rev types.PlanRevision
	remoteJourneyUntilWithin(t, shells, 90*time.Second, func() bool {
		var ok bool
		rev, ok = app.latestPlanRevision(id)
		projection := app.activeSessionDeck()
		return (ok && projection != nil && projection.decisionAgentID == id) || len(app.agentPrompts) > 0
	})
	if len(app.agentPrompts) > 0 {
		t.Fatalf("unexpected second permission: %+v", app.agentPrompts[0].request)
	}
	if len(rev.Evidence) != 2 || rev.Evidence[0].Status != types.PlanEvidenceFailed || rev.Evidence[1].Status != types.PlanEvidencePassed {
		t.Fatalf("test did not fail then pass: %+v", rev.Evidence)
	}
	paint = frameText(app)
	for _, want := range []string{"Implement (y)", "New session (c)", "Review (r)", "Keep planning (n)"} {
		if !strings.Contains(paint, want) {
			t.Fatalf("at 80×24, the decision did not show %q:\n%s", want, paint)
		}
	}
	for i, e := range rev.Evidence {
		body, err := app.ws.ReadPlanEvidence(t.Context(), id, rev.Number, e.Digest)
		if err != nil || fmt.Sprintf("%x", sha256.Sum256([]byte(body))) != e.Digest || !strings.Contains(body, "TestValue") || !strings.Contains(body, path) {
			t.Fatalf("revision %d receipt %d: verify=%v body=%q", rev.Number, i, err, body)
		}
		if i == 0 && !strings.Contains(body, "expected five, got 3") {
			t.Fatalf("baseline failed for a different reason: %s", body)
		}
		if i == 1 {
			runAppCommand(t, app, app.readPlanEvidence(id, rev.Number, e.Digest))
			screen := app.sessionTextOverlay()
			if !screen.ReadOnly() || screen.Value() != body {
				t.Fatal("reviewer did not see the verified receipt as read-only text")
			}
		}
	}
	if _, err := app.ws.ReadPlanEvidence(t.Context(), id, rev.Number+1, rev.Evidence[0].Digest); err == nil {
		t.Fatal("evidence accepted a different revision")
	}
	app.setMode(ModeChat)
	out, err := app.ws.SettlePlanReview(id, types.PlanReviewSettle{Action: types.PlanReviewImplement, Revision: rev.Number})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range rev.Evidence {
		if !strings.Contains(out.Text, e.Digest) {
			t.Fatal("implementation input lost approved evidence")
		}
	}
	remoteJourneyUntilWithin(t, shells, 30*time.Second, func() bool { return !app.ws.PlanModeForAgent(id) && !app.ws.AgentTurnActive(id) })
	if err := f.sourceUnchanged(); err != nil {
		t.Fatalf("approval copied the prototype: %v", err)
	}
}

// TestPlanInvestigationNativeCapture runs the investigation journey in a real
// terminal for scripts/tuishot.sh. The user types the Plan request, answers
// the host-run question, opens the evidence from the Command Palette and
// implements the plan. CRUDE_CAPTURE_EVIDENCE receives one line as each step
// becomes visible; F9 checks the outcome and writes CRUDE_VISUAL_PROOF.
func TestPlanInvestigationNativeCapture(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_PLAN_INVESTIGATION") != "1" {
		t.Skip("native terminal capture")
	}
	f := newInvestigationFixture(t)
	f.app.setFocusInput()
	m := &investigationCapture{App: f.app, t: t, f: f, seen: map[string]bool{}}
	program := tea.NewProgram(m, tea.WithWidthMethod(ansi.GraphemeWidth))
	m.SetProgram(program)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go func() {
		for {
			select {
			case msg := <-f.shell.fifo:
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

type investigationCapture struct {
	*App
	t       *testing.T
	f       investigationFixture
	seen    map[string]bool
	consent bool
}

func (m *investigationCapture) Init() tea.Cmd { return tickCmd() }

func (m *investigationCapture) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.Code == tea.KeyF9 {
		m.finish()
		return m, tea.Quit
	}
	_, cmd := m.App.Update(msg)
	m.observe()
	return m, cmd
}

// observe publishes each step once, after the shell shows it.
func (m *investigationCapture) observe() {
	id := m.f.id
	if len(m.agentPrompts) > 0 && m.agentPrompts[0].request.operation == types.PlanInvestigationHostOperation {
		m.consent = true
		m.publish("consent")
	}
	rev, ok := m.latestPlanRevision(id)
	if ok && len(rev.Evidence) == 2 && rev.Open() {
		m.publish("proposed")
	}
	if screen := m.sessionTextOverlay(); screen != nil && screen.IsOpen() && screen.ReadOnly() && strings.Contains(screen.Value(), "expected five, got 3") {
		m.publish("evidence")
	}
	if ok && rev.Outcome == types.PlanOutcomeImplemented && !m.ws.PlanModeForAgent(id) && !m.ws.AgentTurnActive(id) {
		m.publish("implemented")
	}
}

func (m *investigationCapture) publish(step string) {
	if m.seen[step] {
		return
	}
	m.seen[step] = true
	if path := os.Getenv("CRUDE_CAPTURE_EVIDENCE"); path != "" {
		file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err == nil {
			_, err = fmt.Fprintln(file, step)
			err = errors.Join(err, file.Close())
		}
		if err != nil {
			m.t.Error(err)
		}
	}
}

func (m *investigationCapture) finish() {
	rev, _ := m.latestPlanRevision(m.f.id)
	var statuses []types.PlanEvidenceStatus
	for _, e := range rev.Evidence {
		statuses = append(statuses, e.Status)
	}
	switch {
	case !m.consent:
		m.t.Error("the host-run question was not shown")
	case len(statuses) != 2 || statuses[0] != types.PlanEvidenceFailed || statuses[1] != types.PlanEvidencePassed:
		m.t.Errorf("evidence = %v, want failed then passed", statuses)
	case !m.seen["evidence"]:
		m.t.Error("the failing evidence was not opened")
	case rev.Outcome != types.PlanOutcomeImplemented || m.ws.PlanModeForAgent(m.f.id):
		m.t.Errorf("revision %d outcome %q; Plan mode %t", rev.Number, rev.Outcome, m.ws.PlanModeForAgent(m.f.id))
	}
	if err := m.f.sourceUnchanged(); err != nil {
		m.t.Error(err)
	}
	if path := os.Getenv("CRUDE_VISUAL_PROOF"); path != "" {
		if err := os.WriteFile(path, []byte(fmt.Sprintf("evidence=%v outcome=%s passed=%t\n", statuses, rev.Outcome, !m.t.Failed())), 0600); err != nil {
			m.t.Error(err)
		}
	}
}
