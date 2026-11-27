package dataupdate

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/core/coreprotocol"
	"github.com/charmbracelet/x/ansi"
)

// A short update can finish before the first frame. The shell must not wait
// for a second final event before continuing into the session picker.
func TestUpdateAlreadyFinishedWhenPageOpens(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()
	m := NewModel(ctx, coreprotocol.DataUpdate{Phase: coreprotocol.DataUpdateFinished, Final: true, Files: 1, FilesDone: 1}, nil, nil)
	if !strings.Contains(m.View().RenderContent(), "100%") {
		t.Fatal("final frame did not show 100%")
	}
	_, err := tea.NewProgram(m, tea.WithContext(ctx), tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer()).Run()
	if err != nil || m.Result() != Continue {
		t.Fatalf("finished page: result=%v err=%v", m.Result(), err)
	}
}

// TestDataUpdateVisualAcceptance drives the actual detached page with a fixed
// progress reading so both terminal sizes can capture the same frame.
func TestDataUpdateVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_DATA_UPDATE") != "1" {
		t.Skip("dedicated terminal capture")
	}
	u := coreprotocol.DataUpdate{Phase: coreprotocol.DataUpdateUpdating, Version: "vNext", Files: 12, FilesDone: 5, Done: 45, Total: 100, Step: "Sessions and workspaces", Backup: `C:\Users\Example\.crude\backups\update-20261127-092628-3621304585`, Stores: []coreprotocol.DataUpdateStore{{Name: "Sessions and workspaces", Files: 10, Done: 5}, {Name: "Local History", Files: 2}}}
	result, err := Run(t.Context(), u, nil, nil)
	if err != nil || result != Quit {
		t.Fatalf("update page exit=%v err=%v", result, err)
	}
}

func TestUpdatePathsWrapWithoutLosingTheirEnd(t *testing.T) {
	path := "C:/Users/" + strings.Repeat("long-directory/", 8) + "backup-3621304585"
	for _, size := range []tea.WindowSizeMsg{{Width: 158, Height: 39}, {Width: 66, Height: 23}} {
		m := NewModel(t.Context(), coreprotocol.DataUpdate{Phase: coreprotocol.DataUpdateUpdating, Backup: path, Files: 228, FilesDone: 165, Done: 52, Total: 100}, nil, nil)
		m.Update(size)
		view := ansi.Strip(m.View().RenderContent())
		for _, want := range []string{"52%", "165 of 228 files complete", "Backup folder", "3621304585"} {
			if !strings.Contains(view, want) {
				t.Fatalf("%+v missing %q: %s", size, want, view)
			}
		}
	}
}
