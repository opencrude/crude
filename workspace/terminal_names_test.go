package workspace

import (
	"errors"
	"strings"
	"testing"

	"crude/internal/testterminal"
	"crude/terminalvt"
)

func TestTerminalServiceRenameAndClearKeepProcessModesAndLeaseFences(t *testing.T) {
	svc, _ := testTerminalService(t)
	command, args := testterminal.Command(t, "idle")
	info, err := svc.Create(TerminalCreateOpts{Command: command, Args: args, Cols: 40, Rows: 6})
	if err != nil {
		t.Fatal(err)
	}
	lease, err := svc.Acquire(info.ID, TerminalController{ClientID: "owner", Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	svc.Send(TerminalOutputMsg{ID: info.ID, Data: []byte("\x1b[?2004h\x1b[?1004h" + strings.Repeat("OLD-CONTENT\r\n", 20))})
	if err := svc.Rename(lease, "Model training"); err != nil {
		t.Fatal(err)
	}
	svc.manager.SetTitle(info.ID, "pwsh application title")
	if err := svc.Clear(lease); err != nil {
		t.Fatal(err)
	}
	page, err := svc.Rows(info.ID, terminalvt.RowRequest{Start: -1, Count: 0})
	if err != nil {
		t.Fatal(err)
	}
	if page.Info.DisplayName() != "Model training" || page.Info.Number != 1 ||
		page.Info.PID != info.PID || !page.Grid.Modes.BracketedPaste || !page.Grid.Modes.FocusReporting ||
		page.Grid.Total != page.Grid.GridRows {
		t.Fatalf("name or clear replaced the PTY or its modes: %+v", page)
	}
	if strings.Contains(terminalText(page), "OLD-CONTENT") {
		t.Fatal("clear left terminal content")
	}
	if err := svc.Rename(lease, "bad\nname"); err == nil {
		t.Fatal("control characters entered terminal labels")
	}
	if err := svc.Release(lease); err != nil {
		t.Fatal(err)
	}
	replacement, err := svc.Acquire(info.ID, TerminalController{ClientID: "viewer", Epoch: 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.Rename(lease, "stale"); !errors.Is(err, ErrTerminalControl) {
		t.Fatal("stale lease renamed a terminal", err)
	}
	if err := svc.Clear(lease); !errors.Is(err, ErrTerminalControl) {
		t.Fatal("stale lease cleared a terminal", err)
	}
	if err := svc.Rename(replacement, "New owner"); err != nil {
		t.Fatal(err)
	}
}
