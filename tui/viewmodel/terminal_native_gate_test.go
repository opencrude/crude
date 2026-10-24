package viewmodel

// This fixture measures a real connected shell, not a replica/Rows microbench.
// The core is a separate process so its parser and GC cannot enter shell CPU
// accounting. Output evidence stops at the renderer's writer: it does NOT prove
// that an outer terminal displayed the frames. Use scripts/tuishot.sh separately.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/metrics"
	"strconv"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"crude/buildinfo"
	"crude/core"
	"crude/coreclient"
	"crude/corewire"
	"crude/history"
	"crude/session"
	"crude/shellport"
	"crude/shellport/remote"
	"crude/tui/perf"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

// TestTerminalNativeGateCoreHelper is re-executed only by the opt-in fixture.
// Closing its stdin owns cancellation, Serve join, Runtime.Close and PTY reap.
func TestTerminalNativeGateCoreHelper(t *testing.T) {
	if os.Getenv("CRUDE_TERMINAL_GATE_CORE") != "1" {
		t.Skip("external core helper")
	}
	dir := os.Getenv("CRUDE_TERMINAL_GATE_DIR")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt, err := core.New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "", "terminal-gate", types.Settings{}, nil)
		ws.SetAppDefaults(types.AppSettings{
			LocalHistory: types.LocalHistorySettings{Enabled: "off"},
			Memory:       types.MemorySettings{AutoRecall: "off", AutoExtract: "off", Consolidation: "off"},
			Terminal: types.TerminalSettings{Shell: types.TerminalShellCustom,
				Command: os.Getenv("CRUDE_TERMINAL_GATE_PYTHON"),
				Args:    []string{os.Getenv("CRUDE_TERMINAL_GATE_SCRIPT"), "--child"}, LoginShell: "off"},
		})
		ws.SetBackendConfig(&session.Config{}, dir)
		return ws, nil
	}, filepath.Join(dir, "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rt.Close(); err != nil {
			t.Error(err)
		}
	}()
	listener, err := corewire.ListenLocal(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.Copy(io.Discard, os.Stdin); cancel() }()
	fmt.Println("TERMINAL_GATE_CORE_READY")
	if err := core.Serve(ctx, rt, listener); err != nil {
		t.Error(err)
	}
}

type terminalGateKey struct {
	EnqueueUnixNS int64  `json:"enqueue_unix_ns"`
	Text          string `json:"text"`
}

type terminalGateLoop struct {
	Kind      string `json:"kind"`
	EndUnixNS int64  `json:"end_unix_ns"`
	UpdateNS  int64  `json:"update_ns"`
	ViewNS    int64  `json:"view_ns"`
	PresentNS int64  `json:"present_ns"`
}

type terminalGateResult struct {
	Schema                int                `json:"schema"`
	Cohort                string             `json:"cohort"`
	Workload              string             `json:"workload"`
	ShellPID              int                `json:"shell_pid"`
	ChildPID              int                `json:"pty_child_pid"`
	Cols                  int                `json:"cols"`
	Rows                  int                `json:"rows"`
	StartUnixNS           int64              `json:"start_unix_ns"`
	EndUnixNS             int64              `json:"end_unix_ns"`
	ContentFlushUnixNS    []int64            `json:"content_flush_unix_ns"`
	OutputBytes           uint64             `json:"output_bytes"`
	ShellGCCPUSeconds     float64            `json:"shell_gc_cpu_seconds"`
	ShellActiveCPUSeconds float64            `json:"shell_active_cpu_seconds"`
	ShellGCCPUPercent     float64            `json:"shell_gc_cpu_percent"`
	Keys                  []terminalGateKey  `json:"keys"`
	Boundary              string             `json:"boundary"`
	Loops                 []terminalGateLoop `json:"loop_samples,omitempty"`
	TerminalRect          [4]int             `json:"terminal_rect_cells"`
}

// ObserveFlush, rather than View calls or received projections, marks output.
// Require substantial truecolor content and a new output hash, excluding idle
// cursor/blink/status writes. Hashing and scanning are included in shell cost.
type terminalGateWriter struct {
	sync.Mutex
	output      io.Writer
	active      bool
	colors      int
	hash, prior uint64
	bytes       uint64
	content     []int64
	loop        *perf.LoopObserver
	loops       []terminalGateLoop
}

