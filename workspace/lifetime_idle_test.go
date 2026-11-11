package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"crude/document"
	"crude/internal/testterminal"
	"crude/terminalvt"
	"crude/types"
)

func TestIdleDocumentLeaseRefusesViewsAndRestoresOpen(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "retained.txt")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	s := NewDocumentService(root, nil, nil)
	defer s.Close()
	owner := document.Owner{ClientID: "shell", Epoch: 1}
	d, err := s.Open(owner, path)
	if err != nil {
		t.Fatal(err)
	}
	if u, err := s.beginIdleLease(); err == nil {
		u()
		t.Fatal("open document accepted")
	} else if !strings.Contains(err.Error(), path) {
		t.Fatal(err)
	}
	if err := s.CloseView(owner, d.ID); err != nil {
		t.Fatal(err)
	}
	u, err := s.beginIdleLease()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Open(owner, path); err == nil {
		t.Fatal("document acquired during stop")
	}
	u()
	if _, err := s.Open(owner, path); err != nil {
		t.Fatal("refused stop broke open", err)
	}
}

func TestIdleStorageLeaseExcludesReadersAndRollsBack(t *testing.T) {
	u, err := lockWorkspaceStorage(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	if stop, ok := TryIdleStorageLease(); ok {
		stop()
		t.Fatal("catalog reader crossed stop")
	}
	u()
	stop, ok := TryIdleStorageLease()
	if !ok {
		t.Fatal("reader leaked")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if release, err := lockWorkspaceStorage(ctx, false); err == nil {
		release()
		t.Fatal("new reader crossed stop")
	}
	stop()
	u, err = lockWorkspaceStorage(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	u()
}

func TestIdleTerminalLeaseRefusesCreationAndControllers(t *testing.T) {
	s, _ := testTerminalService(t)
	command, args := testterminal.Command(t, "idle")
	opts := TerminalCreateOpts{Command: command, Args: args, Cols: 20, Rows: 4}
	u, err := s.beginIdleLease(IdleAutomatic)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(opts); err == nil {
		t.Fatal("created terminal through idle fence")
	}
	u()
	info, err := s.Create(opts)
	if err != nil {
		t.Fatal(err)
	}
	if u, err := s.beginIdleLease(IdleAutomatic); err == nil {
		u()
		t.Fatal("live terminal accepted")
	}
	// A terminal started with its own command is that program's work.
	if u, err := s.beginIdleLease(IdleExplicitStop); err == nil {
		u()
		t.Fatal("explicit stop accepted a terminal command")
	}
	lease, err := s.Acquire(info.ID, TerminalController{ClientID: "shell", Epoch: 1})
	if err != nil {
		t.Fatal("refusal mutated controller", err)
	}
	if err := s.Release(lease); err != nil {
		t.Fatal(err)
	}
}

// An explicit stop must not demand --force for a shell that waits at its
// prompt. It still refuses while the shell runs a foreground program, and
// names that program.
func TestExplicitStopClosesIdleShellButNotRunningProgram(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX job control")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is required for job control")
	}
	s, _ := testTerminalService(t)
	s.manager.SetSettings(types.TerminalSettings{Shell: types.TerminalShellCustom, Command: bash, Args: []string{"--norc", "--noprofile", "-i"}})
	info, err := s.Create(TerminalCreateOpts{Cols: 80, Rows: 24})
	if err != nil {
		t.Fatal(err)
	}
	await := func(want bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			u, err := s.beginIdleLease(IdleExplicitStop)
			if err == nil {
				u()
			}
			if (err == nil) == want {
				if !want && !strings.Contains(err.Error(), "is running sleep") {
					t.Fatal("refusal does not name the program:", err)
				}
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("explicit stop accepted=%v, want %v: %v", err == nil, want, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	await(true)
	if u, err := s.beginIdleLease(IdleAutomatic); err == nil {
		u()
		t.Fatal("automatic idle exit would close an open shell")
	}
	if err := s.manager.Write(info.ID, []byte("sleep 30\n")); err != nil {
		t.Fatal(err)
	}
	await(false)
	if err := s.manager.Write(info.ID, []byte{0x03}); err != nil {
		t.Fatal(err)
	}
	await(true)
}

func TestExplicitStopProtectsBackgroundShellWork(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("native POSIX child-process observation")
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is required for job control")
	}
	s, _ := testTerminalService(t)
	s.manager.SetSettings(types.TerminalSettings{Shell: types.TerminalShellCustom, Command: bash, Args: []string{"--norc", "--noprofile", "-i"}})
	info, err := s.Create(TerminalCreateOpts{Cols: 100, Rows: 12})
	if err != nil {
		t.Fatal(err)
	}
	dirty, unsubscribe, err := s.Subscribe(info.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	awaitLine := func(marker string) {
		t.Helper()
		for {
			page, err := s.Rows(info.ID, terminalvt.RowRequest{Start: -1})
			if err != nil {
				t.Fatal(err)
			}
			for _, line := range strings.Split(terminalText(page), "\n") {
				if strings.TrimSpace(line) == marker {
					return
				}
			}
			select {
			case <-dirty:
			case <-ctx.Done():
				t.Fatalf("terminal did not reach %s: %q", marker, terminalText(page))
			}
		}
	}
	// Readiness comes from the child, not a guessed delay. The interactive
	// shell is free at its prompt while that child keeps running.
	if err := s.manager.Write(info.ID, []byte("PS1=; (printf '\\nBG_READY\\n'; exec sleep 60) & job=$!\n")); err != nil {
		t.Fatal(err)
	}
	awaitLine("BG_READY")
	if release, err := s.beginIdleLease(IdleExplicitStop); err == nil {
		release()
		t.Fatal("explicit stop would terminate background shell work")
	}
	if err := s.manager.Write(info.ID, []byte("kill \"$job\"; wait \"$job\" 2>/dev/null; printf '\\nBG_DONE\\n'\n")); err != nil {
		t.Fatal(err)
	}
	awaitLine("BG_DONE")
	release, err := s.beginIdleLease(IdleExplicitStop)
	if err != nil {
		t.Fatal("joined background work left an idle shell blocked:", err)
	}
	release()
}
