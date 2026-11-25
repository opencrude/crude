//go:build windows && cgo

package viewmodel

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"crude/workspace"
)

const consoleTextProbe = "CRUDE_TEST_CONPTY_TEXT_PROBE"

// Only the private PTY child reads console records. Never use the console of
// the shell running the test suite, or discard pending human input on it.
func TestPrivateConsoleTextProbe(t *testing.T) {
	dir := os.Getenv(consoleTextProbe)
	if dir == "" {
		return
	}
	reader, err := uv.NewCancelReader(os.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	events := make(chan uv.Event, 64)
	done := make(chan error, 1)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	go func() { done <- uv.NewTerminalReader(reader, "").StreamEvents(ctx, events) }()
	defer func() {
		cancel()
		reader.Cancel()
		<-done
		if err := reader.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := os.WriteFile(filepath.Join(dir, "ready"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	var keys []tea.KeyPressMsg
	for {
		select {
		case event := <-events:
			key, ok := event.(uv.KeyPressEvent)
			if !ok || key.Text == "" {
				continue
			}
			keys = append(keys, tea.KeyPressMsg(key))
			if strings.HasSuffix(key.Text, "!") {
				data, err := json.Marshal(keys)
				if err != nil {
					t.Fatal(err)
				}
				tmp := filepath.Join(dir, "received.tmp")
				if err := os.WriteFile(tmp, data, 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(tmp, filepath.Join(dir, "received")); err != nil {
					t.Fatal(err)
				}
				return
			}
		case <-ctx.Done():
			t.Fatal("the private console did not receive the end of the input")
		}
	}
}

func TestWindowsConsoleEmojiReachesEachFocusedControl(t *testing.T) {
	const text = "x😂😂a👩‍💻☀️!"
	dir := t.TempDir()
	t.Setenv(consoleTextProbe, dir)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pty := workspace.NewTerminalManager(dir)
	defer pty.CloseAll()
	child, err := pty.Create(workspace.TerminalCreateOpts{
		Command: exe, Args: []string{"-test.run=^TestPrivateConsoleTextProbe$"}, CWD: dir, Cols: 80, Rows: 24,
	})
	if err != nil {
		t.Fatal(err)
	}
	read := func(name string) []byte {
		t.Helper()
		path := filepath.Join(dir, name)
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
			data, err := os.ReadFile(path)
			if err == nil {
				return data
			}
			if !os.IsNotExist(err) {
				t.Fatal(err)
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("did not receive %q from the private console", name)
		return nil
	}
	read("ready")
	if err := pty.Write(child.ID, []byte(text)); err != nil {
		t.Fatal(err)
	}
	var keys []tea.KeyPressMsg
	if err := json.Unmarshal(read("received"), &keys); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"composer", "editor", "find field"} {
		t.Run(target, func(t *testing.T) {
			app, _ := newInputContractEditorApp(t, "notes.txt")
			app.Update(tea.WindowSizeMsg{Width: 110, Height: 28})
			ed := app.EditorState.Editor
			ed.SetCursorPosition(0, 0)
			before := ed.Value()
			want, value := text+before, ed.Value
			switch target {
			case "composer":
				app.AddOrchestrator("writer")
				app.maybeHydrateComposerDraft()
				app.setFocusInput()
				want, value = text, app.Input.Value
			case "find field":
				app.View()
				app.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
				if app.FindReplace == nil {
					t.Fatal("Ctrl+F did not open the find field")
				}
				want, value = text, app.FindReplace.Query
			}
			for _, key := range keys {
				app.Update(key)
			}
			if got := value(); got != want {
				t.Fatalf("%s received %q; want %q", target, got, want)
			}
			if target != "editor" && ed.Value() != before {
				t.Fatal("typing also changed the unfocused document")
			}
		})
	}
}