// Preserve the real terminal descriptor through measurement wrapping. Without
// it Bubble Tea cannot put stdin into raw mode or use the actual outer geometry;
// line-discipline CRLF expansion would invalidate the pixel capture.
type terminalGateTTYWriter struct {
	*terminalGateWriter
	file *os.File
}

func (w *terminalGateTTYWriter) Fd() uintptr                { return w.file.Fd() }
func (w *terminalGateTTYWriter) Read(p []byte) (int, error) { return w.file.Read(p) }
func (*terminalGateTTYWriter) Close() error                 { return nil } // the test never owns stdout

func (w *terminalGateWriter) Write(p []byte) (int, error) {
	n, err := w.output.Write(p)
	w.Lock()
	defer w.Unlock()
	if w.active {
		w.bytes += uint64(n)
		w.colors += bytes.Count(p[:n], []byte(";2;"))
		for _, b := range p[:n] {
			w.hash = (w.hash ^ uint64(b)) * 1099511628211
		}
	}
	return n, err
}

func (w *terminalGateWriter) ObserveLoop(s tea.LoopSample) {
	w.loop.ObserveLoop(s)
	// Only Update writes loop samples. Do not acquire the output writer's lock:
	// instrumentation must not put keyboard admission behind output hashing.
	if len(w.loops) >= 4096 {
		return
	}
	kind := "other"
	switch s.Msg.(type) {
	case tea.KeyPressMsg:
		kind = "key"
	case shellport.TerminalFrameMsg:
		kind = "terminal-frame"
	}
	w.loops = append(w.loops, terminalGateLoop{kind, time.Now().UnixNano(), int64(s.Update), int64(s.View), int64(s.Present)})
}
func (w *terminalGateWriter) ObserveFlush(s tea.FlushSample) {
	w.loop.ObserveFlush(s)
	w.Lock()
	defer w.Unlock()
	if w.active && w.colors >= 100 && w.hash != w.prior {
		w.content = append(w.content, time.Now().UnixNano())
		w.prior = w.hash
	}
	w.colors, w.hash = 0, 14695981039346656037
}

func TestTerminalGateWriterExcludesIdleAndDuplicateOutput(t *testing.T) {
	w := &terminalGateWriter{output: io.Discard, active: true, loop: perf.NewLoopObserver(), hash: 14695981039346656037}
	flush := func(body []byte) {
		t.Helper()
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
		w.ObserveFlush(tea.FlushSample{Bytes: len(body)})
	}
	flush([]byte("cursor blink status"))
	flush(bytes.Repeat([]byte(";2;"), 99))
	if len(w.content) != 0 {
		t.Fatal("idle/minor output counted as a content frame")
	}
	body := bytes.Repeat([]byte(";2;"), 100)
	flush(body)
	flush(body)
	if len(w.content) != 1 {
		t.Fatal("identical output counted twice, or substantial content omitted")
	}
	flush(append(body, 'x'))
	if len(w.content) != 2 {
		t.Fatal("new substantial output not counted")
	}
}

type terminalGateModel struct {
	*App
	ready        chan workspace.TerminalInfo
	id           string
	once         bool
	terminalRect [4]int
}

func (*terminalGateModel) Init() tea.Cmd {
	width := 220
	if os.Getenv("CRUDE_TERMINAL_GATE_NATIVE_OUTPUT") == "1" {
		width = 208
	}
	return tea.Batch(tickCmd(), func() tea.Msg { return tea.WindowSizeMsg{Width: width, Height: 65} })
}

