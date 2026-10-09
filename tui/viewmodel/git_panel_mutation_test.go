package viewmodel

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/ui"
	"crude/workspace"
)

func TestSourceControlMutationHasDeadline(t *testing.T) {
	app := newFullTestApp(t)
	app.sourceControlTimeout = 20 * time.Millisecond

	started := time.Now()
	cmd := app.sourceControlMutation(
		"unreachable",
		nil,
		func(ctx context.Context, _ shellport.GitService) error {
			<-ctx.Done()
			return ctx.Err()
		},
	)
	if cmd == nil {
		t.Fatal("source-control mutation returned no command")
	}
	msg, ok := cmd().(sourceControlResultMsg)
	if !ok {
		t.Fatalf("completion type = %T, want sourceControlResultMsg", msg)
	}
	if !errors.Is(msg.err, context.DeadlineExceeded) {
		t.Fatalf("mutation error = %v, want deadline exceeded", msg.err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("deadline took %v, want a prompt cancellation", elapsed)
	}
}

func TestDiscardGitChangeUsesSafeConfirmation(t *testing.T) {
	app := newFullTestApp(t)
	change := workspace.GitChange{
		Path: "main.go", AbsolutePath: app.ws.RootPath() + "/main.go",
		Area: workspace.GitAreaWorking, Code: "M", Description: "Modified",
	}
	app.startDiscardGitChange(change)
	if !app.overlayHost.IsOpen(ui.OverlayTreeDelete) {
		t.Fatal("discard confirmation did not open")
	}
	if app.GitDiscardPending == nil || app.GitDiscardPending.Path != change.Path {
		t.Fatalf("pending=%#v", app.GitDiscardPending)
	}
	if app.TreeDeleteChoice != 1 {
		t.Fatalf("default choice=%d want Cancel", app.TreeDeleteChoice)
	}

	// Enter on the safe default cancels; no Git mutation command is started.
	app.handleTreeDeleteConfirmKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if app.overlayHost.IsOpen(ui.OverlayTreeDelete) || app.GitDiscardPending != nil {
		t.Fatal("cancel did not close and clear discard confirmation")
	}
}
