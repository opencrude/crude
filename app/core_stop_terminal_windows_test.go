package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/core"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/terminalvt"
	"crude/types"
	"crude/workspace"
)

// Exercise the detached core and actual CLI/helper, not just a terminal manager
// sample. A foreground command refuses the stop without changing its owner.
// Once it returns normally, the same open shell must permit stop and handover.
func TestCoreStopAndHandoverAfterTerminalCommandCompletes(t *testing.T) {
	for _, shell := range []string{"pwsh.exe", "powershell.exe", "cmd.exe"} {
		t.Run(shell, func(t *testing.T) {
			command, err := exec.LookPath(shell)
			if err != nil {
				t.Skipf("%s is not installed", shell)
			}
			for _, method := range []string{"stop", "handover"} {
				t.Run(method, func(t *testing.T) {
					home := coreTestHome(t)
					t.Setenv("CRUDE_HOME", home)
					settings := types.TerminalSettings{
						Shell: types.TerminalShellCustom, Command: command,
						Args: []string{"-NoLogo", "-NoProfile"},
					}
					if shell == "cmd.exe" {
						settings.Args = []string{"/d", "/q"}
					}
					if err := SaveFileConfig(FileConfig{Terminal: &settings}); err != nil {
						t.Fatal(err)
					}
					root := filepath.Join(home, "project")
					if err := os.Mkdir(root, 0700); err != nil {
						t.Fatal(err)
					}
					finish := filepath.Join(root, "finish")
					// Inline code needs no script execution-policy change on the host.
					body := fmt.Sprintf("Write-Output PROGRAM_READY; while (-not (Test-Path -LiteralPath '%s')) { Start-Sleep -Milliseconds 25 }", strings.ReplaceAll(finish, "'", "''"))
					ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
					defer cancel()
					client, err := ConnectCore(ctx, home, core.NewClientID())
					if err != nil {
						t.Fatal(err)
					}
					defer func(c *coreclient.Client) { _ = c.Close() }(client)
					attached := attach(t, client, root)
					var info workspace.TerminalInfo
					if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.TerminalCreate, &workspace.TerminalCreateOpts{Cols: 120, Rows: 18}, &info); err != nil {
						t.Fatal(err)
					}
					var lease workspace.TerminalLease
					if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.TerminalControl, &coreprotocol.TerminalIDRequest{ID: info.ID}, &lease); err != nil {
						t.Fatal(err)
					}
					line := fmt.Sprintf(`powershell.exe -NoLogo -NoProfile -NonInteractive -Command "%s"; Write-Output ('COMMAND_' + 'DONE')`, body)
					if shell == "cmd.exe" {
						line = fmt.Sprintf(`powershell.exe -NoLogo -NoProfile -NonInteractive -Command "%s" & echo COMMAND_DONE`, body)
					}
					if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.TerminalInput, &coreprotocol.TerminalInputRequest{Lease: lease, Kind: "paste", Data: []byte(line)}, nil); err != nil {
						t.Fatal(err)
					}
					if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.TerminalInput, &coreprotocol.TerminalInputRequest{Lease: lease, Kind: "key", Key: terminalvt.Key{Code: terminalvt.KeyEnter}}, nil); err != nil {
						t.Fatal(err)
					}
					awaitCoreTerminalLine(t, ctx, client, attached.WorkspaceID, info.ID, "PROGRAM_READY")
					owner, err := readCoreOwner(home)
					if err != nil {
						t.Fatal(err)
					}
					if err := client.Close(); err != nil {
						t.Fatal(err)
					}
					result, err := StopCore(ctx, home, false)
					if err != nil || result.Accepted || !strings.Contains(strings.ToLower(result.Reason), "terminal") {
						t.Fatalf("live foreground work was not protected: %+v %v", result, err)
					}
					if current, err := readCoreOwner(home); err != nil || current != owner {
						t.Fatalf("refused stop changed its owner: %+v %v", current, err)
					}
					client, err = ConnectCore(ctx, home, core.NewClientID())
					if err != nil {
						t.Fatal("refused stop broke attachment:", err)
					}
					defer func(c *coreclient.Client) { _ = c.Close() }(client)
					attached = attach(t, client, root)
					if err := os.WriteFile(finish, nil, 0600); err != nil {
						t.Fatal(err)
					}
					awaitCoreTerminalLine(t, ctx, client, attached.WorkspaceID, info.ID, "COMMAND_DONE")
					if err := client.Close(); err != nil {
						t.Fatal(err)
					}
					if method == "handover" {
						if err := handoverCore(ctx, home); err != nil {
							t.Fatal("completed command left idle shell blocking handover:", err)
						}
					} else {
						exe, err := os.Executable()
						if err != nil {
							t.Fatal(err)
						}
						stop := exec.CommandContext(ctx, exe, "--core-stop")
						stop.Env = coreProfileEnv(home, "")
						if out, err := stop.CombinedOutput(); err != nil {
							t.Fatalf("idle shell required --force: %s: %v", out, err)
						}
					}
					if err := settleRestartLifetime(ctx, home); err != nil {
						t.Fatal("accepted stop did not release its lifetime:", err)
					}
					if _, err := readCoreOwner(home); !errors.Is(err, os.ErrNotExist) {
						t.Fatalf("stopped owner remains: %v", err)
					}
				})
			}
		})
	}
}

func awaitCoreTerminalLine(t *testing.T, ctx context.Context, client *coreclient.Client, workspaceID, terminalID, line string) {
	t.Helper()
	for {
		var p workspace.TerminalProjection
		if err := client.Call(ctx, workspaceID, coreprotocol.TerminalRows, &coreprotocol.TerminalRowsRequest{ID: terminalID, Start: -1}, &p); err != nil {
			t.Fatal(err)
		}
		var text strings.Builder
		for _, row := range p.Grid.Cells {
			var current strings.Builder
			for _, cell := range row {
				current.WriteString(cell.Text)
			}
			if strings.TrimSpace(current.String()) == line {
				if !p.Info.Alive {
					t.Fatal("shell exited instead of returning to its prompt")
				}
				return
			}
			text.WriteString(current.String())
			text.WriteByte('\n')
		}
		select {
		case <-ctx.Done():
			t.Fatalf("terminal did not reach %s: %s", line, text.String())
		case <-time.After(10 * time.Millisecond):
		}
	}
}