func (m *terminalGateModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := m.App.Update(msg)
	if _, ok := msg.(tea.WindowSizeMsg); ok && !m.once {
		m.once = true
		m.Tree.Visible = false
		m.termDockHeight = 50 + 1 + ui.StripHeight
		cmd = tea.Batch(cmd, m.openNewTerminal())
	}
	if created, ok := msg.(terminalCreatedMsg); ok && created.err == nil {
		m.id = created.info.ID
	}
	if frame, ok := msg.(shellport.TerminalFrameMsg); ok && frame.ID == m.id && frame.Err == nil {
		// Fit the actual terminal body, not assumed chrome dimensions. Layout
		// remains the production owner and sends the acknowledged PTY resize.
		if screen := m.termScreens[m.id]; screen != nil && screen.Bounds().W > 0 && screen.Bounds().W != 200 {
			width := m.width + 200 - screen.Bounds().W
			return m, tea.Batch(cmd, func() tea.Msg {
				return tea.WindowSizeMsg{Width: width, Height: 65}
			})
		}
		p := frame.Projection
		if p.Grid.Cols == 200 && p.Grid.GridRows == 50 && p.Info.Alive && p.Generation > 0 {
			b, ok := m.rootHost.ScreenRect(m.termScreens[m.id].ID())
			if !ok {
				return m, cmd
			}
			m.terminalRect = [4]int{b.X, b.Y, b.W, b.H}
			select {
			case m.ready <- p.Info:
			default:
			}
		}
	}
	return m, cmd
}

func terminalGateCPU() (gc, active float64) {
	s := []metrics.Sample{{Name: "/cpu/classes/gc/total:cpu-seconds"}, {Name: "/cpu/classes/total:cpu-seconds"}, {Name: "/cpu/classes/idle:cpu-seconds"}}
	metrics.Read(s)
	return s[0].Value.Float64(), s[1].Value.Float64() - s[2].Value.Float64()
}

