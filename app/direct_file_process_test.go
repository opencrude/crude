package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/core/coreprotocol"
	"crude/internal/terminalvt"
	"crude/workspace"
)

// Exercise the real argv, core process, shell attachment and terminal UI. The
// private terminal is a PTY/ConPTY, not a second implementation of the chooser.
func TestDirectFileProcessOpensEditorWithoutFolderTree(t *testing.T) {
	profile := coreTestHome(t)
	root := t.TempDir()
	path := filepath.Join(root, "opened.txt")
	sibling := "sidebar-only-marker.txt"
	const text = "Direct file process sentinel"
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, sibling), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(DataDir(profile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(DataDir(profile), "config.json"), []byte(`{"onboarding":{"introduction_completed":true,"ai_setup_reminder_hidden":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	shell, err := workspace.NewTerminalService(workspace.NewTerminalManager(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	defer shell.CloseAll()
	info, err := shell.Create(workspace.TerminalCreateOpts{Command: os.Args[0], Args: []string{"--client-profile", "direct-file-test", "--", path}, Cols: 160, Rows: 40})
	if err != nil {
		t.Fatal(err)
	}
	changed, unsubscribe, err := shell.Subscribe(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	lease, err := shell.Acquire(info.ID, workspace.TerminalController{ClientID: "input", Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(20 * time.Second)
	defer timer.Stop()
	dismissed := false
	for {
		page, err := shell.Rows(info.ID, terminalvt.RowRequest{Start: -1})
		if err != nil {
			t.Fatal(err)
		}
		var buf strings.Builder
		for _, row := range page.Grid.Cells {
			for _, c := range row {
				buf.WriteString(c.Text)
			}
			buf.WriteByte('\n')
		}
		view := buf.String()
		if strings.Contains(view, "Trust this folder?") && !dismissed {
			if err := shell.Input(lease, []byte("\x1b")); err != nil {
				t.Fatal(err)
			}
			dismissed = true
		}
		if strings.Contains(view, text) && strings.Contains(view, "opened.txt") && !strings.Contains(view, "Trust this folder?") {
			if strings.Contains(view, sibling) {
				t.Fatal("file launch left the folder tree open")
			}
			break
		}
		select {
		case <-changed:
		case <-timer.C:
			t.Fatalf("direct launch did not show file contents: %s", view)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	status, err := CoreStatus(ctx, profile)
	if err != nil || len(status.Workspaces) != 1 || status.Workspaces[0].Root != root {
		t.Fatalf("process attached wrong workspace: %+v, %v", status.Workspaces, err)
	}
	observer, err := ConnectCore(ctx, profile, "file-observer")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = observer.Close() }()
	var attached coreprotocol.AttachResult
	// Use a read-only observation of the file actor without promoting it.
	req := coreprotocol.AttachRequest{Root: root}
	req.Entry = coreprotocol.EntryIntent{Explicit: true, FileOnly: true}
	if err := observer.Call(ctx, "", coreprotocol.Attach, &req, &attached); err != nil {
		t.Fatal(err)
	}
	var lsp coreprotocol.LSPSnapshot
	if err := observer.Call(ctx, attached.WorkspaceID, coreprotocol.LSPSnapshotMethod, &coreprotocol.LSPRequest{}, &lsp); err != nil {
		t.Fatal(err)
	}
	if lsp.Settings.Enabled || len(lsp.Health) != 0 {
		t.Fatal("direct file requested language servers", lsp.Health)
	}
}
