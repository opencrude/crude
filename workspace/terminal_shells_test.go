package workspace

import (
	"crude/terminalvt"
	"runtime"
	"strings"
	"testing"
	"time"

	ttypes "crude/types"
)

func TestTerminalSettingsStartConfiguredShellAndEnvironment(t *testing.T) {
	settings := ttypes.TerminalSettings{Shell: ttypes.TerminalShellCustom, Command: "/bin/sh", Args: []string{"-c", `printf "VALUE=$CRUDE_TERMINAL_PROBE"; cat`}, Environment: map[string]string{"CRUDE_TERMINAL_PROBE": "from-settings"}}
	if runtime.GOOS == "windows" {
		settings.Command, settings.Args = "cmd.exe", []string{"/q", "/k", "echo VALUE=%CRUDE_TERMINAL_PROBE%"}
	}
	manager := NewTerminalManager(t.TempDir())
	service, err := NewTerminalService(manager)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.CloseAll)
	manager.SetSettings(settings)
	info, err := service.Create(TerminalCreateOpts{Cols: 60, Rows: 6})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		projection, err := service.Rows(info.ID, terminalvt.RowRequest{Start: -1, Count: 6})
		if err != nil {
			t.Fatal(err)
		}
		var text strings.Builder
		for _, row := range projection.Grid.Cells {
			for _, cell := range row {
				text.WriteString(cell.Text)
			}
		}
		if strings.Contains(text.String(), "VALUE=from-settings") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("configured shell or environment not used: %q", text.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTerminalSettingsRejectMissingShellWithGuidance(t *testing.T) {
	manager := NewTerminalManager(t.TempDir())
	service, err := NewTerminalService(manager)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.CloseAll)
	manager.SetSettings(ttypes.TerminalSettings{Shell: "definitely-not-a-shell"})
	if _, err := service.Create(TerminalCreateOpts{}); err == nil || !strings.Contains(err.Error(), "Settings › Terminal") {
		t.Fatalf("missing shell error = %v", err)
	}
	if len(manager.List()) != 0 {
		t.Fatal("a failed launch left a terminal behind")
	}
}
