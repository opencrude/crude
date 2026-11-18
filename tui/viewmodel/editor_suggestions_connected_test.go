package viewmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/prompt"
	"crude/provider"
	"crude/session"
	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/types"
)

// These journeys run the real shell, the framed core, the suggestion service
// and a local OpenAI-compatible model server. Input is native keys; nothing
// injects a candidate or a result.

type suggestionModel struct {
	mu        sync.Mutex
	inputs    []string
	calls     atomic.Int32
	cancelled atomic.Int32
	// hold, when set, keeps every request open until its context ends.
	hold   atomic.Bool
	answer func(input string) string
}

func (s *suggestionModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.calls.Add(1)
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		Messages []struct {
			Role    string `json:"role"`
			Content any    `json:"content"`
		} `json:"messages"`
	}
	_ = json.Unmarshal(raw, &body)
	input := ""
	for _, m := range body.Messages {
		if m.Role != "user" {
			continue
		}
		switch content := m.Content.(type) {
		case string:
			input += content
		case []any:
			for _, part := range content {
				if p, ok := part.(map[string]any); ok {
					if text, ok := p["text"].(string); ok {
						input += text
					}
				}
			}
		}
	}
	s.mu.Lock()
	s.inputs = append(s.inputs, input)
	s.mu.Unlock()
	if s.hold.Load() {
		<-r.Context().Done()
		s.cancelled.Add(1)
		return
	}
	content, _ := json.Marshal(s.answer(input))
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":%s}}]}\n\n", content)
	fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":40,\"completion_tokens\":8}}\n\ndata: [DONE]\n\n")
}

// suggestionRewrite answers with the request's editable region changed by fn.
func suggestionRewrite(fn func(region string) string) func(string) string {
	return func(input string) string {
		start := strings.Index(input, prompt.SuggestionRegionStart+"\n")
		end := strings.Index(input, prompt.SuggestionRegionEnd)
		if start < 0 || end < start {
			return ""
		}
		region := strings.Replace(input[start+len(prompt.SuggestionRegionStart)+1:end], prompt.SuggestionCursor, "", 1)
		return prompt.SuggestionRegionStart + "\n" + fn(region) + prompt.SuggestionRegionEnd
	}
}

type suggestionJourney struct {
	t       *testing.T
	model   *suggestionModel
	shell   remoteJourneyShell
	app     *App
	ed      *editor.Editor
	path    string
	results chan tea.Msg
}

func newSuggestionJourney(t *testing.T, text string, answer func(string) string) *suggestionJourney {
	t.Helper()
	model := &suggestionModel{answer: answer}
	server := httptest.NewServer(model)
	t.Cleanup(server.Close)
	runtime := remoteJourneyRuntimeConfig(t, server.URL, func(cfg *session.Config) {
		cfg.AccountManager = provider.NewAccountManager(cfg.Accounts, nil)
	})
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	j := &suggestionJourney{t: t, model: model, shell: shell, app: shell.app, path: path, results: make(chan tea.Msg, 256)}
	j.ed = liveTestOpen(t, j.app, path)
	// Answer the first-open trust question with its safe default
	// (restricted mode); suggestions do not need trust.
	j.update(types.TickMsg{})
	if j.app.overlayHost.IsOpen(ui.OverlayWorkspaceTrust) {
		j.key(tea.KeyPressMsg{Code: tea.KeyEnter})
		j.until("trust question answered", func() bool { return !j.app.overlayHost.IsOpen(ui.OverlayWorkspaceTrust) })
	}
	j.app.setFocusEditor()
	j.update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return j
}

// launch runs a command off the test goroutine, as Bubble Tea does, and
// feeds its result back through Update in until.
func (j *suggestionJourney) launch(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		msg := cmd()
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, c := range batch {
				j.launch(c)
			}
			return
		}
		if msg != nil {
			j.results <- msg
		}
	}()
}

