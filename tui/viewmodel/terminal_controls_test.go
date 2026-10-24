package viewmodel

import (
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"golang.org/x/term"

	"crude/terminalvt"
	"crude/tui/chatprojection"
	"crude/tui/screens/shellchat"
	"crude/tui/screens/shellterm"
	"crude/tui/ui"
	"crude/workspace"
)

// Re-exec the native test binary inside the real PTY, not a shell with its own
// key bindings. This verifies both ConPTY console interrupts and Unix ISIG,
// plus raw byte delivery when the child explicitly disables signal processing.
func TestTerminalControlChild(t *testing.T) {
	args := os.Args
	if len(args) < 3 || args[len(args)-2] != "crude-control-child" {
		return
	}
	if args[len(args)-1] == "signal" {
		if err := prepareTerminalSignalChild(); err != nil {
			fmt.Println(err)
			os.Exit(5)
		}
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt)
		fmt.Println("CONTROL_CHILD_READY")
		select {
		case <-ch:
			fmt.Println("NATIVE_INTERRUPT_RECEIVED")
		case <-time.After(15 * time.Second):
			os.Exit(2)
		}
	} else {
		state, err := term.MakeRaw(int(os.Stdin.Fd()))
		if err != nil {
			fmt.Println(err)
			os.Exit(3)
		}
		fmt.Print("CONTROL_CHILD_READY\r\n")
		data := make([]byte, 10)
		_, err = io.ReadFull(terminalControlReader(), data)
		_ = term.Restore(int(os.Stdin.Fd()), state)
		if err != nil {
			fmt.Println(err)
			os.Exit(4)
		}
		fmt.Printf("RAW_CONTROLS:%x\r\n", data)
	}
	os.Exit(0)
}

func TestTerminalControlsNativeSignalAndRawKeysDockAndFloat(t *testing.T) {
	for _, floating := range []bool{false, true} {
		for _, mode := range []string{"signal", "raw"} {
			t.Run(fmt.Sprintf("float=%v/%s", floating, mode), func(t *testing.T) {
				app := newFullTestApp(t)
				wsOf(app).Logger = stubLogger{}
				runTerminalCommand(t, app, func() tea.Msg { return tea.WindowSizeMsg{Width: 120, Height: 40} })
				bin, err := os.Executable()
				if err != nil {
					t.Fatal(err)
				}
				cwd := t.TempDir()
				info, err := createAppTerminal(t, app, workspace.TerminalCreateOpts{Command: bin, Args: []string{"-test.run=^TestTerminalControlChild$", "--", "crude-control-child", mode}, CWD: cwd, Cols: 100, Rows: 10})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(app.discardTerminalUI)
				app.dockTerminal(info.ID)
				if floating {
					app.promoteTerminal(info.ID)
				}
				app.paintShellFrame()
				runTerminalCommand(t, app, app.drainTerminalCommands())
				service := app.terminalService()
				await := func(want string) {
					t.Helper()
					deadline := time.Now().Add(8 * time.Second)
					var text string
					for time.Now().Before(deadline) {
						p, err := service.Rows(info.ID, terminalvt.RowRequest{Start: -1})
						if err != nil {
							t.Fatal(err)
						}
						app.termScreens[info.ID].SetProjection(p)
						text = terminalProjectionText(p)
						if strings.Contains(text, want) {
							return
						}
						time.Sleep(10 * time.Millisecond)
					}
					t.Fatalf("missing %q in native child output: %q", want, text)
				}
				await("CONTROL_CHILD_READY")
				// Leave a real retained terminal selection: Ctrl+C is still the
				// child's key, not Copy; Ctrl+Shift+C is the explicit copy chord.
				s := app.termScreens[info.ID]
				s.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl | tea.ModShift}))
				var keys []rune
				want := "NATIVE_INTERRUPT_RECEIVED"
				if mode == "signal" {
					keys = []rune{'c'}
				} else {
					keys = []rune{'c', 'd', 'z', 's', 'q', '\\', 'l', 'u', 'w', 'r'}
					want = "RAW_CONTROLS:03041a13111c0c151712"
				}
				for _, code := range keys {
					// An unfocused pane's stale drag/selection must not intercept
					// any child key, even copy or flow-control chords.
					chat := app.chatScreen
					chat.SetMessages([]chatprojection.MessageRow{{ID: "stale", Lines: []string{"unfocused selection"}}}, shellchat.PaintParams{Compose: shellchat.ComposeParams{InnerW: 40, Theme: app.Theme}})
					chat.SyncScrollMetrics(1, 1)
					chat.Layout(ui.LayoutContext{}, ui.Rect{W: 40, H: 1})
					chat.Handle(ui.EventContext{LocalX: 0, LocalY: 0}, ui.MouseClickEvent(tea.MouseClickMsg{Button: tea.MouseLeft}))
					chat.Handle(ui.EventContext{LocalX: 6, LocalY: 0}, ui.MouseMotionEvent(tea.MouseMotionMsg{Button: tea.MouseLeft}))
					if !chat.HasActiveSelection() {
						t.Fatal("missing stale selection fixture")
					}
					_, cmd := app.Update(tea.KeyPressMsg{Code: code, Mod: tea.ModCtrl})
					runTerminalCommand(t, app, cmd)
				}
				await(want)
				app.forceFrame()
				if painted := app.View().RenderContent(); !strings.Contains(painted, want) {
					t.Fatal("native control result absent from rendered terminal")
				}
			})
		}
	}
}

func TestTerminalModifiedScrollChordsPassThrough(t *testing.T) {
	app, id := newScrollingTerminalApp(t)
	s := app.termScreens[id]
	var commands []shellterm.Command
	s.SetCommand(func(c shellterm.Command) tea.Cmd { commands = append(commands, c); return nil })
	for _, code := range []rune{tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd} {
		s.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: code, Mod: tea.ModCtrl | tea.ModShift}))
	}
	if len(commands) != 4 {
		t.Fatalf("modified scroll chords stolen: %+v", commands)
	}
	for _, c := range commands {
		if c.Kind != shellterm.CommandKey {
			t.Fatal("modified key became scroll command")
		}
	}
}
