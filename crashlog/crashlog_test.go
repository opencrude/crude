package crashlog_test

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/buildinfo"
	"crude/crashlog"
)

func TestProcessCrashReports(t *testing.T) {
	for _, role := range []string{"tui", "core"} {
		for _, failure := range []string{"normal", "main", "worker", "runtime-fatal", "tea-update", "tea-command", "blocked-cleanup"} {
			t.Run(role+"/"+failure, func(t *testing.T) {
				home := t.TempDir()
				ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCrashProcessHelper$")
				cmd.Env = append(os.Environ(), buildinfo.HomeEnv+"="+home, "CRUDE_CRASH_TEST="+failure, "CRUDE_CRASH_ROLE="+role)
				cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
				err := cmd.Run()
				if failure == "normal" && err != nil || failure != "normal" && err == nil {
					t.Fatalf("unexpected process outcome: %v", err)
				}
				files, err := filepath.Glob(filepath.Join(home, buildinfo.DataDir(), "crashes", "*.log"))
				if err != nil {
					t.Fatal(err)
				}
				if failure == "normal" {
					if len(files) != 0 {
						t.Fatal("clean shutdown left a crash report", files)
					}
					return
				}
				if len(files) != 1 || !strings.HasPrefix(filepath.Base(files[0]), role+"-") {
					t.Fatal("missing process-specific crash report", files)
				}
				data, err := os.ReadFile(files[0])
				if err != nil {
					t.Fatal(err)
				}
				text := string(data)
				for _, want := range []string{"Crude " + role + " crash report", "Build: ", "Source: ", "goroutine ", "crashlog_test"} {
					if !strings.Contains(text, want) {
						t.Fatalf("report missing %q:\n%s", want, text)
					}
				}
				if !strings.Contains(text, "CRASH_SENTINEL") && failure != "runtime-fatal" {
					t.Fatal("original panic reason was lost", text)
				}
				if runtime.GOOS != "windows" {
					info, _ := os.Stat(files[0])
					if info.Mode().Perm() != 0600 {
						t.Fatal("crash report is not private", info.Mode())
					}
				}
			})
		}
	}
}

// Each subprocess installs the production recorder. Fatal runtime and worker
// cases cannot be simulated by calling a helper that merely writes a stack.
func TestCrashProcessHelper(t *testing.T) {
	failure := os.Getenv("CRUDE_CRASH_TEST")
	if failure == "" {
		t.Skip("subprocess helper")
	}
	home, err := buildinfo.Home()
	if err != nil {
		t.Fatal(err)
	}
	report, err := crashlog.Start(home, os.Getenv("CRUDE_CRASH_ROLE"))
	if err != nil {
		t.Fatal(err)
	}
	defer report.Finish()
	switch failure {
	case "normal":
		return
	case "main":
		panic("CRASH_SENTINEL main")
	case "worker":
		go func() { panic("CRASH_SENTINEL worker") }()
		select {}
	case "runtime-fatal":
		var mutex sync.Mutex
		mutex.Unlock() // runtime.throw, not a recoverable panic
	default:
		p := tea.NewProgram(crashModel{failure}, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(),
			tea.WithPanicHandler(func(value any, stack []byte) {
				crashlog.Recovered(value, stack)
				if failure == "blocked-cleanup" {
					// Exit without Finish: disk evidence must already be complete.
					os.Exit(23)
				}
			}))
		if _, err := p.Run(); !errors.Is(err, tea.ErrProgramPanic) {
			t.Fatal("framework did not return the original panic outcome", err)
		}
		report.Finish() // recovered panic report must survive normal unwind
		os.Exit(24)
	}
}

type crashModel struct{ failure string }

func (m crashModel) Init() tea.Cmd {
	if m.failure == "tea-command" {
		return func() tea.Msg { panic("CRASH_SENTINEL command") }
	}
	return func() tea.Msg { return "crash" }
}
func (m crashModel) Update(tea.Msg) (tea.Model, tea.Cmd) { panic("CRASH_SENTINEL update") }
func (crashModel) View() tea.View                        { return tea.NewView("") }
