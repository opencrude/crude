package viewmodel

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/screens/inlay"
	"crude/tui/ui"
	"crude/types"
)

// The fake port observes cancellation but deliberately completes late with an
// otherwise valid answer. Shutdown must join the actual command body.
type holdingInspectPort struct {
	*retryInlayPort
	entered, cancelled, release chan struct{}
	late                        types.LSPInlayInspection
	once                        sync.Once
}

func (p *holdingInspectPort) InspectLSPInlayHint(ctx context.Context, _, _ string, _ int, _ bool) (types.LSPInlayInspection, error) {
	close(p.entered)
	<-ctx.Done()
	close(p.cancelled)
	<-p.release
	return p.late, nil
}
func (p *holdingInspectPort) unblock() { p.once.Do(func() { close(p.release) }) }

func holdingInlayInspection(t *testing.T) (*App, *editor.Editor, *inlayInspectorState, *holdingInspectPort, tea.Cmd) {
	t.Helper()
	app, ed, base := inlayRetryFixture(t, nil)
	app.resetInlayHints()
	base.calls.Store(1)
	port := &holdingInspectPort{retryInlayPort: base, entered: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(port.unblock)
	app.ws = port
	runAppCommand(t, app, app.syncInlayHints())
	app.openInlayInspectorCmd()
	reader := app.inlayInspector
	if reader == nil || !reader.screen.InList() {
		t.Fatal("reader list unavailable")
	}
	action := reader.screen.HandleAction(ui.EventContext{}, ui.Action{ID: inlay.ActionInspect})
	if action.Cmd == nil {
		t.Fatal("native inspection action missing")
	}
	intent, ok := action.Cmd().(inlay.InspectMsg)
	if !ok {
		t.Fatal("wrong inspection intent")
	}
	set := reader.request.result
	port.late = types.LSPInlayInspection{SetID: set.ID, Digest: set.Digest, Index: intent.Index,
		Boundary: set.Boundary, WireVersion: set.WireVersion, Range: set.Range, Hint: set.Hints[0],
		OriginalJSON: []byte(`{"label":"hint"}`), ResolveState: "not-requested"}
	command := app.inspectInlayHintCmd(intent)
	if command == nil {
		t.Fatal("inspection did not start")
	}
	return app, ed, reader, port, command
}

func awaitInlaySignal(t *testing.T, signal <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(10 * time.Second):
		t.Fatal(what)
	}
}

func TestInlayInspectionIsJoinedOnShutdown(t *testing.T) {
	app, ed, _, port, command := holdingInlayInspection(t)
	state := asyncIOStateFor(app)
	state.mu.Lock()
	baseline := make(map[uint64]bool, len(state.clientIOJobs))
	for id := range state.clientIOJobs {
		baseline[id] = true
	}
	state.mu.Unlock()
	result := make(chan tea.Msg, 1)
	go func() { result <- command() }()
	awaitInlaySignal(t, port.entered, "inspection did not reach its port")
	state.mu.Lock()
	registered := false
	for id := range state.clientIOJobs {
		if !baseline[id] {
			registered = true
		}
	}
	state.mu.Unlock()
	if !registered {
		t.Fatal("started inspection is not owned by joined client I/O")
	}
	app.closeInlayInspector()
	awaitInlaySignal(t, port.cancelled, "close did not cancel the exact inspection")
	stopped := make(chan struct{})
	go func() { app.StopAsyncIO(); close(stopped) }()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for !app.asyncIOStopped.Load() {
		select {
		case <-stopped:
			t.Fatal("shutdown returned before joining the inspection")
		case <-deadline.C:
			t.Fatal("shutdown did not reach its join boundary")
		default:
			runtime.Gosched()
		}
	}
	select {
	case <-stopped:
		t.Fatal("shutdown returned while the inspection was running")
	default:
	}
	port.unblock()
	select {
	case raw := <-result:
		msg, ok := raw.(inlayInspectionMsg)
		if !ok || !errors.Is(msg.err, context.Canceled) || msg.inspection.SetID != "" {
			t.Fatal("cancelled provider answer survived", raw)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancelled inspection did not return")
	}
	awaitInlaySignal(t, stopped, "shutdown did not join the inspection")
	if ed.Value() != "abc\n" || ed.Modified() {
		t.Fatal("inspection changed source")
	}
}

func TestInlayInspectionCanceledBeforeDispatchDoesNotStart(t *testing.T) {
	app, _, _, port, command := holdingInlayInspection(t)
	app.closeInlayInspector()
	if result := command(); result != nil {
		t.Fatalf("cancelled command produced %T", result)
	}
	select {
	case <-port.entered:
		t.Fatal("cancelled inspection reached the provider")
	default:
	}
}

func TestInlayInspectionTimeoutKeepsErrorAgainstLateSuccess(t *testing.T) {
	app, ed, reader, port, command := holdingInlayInspection(t)
	result := make(chan tea.Msg, 1)
	go func() { result <- command() }()
	awaitInlaySignal(t, port.entered, "inspection did not start")
	reader.deadline = time.Now().Add(-time.Second)
	app.syncInlayInspector()
	awaitInlaySignal(t, port.cancelled, "timeout did not cancel the exact inspection")
	if reader.reading || reader.readCancel != nil || !strings.Contains(inlayVisualReader(reader), "Refresh Inlay Hints") {
		t.Fatal("timeout lost its actionable error")
	}
	port.unblock()
	select {
	case raw := <-result:
		msg, ok := raw.(inlayInspectionMsg)
		if !ok || !errors.Is(msg.err, context.Canceled) || msg.inspection.SetID != "" {
			t.Fatal("late success retained authority", raw)
		}
		app.Update(msg)
	case <-time.After(10 * time.Second):
		t.Fatal("timed-out inspection did not return")
	}
	if app.inlayInspector != reader || reader.screen.InDetail() || !strings.Contains(inlayVisualReader(reader), "Refresh Inlay Hints") || ed.Value() != "abc\n" || ed.Modified() {
		t.Fatal("late answer changed the error or source")
	}
}
