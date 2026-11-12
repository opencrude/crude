package viewmodel

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/coreprotocol"
	"crude/shellport"
	"crude/types"
	"crude/workspace"
)

// These tests fill the real retained workspace's provider-work budget, then
// send the user's own request across its actual remote core attachment. Only
// the deliberately blocked provider is a fixture; core refusal and retry are real.
type holdingCompletionRuntime struct {
	base          typingCompletionRuntime
	gate, entered chan struct{}
}
type holdingCompletionClient struct {
	*typingCompletionClient
	owner *holdingCompletionRuntime
}

func (r *holdingCompletionRuntime) NewClient(spec types.LSPServerSpec, root string, admit func() error) workspace.LSPClient {
	return &holdingCompletionClient{typingCompletionClient: r.base.NewClient(spec, root, admit).(*typingCompletionClient), owner: r}
}
func (c *holdingCompletionClient) RequestCompletions(ctx context.Context, path string, line, col int, trigger types.LSPCompletionContext) (types.LSPCompletionResponse, error) {
	c.owner.entered <- struct{}{}
	<-c.owner.gate // Deliberately ignore cancellation until the provider returns.
	return c.typingCompletionClient.RequestCompletions(ctx, path, line, col, trigger)
}
func waitHeldAssistance(t *testing.T, entered <-chan struct{}, kind string) {
	t.Helper()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal(kind, "provider did not start")
	}
}
func requireBusyFault(t *testing.T, err error) {
	t.Helper()
	var fault *coreprotocol.Fault
	if !errors.As(err, &fault) || fault.Code != "busy" {
		t.Fatal("real core did not report definite non-admission", err)
	}
}
func TestConnectedCompletionBusyRecoveryAcceptsAndUndoes(t *testing.T) {
	engine := &holdingCompletionRuntime{gate: make(chan struct{}), entered: make(chan struct{}, 32)}
	shells, eds, w, path := newTypingCompletionFixtureWithRuntime(t, "pr", engine)
	app, ed := shells[0].app, eds[0]
	source, ready := ed.CurrentInsightSource()
	if !ready {
		t.Fatal("source not current")
	}
	owner := app.ws.(shellport.LiveDocuments).DocumentOwner()
	var workers sync.WaitGroup
	var once sync.Once
	unblock := func() { once.Do(func() { close(engine.gate) }) }
	defer func() { unblock(); workers.Wait() }()
	for sequence := uint64(1); sequence <= 16; sequence++ {
		workers.Add(1)
		go func(n uint64) {
			defer workers.Done()
			_, _ = w.RequestLSPCompletions(t.Context(), owner, source, "held-completion", n, 0, 2, types.LSPCompletionContext{})
		}(sequence)
		waitHeldAssistance(t, engine.entered, "completion")
	}
	cmd := app.requestCompletionCmd()
	if cmd == nil {
		t.Fatal("user completion did not start")
	}
	result, ok := cmd().(completionResultMsg)
	if !ok {
		t.Fatal("completion result missing")
	}
	requireBusyFault(t, result.err)
	retry := app.handleCompletionResult(result)
	if retry == nil {
		t.Fatal("definite busy did not schedule retry")
	}
	if ed.Value() != "pr" || ed.Modified() || app.Input.Value() != "private draft" {
		t.Fatal("capacity refusal changed source or draft")
	}
	unblock()
	workers.Wait()
	if err := w.LiveDocuments.ReleaseLSPCompletions(owner, "held-completion", 16); err != nil {
		t.Fatal(err)
	}
	runAppCommand(t, app, retry)
	if app.completion == nil || app.completion.screen == nil || !app.completion.shown {
		t.Fatal("completion did not recover without a new edit")
	}
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: tea.KeyTab})
	remoteJourneyUntil(t, shells, func() bool { return ed.Value() != "pr" && eds[1].Value() == ed.Value() })
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, shells, func() bool { return ed.Value() == "pr" && eds[1].Value() == "pr" })
	if data, err := os.ReadFile(path); err != nil || string(data) != "pr" || app.Input.Value() != "private draft" {
		t.Fatal("completion retry wrote disk or consumed draft", err)
	}
}
func TestConnectedInlayBusyRecoveryPaintsAndPreservesUndo(t *testing.T) {
	f := newInlayConnectedFixture(t, true, inlayConnectedAnswer)
	app, ed := f.shells[0].app, f.main
	app.setInlayViewHidden(true)
	source, ready := ed.CurrentInsightSource()
	if !ready {
		t.Fatal("source not current")
	}
	owner := app.ws.(shellport.LiveDocuments).DocumentOwner()
	gate, entered := make(chan struct{}), make(chan struct{}, 32)
	f.engine.mu.Lock()
	f.engine.requestGate, f.engine.requestEntered = gate, entered
	f.engine.mu.Unlock()
	var workers sync.WaitGroup
	var once sync.Once
	unblock := func() { once.Do(func() { close(gate) }) }
	defer func() { unblock(); workers.Wait() }()
	span := types.LSPInlayRange{Start: types.LSPInlayPosition{Line: 0}, End: types.LSPInlayPosition{Line: 2, Character: 13}}
	for sequence := uint64(1); sequence <= 16; sequence++ {
		workers.Add(1)
		go func(n uint64) {
			defer workers.Done()
			_, _ = f.ws.RequestLSPInlayHints(t.Context(), owner, source, "held-inlay", n, span)
		}(sequence)
		waitHeldAssistance(t, entered, "inlay")
	}
	app.setInlayViewHidden(false)
	app.syncInlayHints()
	view := app.inlayViews[ed]
	if view == nil || view.request == nil {
		t.Fatal("visible inlay did not prepare")
	}
	cmd := app.startInlayRequest(inlayDueMsg{owner: view.request})
	if cmd == nil {
		t.Fatal("visible inlay did not start")
	}
	result, ok := cmd().(inlayResultMsg)
	if !ok {
		t.Fatal("inlay result missing")
	}
	requireBusyFault(t, result.err)
	retry := app.handleInlayResult(result)
	if retry == nil {
		t.Fatal("definite busy did not schedule retry")
	}
	if ed.Value() != inlayConnectedSource || ed.Modified() || app.Input.Value() != "private first inlay draft" {
		t.Fatal("capacity refusal changed source or draft")
	}
	unblock()
	workers.Wait()
	if err := f.ws.LiveDocuments.ReleaseLSPInlayHints(owner, "held-inlay", 16); err != nil {
		t.Fatal(err)
	}
	runAppCommand(t, app, retry)
	if !ed.HasInlayProjection() {
		t.Fatal("inlays did not recover without a new edit")
	}
	ed.SetCursorPosition(2, 13)
	renameConnectedKey(t, f.shells[0], tea.KeyPressMsg{Code: 'X', Text: "X"})
	remoteJourneyUntil(t, f.shells, func() bool { return f.peer.Value() == ed.Value() && ed.Value() != inlayConnectedSource })
	renameConnectedKey(t, f.shells[0], tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, f.shells, func() bool { return ed.Value() == inlayConnectedSource && f.peer.Value() == inlayConnectedSource })
	if data, err := os.ReadFile(f.engine.main); err != nil || string(data) != inlayConnectedSource || app.Input.Value() != "private first inlay draft" {
		t.Fatal("inlay retry wrote disk or consumed draft", err)
	}
}