func (j *suggestionJourney) update(msg tea.Msg) {
	j.t.Helper()
	_, cmd := j.app.Update(msg)
	j.launch(cmd)
}

func (j *suggestionJourney) key(key tea.KeyPressMsg) {
	j.t.Helper()
	j.update(key)
}

func (j *suggestionJourney) typeText(text string) {
	j.t.Helper()
	for _, r := range text {
		j.key(tea.KeyPressMsg{Code: r, Text: string(r)})
		j.settleEdits()
	}
}

// settleEdits waits until the core acknowledged every local edit.
func (j *suggestionJourney) settleEdits() {
	j.t.Helper()
	j.until("edits acknowledged", func() bool {
		s := j.ed.DocumentState()
		_, current := j.ed.CurrentInsightSource()
		return s.PendingEdits == 0 && !s.InFlight && current
	})
}

// until pumps pushed events, command results and timer wakes through Update.
func (j *suggestionJourney) until(what string, condition func() bool) {
	j.t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(2 * time.Millisecond)
	defer tick.Stop()
	for {
		for draining := true; draining; {
			select {
			case msg := <-j.shell.fifo:
				j.update(msg)
			case msg := <-j.results:
				j.update(msg)
			default:
				draining = false
			}
		}
		if condition() {
			return
		}
		select {
		case <-deadline.C:
			j.t.Fatalf("%s: not reached; value=%q suggest=%+v paint:\n%s", what, j.ed.Value(), j.app.suggest, ansi.Strip(j.app.View().RenderContent()))
		case <-tick.C:
		}
	}
}

// turnOn chooses a mode from the Aeron footer menu and answers the consent
// question with Enter, as the user does.
func (j *suggestionJourney) turnOn(mode types.EditorPredictionMode) {
	j.t.Helper()
	j.app.chooseSuggestionMode(mode)
	if j.app.suggest.consent == nil || !j.app.overlayHost.IsOpen(suggestionConsentOverlay) {
		j.t.Fatalf("turning suggestions on did not ask for consent in the shared confirm frame: mode=%q consent=%v open=%v", j.app.suggest.mode, j.app.suggest.consent != nil, j.app.overlayHost.IsOpen(suggestionConsentOverlay))
	}
	paint := ansi.Strip(j.app.View().RenderContent())
	if !strings.Contains(paint, "Turn on suggestions in this workspace?") || !strings.Contains(paint, "Turn on") {
		j.t.Fatalf("consent question is not painted:\n%s", paint)
	}
	// Cancel has the default focus; the user moves to Turn on.
	j.key(tea.KeyPressMsg{Code: tea.KeyRight})
	j.key(tea.KeyPressMsg{Code: tea.KeyEnter})
	j.until("suggestions ready", func() bool {
		return j.app.suggest.mode == mode && j.app.suggest.status.Ready && j.app.suggest.status.Mode == mode
	})
	j.app.setFocusEditor()
	j.update(tea.WindowSizeMsg{Width: 120, Height: 40})
}

func (j *suggestionJourney) caret(line, col int) {
	j.ed.SetCursorPosition(line, col)
	j.update(tea.WindowSizeMsg{Width: 120, Height: 40})
}

func (j *suggestionJourney) shown() (editor.Suggestion, bool) {
	if j.ed.GhostOwner() != suggestionGhostOwner {
		return editor.Suggestion{}, false
	}
	return j.ed.GhostSuggestion()
}

func (j *suggestionJourney) status() types.EditorPredictionStatus {
	j.t.Helper()
	port := j.app.ws.(shellport.Suggestions)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status, err := port.EditorSuggestionStatus(ctx)
	if err != nil {
		j.t.Fatal(err)
	}
	return status
}

var askSuggestionKey = tea.KeyPressMsg{Code: '\\', Mod: tea.ModAlt}

