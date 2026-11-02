//go:build !cgo

package workspace

import (
	"errors"
	"testing"

	"crude/internal/testterminal"
	"crude/terminalvt"
)

func TestTerminalServicePreparationUnavailableCleansPTY(t *testing.T) {
	e, err := terminalvt.New(terminalvt.Options{})
	if err == nil {
		e.Close()
		t.Fatal("engine unexpectedly available")
	}
	if !errors.Is(err, terminalvt.ErrNoCGO) {
		t.Fatal(err)
	}
	s, _ := testTerminalService(t)
	cmd, args := testterminal.Command(t, "echo")
	if _, err := s.Create(TerminalCreateOpts{Command: cmd, Args: args}); !errors.Is(err, terminalvt.ErrNoCGO) {
		t.Fatal(err)
	}
	if len(s.manager.List()) != 0 || len(s.items) != 0 {
		t.Fatal("unavailable engine left a live PTY/resource")
	}
}
