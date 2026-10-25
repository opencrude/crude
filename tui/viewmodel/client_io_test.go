package viewmodel

import (
	"context"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func TestClientIOCommandRefusesCancelledOrStaleStart(t *testing.T) {
	for _, mode := range []string{"cancel", "switch", "rollback-switch", "shutdown"} {
		t.Run(mode, func(t *testing.T) {
			app := newFullTestApp(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			cmd := app.clientIOCommand(ctx, func(context.Context) tea.Msg { t.Error("unowned command started"); return nil })
			switch mode {
			case "cancel":
				cancel()
			case "switch":
				app.workspaceSwitchGen.Add(1)
			case "rollback-switch":
				generation := app.workspaceSwitchGen.Load()
				app.workspaceSwitchGen.Add(1)
				app.cancelClientIOOperations()
				app.workspaceSwitchGen.Store(generation)
			case "shutdown":
				app.StopAsyncIO()
			}
			if msg := cmd(); msg != nil {
				t.Fatalf("rejected command published a result: %#v", msg)
			}
		})
	}
}

func TestClientIOShutdownCancelsAndJoinsActualCommand(t *testing.T) {
	app := newFullTestApp(t)
	started, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	cmd := app.clientIOCommand(context.Background(), func(ctx context.Context) tea.Msg {
		close(started)
		<-ctx.Done()
		<-release // A non-context-aware owner still must finish before teardown.
		return nil
	})
	go cmd()
	<-started
	go func() { app.StopAsyncIO(); close(done) }()
	select {
	case <-done:
		t.Fatal("shutdown returned before the owned command completed")
	case <-time.After(25 * time.Millisecond):
	}
	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not join the released command")
	}
}