// When I ask: Alt+\ shows the first use of a rename beside the caret, Tab
// applies it, and the next use follows at once from the same answer without
// another model request. Esc dismisses it and the core drops the rest.
func TestConnectedSuggestionAskTabFollowAndDismiss(t *testing.T) {
	j := newSuggestionJourney(t, "count := load()\nlog(count)\nsave(count)\nend()\n",
		suggestionRewrite(func(region string) string { return strings.ReplaceAll(region, "(count)", "(total)") }))
	j.turnOn(types.PredictionAsk)
	j.caret(0, 0)
	for range len("count") {
		j.key(tea.KeyPressMsg{Code: tea.KeyDelete})
		j.settleEdits()
	}
	j.typeText("total")
	if j.model.calls.Load() != 0 {
		t.Fatal("When I ask sent a request while the user typed")
	}
	j.key(askSuggestionKey)
	j.until("first change shown", func() bool { _, ok := j.shown(); return ok })
	first, _ := j.shown()
	text := j.ed.Value()
	logUse := strings.Index(text, "(count)") + 1
	if first.Start != logUse || first.End != logUse+len("count") || first.Text != "total" {
		t.Fatalf("first change = %+v in %q", first, text)
	}
	if paint := ansi.Strip(j.app.View().RenderContent()); !strings.Contains(paint, "total") || !strings.Contains(paint, "log(count") {
		t.Fatalf("nearby change is not painted beside the code:\n%s", paint)
	}
	if !strings.Contains(j.model.inputs[0], "-count") || !strings.Contains(j.model.inputs[0], "+total") {
		t.Fatalf("the user's rename was not in the request:\n%s", j.model.inputs[0])
	}

	j.key(tea.KeyPressMsg{Code: tea.KeyTab})
	j.settleEdits()
	if got := j.ed.Value(); got != "total := load()\nlog(total)\nsave(count)\nend()\n" {
		t.Fatalf("Tab did not apply exactly the shown change: %q", got)
	}
	j.until("follow-up change shown", func() bool {
		s, ok := j.shown()
		return ok && s.Text == "total"
	})
	next, _ := j.shown()
	saveUse := strings.LastIndex(j.ed.Value(), "count")
	if next.Start != saveUse || next.End != saveUse+len("count") || j.model.calls.Load() != 1 {
		t.Fatalf("follow-up = %+v, model calls = %d", next, j.model.calls.Load())
	}

	j.key(tea.KeyPressMsg{Code: tea.KeyEsc})
	if _, ok := j.shown(); ok || j.app.suggest.shown != nil || j.app.suggest.dismissed == "" {
		t.Fatalf("Esc did not dismiss and remember the change: %+v", j.app.suggest)
	}
	if j.ed.Value() != "total := load()\nlog(total)\nsave(count)\nend()\n" || !j.ed.IsOpen() {
		t.Fatal("Esc on a suggestion changed the code or closed the tab")
	}
	j.until("core view released", func() bool { return !j.app.suggest.live && j.status().InFlight == 0 })
	// The released view no longer holds the follow-up: a follow request
	// returns nothing and sends nothing.
	j.app.scheduleSuggestion(j.ed, 0, false, true)
	j.until("follow request answered", func() bool { return j.app.suggest.intent == nil && j.app.suggest.request == nil })
	if _, ok := j.shown(); ok || j.model.calls.Load() != 1 {
		t.Fatalf("a dismissed answer came back after release: calls=%d", j.model.calls.Load())
	}
}