func TestTerminalNativeGate(t *testing.T) {
	if os.Getenv("CRUDE_TERMINAL_GATE") != "1" {
		t.Skip("opt in through scripts/terminal_native_gate.py")
	}
	dir, resultPath := os.Getenv("CRUDE_TERMINAL_GATE_DIR"), os.Getenv("CRUDE_TERMINAL_GATE_RESULT")
	if dir == "" || resultPath == "" {
		t.Fatal("gate requires private socket directory and result path")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"-test.run=^TestTerminalNativeGateCoreHelper$", "-test.timeout=45s"}
	if profile := os.Getenv("CRUDE_TERMINAL_GATE_CORE_PROFILE"); profile != "" {
		args = append(args, "-test.cpuprofile="+profile+".cpu", "-test.mutexprofile="+profile+".mutex", "-test.blockprofile="+profile+".block")
	}
	command := exe
	if trace := os.Getenv("CRUDE_TERMINAL_GATE_TRACE"); trace != "" {
		command = "strace"
		// Filter in-kernel before ptrace stops: without seccomp, strace also
		// stops every read/futex despite printing only writes, perturbing the
		// very goroutine scheduling this gate measures. The successful PTY
		// write observations and strict latency ceiling remain identical.
		args = append([]string{"--seccomp-bpf", "-ff", "-ttt", "-y", "-s", "64", "-e", "trace=write,writev", "-o", trace, exe}, args...)
	}
	child := exec.Command(command, args...)
	child.Env = append(os.Environ(), "CRUDE_TERMINAL_GATE_CORE=1")
	stdin, err := child.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = os.Stderr
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		if err := child.Wait(); err != nil {
			t.Error("external core shutdown:", err)
		}
	})
	ready := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(stdout)
		line, err := reader.ReadString('\n')
		if err == nil && line != "TERMINAL_GATE_CORE_READY\n" {
			err = fmt.Errorf("unexpected core greeting %q", line)
		}
		ready <- err
		_, _ = io.Copy(io.Discard, reader)
	}()
	select {
	case err := <-ready:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("external core did not start")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	port, err := remote.Attach(ctx, remote.Options{Root: dir, Dial: func(ctx context.Context, id string) (*coreclient.Client, error) {
		return coreclient.Dial(ctx, func(ctx context.Context) (corewire.Conn, error) {
			conn, err := corewire.DialLocal(ctx, dir)
			if err != nil {
				return nil, err
			}
			return corewire.NewMux(conn, corewire.CarrierLocal, true), nil
		}, id)
	}})
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(port, nil)
	app.SetPromptHistory(history.New())
	t.Cleanup(func() {
		app.StopAsyncIO()
		if err := app.CloseWorkspaceAttachment(); err != nil {
			t.Error(err)
		}
	})
	model := &terminalGateModel{App: app, ready: make(chan workspace.TerminalInfo, 1)}
	output := io.Writer(io.Discard)
	var programOptions []tea.ProgramOption
	native := os.Getenv("CRUDE_TERMINAL_GATE_NATIVE_OUTPUT") == "1"
	if native {
		output = os.Stdout
	}
	writer := &terminalGateWriter{output: output, loop: perf.NewLoopObserver(), hash: 14695981039346656037}
	app.SetDisplayCadence(writer.loop)
	if native {
		programOptions = append(programOptions, tea.WithInput(os.Stdin), tea.WithOutput(&terminalGateTTYWriter{terminalGateWriter: writer, file: os.Stdout}))
	} else {
		programOptions = append(programOptions, tea.WithInput(nil), tea.WithOutput(writer))
	}
	programOptions = append(programOptions, tea.WithObserver(writer), tea.WithColorProfile(colorprofile.TrueColor), tea.WithWidthMethod(ansi.GraphemeWidth), tea.WithEnvironment([]string{"TERM=xterm-256color", "COLORTERM=truecolor"}))
	program := tea.NewProgram(model, programOptions...)
	app.SetProgram(program)
	result := terminalGateResult{Schema: 1, Cohort: buildinfo.ProtocolBuild, Workload: os.Getenv("CRUDE_TERMINAL_GATE_WORKLOAD"), ShellPID: os.Getpid(), Cols: 200, Rows: 50,
		Boundary: "content-bearing distinct renderer writer flushes; NOT outer-emulator displayed fps; key origin is immediately before Program.Send"}
	done := make(chan error, 1)
	go func() {
		var info workspace.TerminalInfo
		select {
		case info = <-model.ready:
		case <-time.After(15 * time.Second):
			done <- fmt.Errorf("no acknowledged live 200x50 terminal")
			program.Quit()
			return
		}
		time.Sleep(time.Second) // Excluded warm-up, not a retry of a failed gate.
		result.ChildPID = info.PID
		gc0, active0 := terminalGateCPU()
		writer.Lock()
		writer.active = true
		result.StartUnixNS = time.Now().UnixNano()
		writer.Unlock()
		seconds := 8
		if value := os.Getenv("CRUDE_TERMINAL_GATE_SECONDS"); value != "" {
			if n, err := strconv.Atoi(value); err == nil && n >= 6 && n <= 30 {
				seconds = n
			}
		}
		end := time.NewTimer(time.Duration(seconds) * time.Second)
		ticker := time.NewTicker(50 * time.Millisecond)
		defer end.Stop()
		defer ticker.Stop()
	measure:
		for {
			select {
			case <-ticker.C:
				if len(result.Keys) < 100 {
					result.Keys = append(result.Keys, terminalGateKey{EnqueueUnixNS: time.Now().UnixNano(), Text: "j"})
					program.Send(tea.KeyPressMsg{Code: 'j', Text: "j"})
				}
			case <-end.C:
				break measure
			}
		}
		writer.Lock()
		result.EndUnixNS = time.Now().UnixNano()
		writer.active = false
		result.ContentFlushUnixNS = append([]int64(nil), writer.content...)
		result.OutputBytes = writer.bytes
		writer.Unlock()
		gc1, active1 := terminalGateCPU()
		result.ShellGCCPUSeconds, result.ShellActiveCPUSeconds = gc1-gc0, active1-active0
		if result.ShellActiveCPUSeconds > 0 {
			result.ShellGCCPUPercent = 100 * result.ShellGCCPUSeconds / result.ShellActiveCPUSeconds
		}
		program.Quit()
		done <- nil
	}()
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	result.Loops = append([]terminalGateLoop(nil), writer.loops...)
	result.TerminalRect = model.terminalRect
	body, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resultPath, append(body, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if len(result.Keys) != 100 || len(result.ContentFlushUnixNS) == 0 || result.ShellActiveCPUSeconds <= 0 {
		t.Fatal("incomplete terminal gate evidence; inspect result JSON")
	}
}