// As I type: a request that is still running when the caret moves is
// cancelled at the model server, and nothing is shown afterwards.
func TestConnectedSuggestionCaretMoveCancelsTheRunningRequest(t *testing.T) {
	j := newSuggestionJourney(t, "func main() {\n\t\n}\n", suggestionRewrite(func(region string) string { return region }))
	j.turnOn(types.PredictionType)
	j.model.hold.Store(true)
	j.caret(1, 0)
	j.key(tea.KeyPressMsg{Code: tea.KeyEnd})
	j.typeText("x")
	j.until("request reached the model", func() bool { return j.model.calls.Load() == 1 })
	if paint := ansi.Strip(j.app.View().RenderContent()); !strings.Contains(paint, "Aeron …") {
		t.Fatalf("the footer does not show that Aeron is working:\n%s", paint)
	}
	j.key(tea.KeyPressMsg{Code: tea.KeyLeft})
	j.until("model request cancelled", func() bool { return j.model.cancelled.Load() == 1 })
	j.until("core idle", func() bool { return j.app.suggest.request == nil && j.status().InFlight == 0 })
	if _, ok := j.shown(); ok {
		t.Fatal("a cancelled request showed a suggestion")
	}
	if paint := ansi.Strip(j.app.View().RenderContent()); strings.Contains(paint, "Aeron …") || strings.Contains(paint, "Aeron !") {
		t.Fatalf("the footer kept a working or problem state after cancel:\n%s", paint)
	}
}

// As I type: a shown insertion leaves when the editor loses the keys, and the
// same change does not come back after Esc until the code near it changes.
func TestConnectedSuggestionFocusLossAndDismissalMemory(t *testing.T) {
	j := newSuggestionJourney(t, "func total(xs []int) int {\n\t\n}\n",
		suggestionRewrite(func(region string) string {
			return strings.Replace(region, "sum\n", "sum := 0\n", 1)
		}))
	j.turnOn(types.PredictionType)
	j.caret(1, 0)
	j.key(tea.KeyPressMsg{Code: tea.KeyEnd}) // after the tab
	j.typeText("sum")
	j.until("insertion shown", func() bool { s, ok := j.shown(); return ok && s.Text == " := 0" })
	if paint := ansi.Strip(j.app.View().RenderContent()); !strings.Contains(paint, "sum := 0") {
		t.Fatalf("inline text is not painted at the caret:\n%s", paint)
	}

	j.app.setFocusInput()
	j.update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if _, ok := j.shown(); ok || j.app.suggest.shown != nil {
		t.Fatal("the suggestion stayed after the editor lost the keys")
	}
	j.until("released on focus loss", func() bool { return !j.app.suggest.live })
	j.app.setFocusEditor()
	j.update(tea.WindowSizeMsg{Width: 120, Height: 40})
	j.key(tea.KeyPressMsg{Code: tea.KeyTab})
	j.settleEdits()
	if got := j.ed.Value(); strings.Contains(got, ":= 0") {
		t.Fatalf("Tab accepted a suggestion that had left the screen: %q", got)
	}
	// Undo the indentation Tab typed, then ask again by typing.
	j.key(tea.KeyPressMsg{Code: tea.KeyBackspace})
	j.settleEdits()
	j.until("insertion shown again", func() bool { s, ok := j.shown(); return ok && s.Text == " := 0" })
	calls := j.model.calls.Load()

	j.key(tea.KeyPressMsg{Code: tea.KeyEsc})
	if _, ok := j.shown(); ok {
		t.Fatal("Esc did not dismiss the suggestion")
	}
	// The same text near the caret: the dismissed change stays away, even
	// though the answer is reused from the cache.
	j.typeText("x")
	j.key(tea.KeyPressMsg{Code: tea.KeyBackspace})
	j.settleEdits()
	j.until("automatic request answered", func() bool {
		return j.app.suggest.intent == nil && j.app.suggest.request == nil
	})
	if _, ok := j.shown(); ok {
		t.Fatal("a dismissed suggestion came back while the code near it was the same")
	}
	// Changed code near the caret: suggestions come back.
	for range len("sum") {
		j.key(tea.KeyPressMsg{Code: tea.KeyBackspace})
		j.settleEdits()
	}
	j.typeText(" sum")
	j.until("suggestion after a real change", func() bool { s, ok := j.shown(); return ok && s.Text == " := 0" })
	if j.model.calls.Load() < calls {
		t.Fatal("request count went backwards")
	}
	if j.ed.Value() != "func total(xs []int) int {\n\t sum\n}\n" {
		t.Fatalf("suggestions changed the code: %q", j.ed.Value())
	}
}
