package viewmodel

import (
	"context"
	"crude/internal/fileuri"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"crude/shellport"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

// Native captures: prebuild the viewmodel test binary, select
// CRUDE_VISUAL_DIAGNOSTIC_DETAILS=1,
// CRUDE_ARGS='-test.run=^TestDiagnosticDetailsVisualAcceptance$ -test.count=1 -test.timeout=600s',
// and CRUDE_DIAGNOSTIC_DETAILS_VISUAL_RESULT=/absolute/evidence.txt.
// Run scripts/tuishot.sh sequentially at COLS=160 ROWS=40 and COLS=80 ROWS=24.
//
// Ordinary external driver (wait for each F3 SETUP/F4 VERIFIED/PAGE marker):
// key:F3 sleep:3
// key:ctrl+End key:Up key:End key:Left key:Left key:Left key:Left key:F4
// click:SOURCE_X,SOURCE_Y,3 key:F4
// key:Down x MENU_DOWN (currently 8), key:F4
// key:Return key:F4
// Repeat [key:Next key:F4] 64 times. F4 records paging; it does not advance this
// phase until selection. It must observe BOTH the original final-field token
// and original message tail in actual published cells, not merely Value().
// key:ctrl+Home key:shift+Right key:F4
// type:Z key:F4
// key:Escape key:F4
// key:Escape key:F4
// move:SOURCE_X,SOURCE_Y key:F4
// wheel:HOVER_X,HOVER_Y,1 key:F4
// move:AWAY_X,AWAY_Y key:F4 quit:F9
//
// Source/hover/away cells and MENU_DOWN are recorded at the preceding observer
// boundaries. Headless logs also print DIAGNOSTIC_NATIVE_DRIVER for that size.
// F3 alone may open the fixture. F4 only observes/copies evidence; it never
// opens a reader, moves a caret, requests LSP work, edits source or repairs UI.
// Providers, trace/disk/canonical reads and result writes run outside Update.
func TestDiagnosticDetailsVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_DIAGNOSTIC_DETAILS") != "1" {
		t.Skip("dedicated-terminal installed-gopls diagnostic reader acceptance")
	}
	runDiagnosticDetailsVisual(t, false, 0, 0)
}

func TestDiagnosticDetailsVisualHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		if !t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			runDiagnosticDetailsVisual(t, true, size[0], size[1])
		}) {
			return
		}
	}
}

const diagnosticVisualSource = "package journey\n\nfunc trigger() { consume(1) }\n"
const diagnosticVisualDraft = "Private diagnostic reader draft stays unsent."
const diagnosticVisualLastField = "DiagnosticOriginalTail095"
const diagnosticVisualMessageTail = "in argument to consume"

// This creates SOURCE, not diagnostic content. The only message accepted below
// comes from the installed provider's real publishDiagnostics notification.
func diagnosticVisualLibrary() string {
	var source strings.Builder
	source.WriteString("package journey\n\nfunc consume(value struct {\n")
	for i := 0; i < 96; i++ {
		name := fmt.Sprintf("DiagnosticOriginalField%03d", i)
		if i == 95 {
			name = diagnosticVisualLastField
		}
		fmt.Fprintf(&source, "\t%s string\n", name)
	}
	source.WriteString("}) {}\n")
	return source.String()
}

type diagnosticVisualDone struct {
	phase   int
	advance bool
	err     error
}

type diagnosticDetailsVisualModel struct {
	*featureVisualModel
	retained   *workspace.Workspace
	phase      int
	boundaries chan diagnosticVisualDone
	executable string
	tracePath  string
	pids       []int
	transcript strings.Builder
	initial    types.InsightSource
	facts      types.InsightDiagnostics
	settings   types.LSPSettings
	reader     *ui.TextArea
	body       string
	selected   ui.TextAreaState
	point      [2]int
	away       [2]int
	hoverPoint [2]int
	menuDown   int
	pageScroll int
	pageCount  int
	sawField   bool
	sawTail    bool
	hoverStart int
}

func runDiagnosticDetailsVisual(t *testing.T, headless bool, width, height int) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("nonvacuous exact-provider join oracle requires Linux /proc")
	}
	gopls := os.Getenv("CRUDE_LSP_VISUAL_GOPLS")
	if gopls == "" {
		gopls, _ = exec.LookPath("gopls")
		if gopls == "" {
			gopls = "/home/lenovo/go/bin/gopls"
		}
	}
	executable, err := filepath.EvalSymlinks(gopls)
	if err != nil {
		t.Fatalf("installed real gopls required: %v", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		t.Fatal(err)
	}
	version, err := exec.Command(executable, "version").CombinedOutput()
	if err != nil || !strings.Contains(string(version), "golang.org/x/tools/gopls") {
		t.Fatalf("installed gopls version: %s %v", version, err)
	}
	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CRUDE_HOME", home)
	sources := map[string]string{
		"go.mod": "module journey\n\ngo 1.22\n", "source.go": diagnosticVisualSource,
		"lib.go": diagnosticVisualLibrary(), "unrelated.txt": "Untouched diagnostic fixture.\n",
	}
	for name, text := range sources {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	tracePath := filepath.Join(home, "gopls-diagnostic-details-rpc.log")
	var retained *workspace.Workspace
	coreRuntime := remoteJourneyRuntime(t, "http://127.0.0.1:1", func(w *workspace.Workspace) {
		retained = w
		settings := w.AppDefaults()
		settings.LSP = types.DefaultLSPSettings()
		settings.LSP.AutoDetect, settings.LSP.AutoComplete = false, false
		settings.LSP.Languages = map[string]types.LSPLanguageSettings{"go": {
			Enabled: true, Command: executable, Args: []string{"serve", "-rpc.trace", "-logfile=" + tracePath},
			RootMarkers: []string{"go.mod"},
			Environment: map[string]string{
				"HOME": home, "GOROOT": strings.TrimSpace(string(goroot)),
				"GOPATH": filepath.Join(home, "go"), "GOCACHE": filepath.Join(home, "cache"),
				"GOENV": "off", "GOPROXY": "off", "GOSUMDB": "off", "GOTOOLCHAIN": "local",
			},
		}}
		w.SetAppDefaults(settings)
		grantSemanticFixture(t, w)
	})
	shell := newRemoteJourneyShell(t, coreRuntime, root)
	remoteJourneyCreate(t, shell)
	ctx, cancel := context.WithCancel(t.Context())
	m := &diagnosticDetailsVisualModel{
		featureVisualModel: &featureVisualModel{
			controlVisualModel: &controlVisualModel{
				App: shell.app, runtime: coreRuntime, root: root, path: filepath.Join(root, "source.go"),
				ctx: ctx, completed: map[int]bool{},
			},
			resultPath: os.Getenv("CRUDE_DIAGNOSTIC_DETAILS_VISUAL_RESULT"),
		},
		retained: retained, executable: executable, tracePath: tracePath,
		boundaries: make(chan diagnosticVisualDone, 1), pageScroll: -1,
	}
	var workers sync.WaitGroup
	defer func() {
		// Retain a nonvacuous process observation even when an early UI gate fails.
		if len(m.pids) == 0 {
			m.pids, _ = lspVisualProcesses(root, executable)
		}
		if m.client != nil {
			if err := m.client.Close(); err != nil {
				t.Error(err)
			}
		}
		cancel()
		workers.Wait()
		m.StopAsyncIO()
		if err := m.CloseWorkspaceAttachment(); err != nil {
			t.Error(err)
		}
		if err := coreRuntime.Close(); err != nil {
			t.Error(err)
		}
		remaining, processErr := lspVisualProcesses(root, executable)
		joined := processErr == nil && len(m.pids) > 0 && len(remaining) == 0
		if !joined {
			t.Errorf("diagnostic provider join unproven: started=%v remaining=%v err=%v", m.pids, remaining, processErr)
		}
		for name, want := range sources {
			got, err := os.ReadFile(filepath.Join(root, name))
			if err != nil || string(got) != want {
				t.Errorf("read-only journey changed disk %s: %q err=%v", name, got, err)
			}
		}
		if err := m.recordDiagnostic(fmt.Sprintf("DIAGNOSTIC_PROVIDER_JOIN started=%v remaining=%v joined=%t err=%v; uiPassed=%t headless=%t",
			m.pids, remaining, joined, processErr, !t.Failed(), headless)); err != nil {
			t.Error(err)
		}
		if !t.Failed() {
			if err := m.recordDiagnostic("DIAGNOSTIC_DETAILS_VISUAL_SUCCESS original gopls message; native context/menu/reader/paging/selection/inert typing/Escape; passive pointer hover/wheel; frozen canonical source/caret/draft/disk; partial context; independent joined provider"); err != nil {
				t.Error(err)
			}
		}
		t.Log(m.transcript.String())
	}()
	if err := m.recordDiagnostic(fmt.Sprintf("DIAGNOSTIC_PROVIDER %s executable=%s headless=%t viewport=%dx%d root=%s sourceSHA256=%x librarySHA256=%x trace=%s",
		strings.TrimSpace(string(version)), executable, headless, width, height, root,
		sha256.Sum256([]byte(diagnosticVisualSource)), sha256.Sum256([]byte(sources["lib.go"])), tracePath)); err != nil {
		t.Fatal(err)
	}
	opts := []tea.ProgramOption{tea.WithWidthMethod(ansi.GraphemeWidth)}
	if headless {
		opts = append(opts, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithWindowSize(width, height))
	}
	program := tea.NewProgram(m, opts...)
	m.program = program
	m.SetProgram(program)
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			select {
			case msg := <-shell.fifo:
				program.Send(msg)
			case <-ctx.Done():
				return
			}
		}
	}()
	m.ws.(shellport.ShellControl).StartShellControl()
	if headless {
		workers.Add(1)
		go func() { defer workers.Done(); m.driveDiagnosticDetails(ctx) }()
	}
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if m.failure != nil {
		t.Fatal(m.failure)
	}
	if m.phase != 12 || m.rootHost.HasPointerCapture() || !m.sawField || !m.sawTail {
		t.Fatalf("diagnostic journey incomplete/captured: phase=%d lastField=%t originalTail=%t", m.phase, m.sawField, m.sawTail)
	}
}

func (m *diagnosticDetailsVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case diagnosticVisualDone:
		m.busy = false
		if v.err != nil {
			m.failure = fmt.Errorf("diagnostic boundary %d: %w", v.phase, v.err)
			return m, tea.Quit
		}
		if v.advance {
			m.phase = v.phase + 1
		}
		select {
		case m.boundaries <- v:
		default:
		}
		return m, nil
	case tea.KeyPressMsg:
		if v.Code == tea.KeyF9 && !m.busy {
			return m, tea.Quit
		}
		if v.Code == tea.KeyF3 || v.Code == tea.KeyF4 {
			if m.busy || v.Code == tea.KeyF3 && m.phase != 0 || v.Code == tea.KeyF4 && (m.phase == 0 || m.phase >= 12) {
				return m, nil
			}
			m.busy = true
			phase := m.phase
			return m, func() tea.Msg {
				advance, err := m.observeDiagnosticDetails(phase)
				if err != nil {
					_ = m.recordDiagnostic(fmt.Sprintf("DIAGNOSTIC_DETAILS_VISUAL_BLOCKED phase=%d err=%v", phase, err))
				}
				return diagnosticVisualDone{phase: phase, advance: advance, err: err}
			}
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}

func (m *diagnosticDetailsVisualModel) driveDiagnosticDetails(ctx context.Context) {
	send := func(code rune, mod tea.KeyMod) { m.program.Send(tea.KeyPressMsg{Code: code, Mod: mod}) }
	observe := func(code rune) bool {
		send(code, 0)
		select {
		case <-m.boundaries:
			return true
		case <-ctx.Done():
			return false
		}
	}
	if !observe(tea.KeyF3) {
		return
	}
	send(tea.KeyEnd, tea.ModCtrl)
	send(tea.KeyUp, 0)
	send(tea.KeyEnd, 0)
	for i := 0; i < 4; i++ {
		send(tea.KeyLeft, 0)
	}
	if !observe(tea.KeyF4) {
		return
	}
	m.program.Send(tea.MouseClickMsg{X: m.point[0], Y: m.point[1], Button: tea.MouseRight})
	if !observe(tea.KeyF4) {
		return
	}
	for i := 0; i < m.menuDown; i++ {
		send(tea.KeyDown, 0)
	}
	if !observe(tea.KeyF4) {
		return
	}
	send(tea.KeyEnter, 0)
	if !observe(tea.KeyF4) {
		return
	}
	// Deliberate overshoot exercises clamping too. Every page is observed; tail
	// evidence may occur before trailing provenance, never inferred from bottom.
	for i := 0; i < 64; i++ {
		send(tea.KeyPgDown, 0)
		if !observe(tea.KeyF4) {
			return
		}
	}
	send(tea.KeyHome, tea.ModCtrl)
	send(tea.KeyRight, tea.ModShift)
	if !observe(tea.KeyF4) {
		return
	}
	m.program.Send(tea.KeyPressMsg{Code: 'Z', Text: "Z"})
	if !observe(tea.KeyF4) {
		return
	}
	send(tea.KeyEscape, 0)
	if !observe(tea.KeyF4) {
		return
	}
	send(tea.KeyEscape, 0)
	if !observe(tea.KeyF4) {
		return
	}
	// The native context-menu click leaves the pointer at the source. Escape
	// dismisses the reader; a stationary pointer is not a new hover gesture.
	m.program.Send(tea.MouseMotionMsg{X: m.away[0], Y: m.away[1], Button: tea.MouseNone})
	m.program.Send(tea.MouseMotionMsg{X: m.point[0], Y: m.point[1], Button: tea.MouseNone})
	if !observe(tea.KeyF4) {
		return
	}
	m.program.Send(tea.MouseMotionMsg{X: m.hoverPoint[0], Y: m.hoverPoint[1], Button: tea.MouseNone})
	m.program.Send(tea.MouseWheelMsg{X: m.hoverPoint[0], Y: m.hoverPoint[1], Button: tea.MouseWheelDown})
	if !observe(tea.KeyF4) {
		return
	}
	m.program.Send(tea.MouseMotionMsg{X: m.away[0], Y: m.away[1], Button: tea.MouseNone})
	if observe(tea.KeyF4) {
		send(tea.KeyF9, 0)
	}
}

func (m *diagnosticDetailsVisualModel) recordDiagnostic(line string) error {
	fmt.Fprintln(&m.transcript, line)
	return m.evidence(line)
}

func (m *diagnosticDetailsVisualModel) setupDiagnosticDetails() error {
	if err := m.connect(); err != nil {
		return err
	}
	m.program.Send(tea.KeyPressMsg{Code: 'P', Text: diagnosticVisualDraft})
	if err := m.await(func(v *controlVisualModel) error {
		if v.Input.Value() != diagnosticVisualDraft {
			return fmt.Errorf("ordinary private composer input not retained")
		}
		return nil
	}); err != nil {
		return err
	}
	// Setup-only control, not a semantic feature entry point.
	if _, err := m.execute("file.open", map[string]any{"path": m.path}, nil); err != nil {
		return err
	}
	if _, err := m.execute("tree.set_visible", map[string]any{"visible": false}, nil); err != nil {
		return err
	}
	if err := m.await(func(v *controlVisualModel) error {
		ed := v.EditorState.Editor
		source, ready := ed.CurrentInsightSource()
		facts := ed.DiagnosticFacts()
		if !ready || source.Kind != "canonical" || source.Path != m.path || source.DocumentID == "" ||
			source.Lifetime == "" || source.WorkspaceGeneration == "" || !v.ws.LSPMgr().ServerReady() ||
			ed.Value() != diagnosticVisualSource || ed.Modified() || ed.HasSelection() ||
			ed.DocumentState().PendingEdits != 0 || v.focusTarget() != FocusEditor ||
			!facts.HasExactSource() || facts.Boundary.Source != source || len(facts.Diagnostics) != 1 {
			return fmt.Errorf("canonical source/real provider/exact diagnostic not ready: source=%+v facts=%+v", source, facts)
		}
		d := facts.Diagnostics[0]
		b := facts.Boundary
		owner, generation := v.ws.LSPMgr().ContextCursor()
		if d.Line != 2 || d.Col != 25 || d.EndLine != 2 || d.EndCol != 26 ||
			d.Severity != types.DiagError || d.Source != "compiler" ||
			!strings.HasPrefix(d.Message, "cannot use 1") || len(d.Message) < 2500 ||
			!strings.Contains(d.Message, diagnosticVisualLastField+" string") ||
			!strings.HasSuffix(d.Message, diagnosticVisualMessageTail) ||
			b.Source.WorkspaceGeneration != owner || b.Context.Generation != generation ||
			b.Context.ID == "" || b.Context.Root != m.root || len(b.Context.UnknownInputs) == 0 ||
			b.Producer.Provider != m.executable || b.Producer.InstanceID == "" ||
			b.Producer.ToolIdentity != "launch-config:"+b.Context.ID {
			return fmt.Errorf("original gopls long-message/source/provider/context contract differs: %+v", facts)
		}
		m.initial, m.facts = source, facts
		m.settings = types.CloneLSPSettings(v.ws.LSPMgr().Settings())
		return featureVisualPaint(v, "func trigger()", "consume(1)")
	}); err != nil {
		return err
	}
	pids, err := lspVisualProcesses(m.root, m.executable)
	if err != nil || len(pids) == 0 {
		return fmt.Errorf("real provider ownership missing: pids=%v err=%v", pids, err)
	}
	m.pids = pids
	if err := m.verifyDiagnosticTrace(true); err != nil {
		return err
	}
	raw, err := json.Marshal(m.facts)
	if err != nil {
		return err
	}
	return m.recordDiagnostic(fmt.Sprintf("F3 SETUP pids=%v source=%+v; next ordinary Ctrl+End Up End Left x4; F4 passive\nORIGINAL_FACTS %s\nORIGINAL_MESSAGE %s",
		pids, m.initial, raw, m.facts.Diagnostics[0].Message))
}

// Reader inspection uses public overlay/content contracts, not the private
// diagnostic reader state or any helper which itself constructs the body.
func diagnosticVisualReader(v *controlVisualModel, id ui.OverlayID, frame tea.View) (*ui.TextArea, ui.Rect, string, error) {
	fw := v.overlayHost.Window(id)
	if fw == nil || !v.overlayHost.IsOpen(id) {
		return nil, ui.Rect{}, "", fmt.Errorf("ordinary input has not mounted %s", id)
	}
	widget, ok := fw.Content.(*ui.WindowFrameWidget)
	if !ok {
		return nil, ui.Rect{}, "", fmt.Errorf("%s is not the shared WindowFrameWidget", id)
	}
	area, ok := widget.Body().(*ui.TextArea)
	if !ok || area.TextEditable() {
		return nil, ui.Rect{}, "", fmt.Errorf("%s has no native read-only TextArea body", id)
	}
	local, mounted := widget.Host().ScreenRect(area.ID())
	if !mounted || local.Empty() || frame.Surface == nil {
		return nil, ui.Rect{}, "", fmt.Errorf("%s reader has no mounted body/completed publication", id)
	}
	x, y := fw.Position()
	content := fw.ContentBounds()
	rect := ui.Rect{X: x + content.X + local.X, Y: y + content.Y + local.Y, W: local.W, H: local.H}
	if rect.X < 0 || rect.Y < 0 || rect.X+rect.W > v.width || rect.Y+rect.H > v.height {
		return nil, ui.Rect{}, "", fmt.Errorf("%s body escapes shell: %+v", id, rect)
	}
	// Read the ACTUAL published product grid, cropped to the mounted body.
	// This survives mid-token wraps without accepting offscreen Value() text.
	grid := uv.NewScreenBuffer(v.width, v.height)
	frame.Surface.Draw(grid, grid.Bounds())
	var pixels strings.Builder
	for row := rect.Y; row < rect.Y+rect.H; row++ {
		for col := rect.X; col < rect.X+rect.W; col++ {
			if c := grid.CellAt(col, row); c != nil {
				pixels.WriteString(c.Content)
			}
		}
		pixels.WriteByte('\n')
	}
	return area, rect, pixels.String(), nil
}

func diagnosticVisualLetters(text string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, text)
}

func (m *diagnosticDetailsVisualModel) observeDiagnosticDetails(phase int) (bool, error) {
	if phase == 0 {
		return true, m.setupDiagnosticDetails()
	}
	var paint, pixels, body string
	var scroll int
	var selected bool
	var area *ui.TextArea
	var readerRect ui.Rect
	if err := m.await(func(v *controlVisualModel) error {
		ed := v.EditorState.Editor
		current, ready := ed.CurrentInsightSource()
		ds := ed.DocumentState()
		line, col := ed.LSPCursorPosition()
		if !ready || current != m.initial || ed.Value() != diagnosticVisualSource ||
			ed.Modified() || ed.HasSelection() || line != 2 || col != 25 || !ed.IsOpen() ||
			ds.PendingEdits != 0 || ds.InFlight || ds.Conflict != nil || ds.UndoPending || ds.HistoryQueued ||
			v.Input.Value() != diagnosticVisualDraft || len(v.EditorState.Tabs) != 1 ||
			!reflect.DeepEqual(ed.DiagnosticFacts(), m.facts) ||
			!reflect.DeepEqual(v.ws.LSPMgr().Settings(), m.settings) {
			return fmt.Errorf("read-only source/caret/draft/fact/settings changed: source=%+v caret=%d:%d state=%+v text=%q draft=%q",
				current, line, col, ds, ed.Value(), v.Input.Value())
		}
		frame := v.View()
		paint = ansi.Strip(frame.RenderContent())
		sourceRect, mounted := v.rootHost.ScreenRect(v.editorScreen.ID())
		if !mounted || sourceRect.Empty() {
			return fmt.Errorf("source editor is not mounted at observation %d", phase)
		}
		caretX, caretY := ed.CursorScreenPosition(sourceRect.X, sourceRect.Y)
		if !sourceRect.Contains(caretX, caretY) {
			return fmt.Errorf("source caret outside actual mounted source")
		}
		if phase == 1 {
			m.point = [2]int{caretX, caretY}
			m.away = [2]int{sourceRect.X + 2, sourceRect.Y + 1}
			if v.overlayHost.IsOpen(ui.OverlayDiagContext) || v.focusedPopup() != nil || v.focusTarget() != FocusEditor {
				return fmt.Errorf("caret positioning unexpectedly opened/focused explicit reader")
			}
			return nil
		}
		if m.point != [2]int{caretX, caretY} {
			return fmt.Errorf("reader browsing moved painted source caret: %v -> %d,%d", m.point, caretX, caretY)
		}
		if phase == 2 || phase == 3 {
			menu := v.LastContextMenu
			if menu == nil || !v.overlayHost.IsOpen(ui.OverlayContextMenu) {
				return fmt.Errorf("ordinary right-click has no editor context menu")
			}
			target := -1
			for i := range menu.FlatItems {
				si, ii, ok := menu.FlatItemAt(i)
				if ok && menu.Sections[si].Items[ii].Label == "Show 1 diagnostic(s)" {
					target = i
				}
			}
			if target < 0 {
				return fmt.Errorf("editor menu lost literal Show 1 diagnostic(s) entry")
			}
			if phase == 2 {
				m.menuDown = (target - menu.Cursor + len(menu.FlatItems)) % len(menu.FlatItems)
			} else if menu.Cursor != target || !strings.Contains(paint, "Show 1 diagnostic(s)") {
				return fmt.Errorf("ordinary Down did not select/paint the diagnostic menu row: cursor=%d target=%d", menu.Cursor, target)
			}
			if v.overlayHost.IsOpen(ui.OverlayDiagContext) {
				return fmt.Errorf("menu observation opened diagnostic reader before Enter")
			}
			return nil
		}
		if phase == 8 || phase == 11 {
			if v.overlayHost.IsOpen(ui.OverlayDiagContext) || v.overlayHost.IsOpen(ui.OverlayDiagHover) ||
				v.focusedPopup() != nil || v.focusTarget() != FocusEditor || v.rootHost.HasPointerCapture() ||
				!strings.Contains(paint, "consume(1)") {
				return fmt.Errorf("ordinary Escape/move-away retained reader, lost source, focus or pointer:\n%s", paint)
			}
			return nil
		}
		id := ui.OverlayDiagContext
		passive := phase == 9 || phase == 10
		if passive {
			id = ui.OverlayDiagHover
		}
		var err error
		area, readerRect, pixels, err = diagnosticVisualReader(v, id, frame)
		if err != nil {
			return err
		}
		body, scroll, selected = area.Value(), area.ScrollOffset(), area.HasActiveSelection()
		for _, literal := range []string{m.facts.Diagnostics[0].Message, m.facts.Diagnostics[0].Source, m.facts.ID, m.executable,
			m.facts.Boundary.Context.ID, "func trigger() { consume(1) }"} {
			if !strings.Contains(body, literal) {
				return fmt.Errorf("%s lost full original message/source/provenance %q", id, literal)
			}
		}
		for _, input := range m.facts.Boundary.Context.UnknownInputs {
			if !strings.Contains(body, input) {
				return fmt.Errorf("%s lost context input provenance %q", id, input)
			}
		}
		lower := strings.ToLower(body)
		if !strings.Contains(body, "Results are available for this file. Other files may not be checked.") ||
			strings.Contains(lower, "project clean") || strings.Contains(lower, "project is clean") ||
			strings.Contains(lower, "context: complete") || strings.Contains(lower, "coverage: complete") {
			return fmt.Errorf("partial provider context misrepresented: %q", body)
		}
		fw := v.overlayHost.Window(id)
		x, y := fw.Position()
		w, h := fw.Size()
		if x < 0 || y < 0 || x+w > v.width || y+h > v.height || x > caretX || x+w <= caretX ||
			(y != caretY+1 && y+h != caretY && y+h+1 != caretY) {
			return fmt.Errorf("reader not anchored to first mounted source caret: caret=%d,%d window=%d,%d %dx%d",
				caretX, caretY, x, y, w, h)
		}
		if passive {
			if v.focusedPopup() != nil || v.focusTarget() != FocusEditor || selected {
				return fmt.Errorf("ordinary pointer hover/wheel stole editor focus/selection")
			}
			if phase == 9 {
				m.hoverPoint = [2]int{readerRect.X + 2, readerRect.Y + min(1, readerRect.H-1)}
				m.hoverStart = scroll
			} else if scroll <= m.hoverStart {
				return fmt.Errorf("ordinary wheel did not scroll passive mounted diagnostic body")
			}
		} else {
			if v.focusedPopup() != fw || v.overlayHost.IsOpen(ui.OverlayContextMenu) {
				return fmt.Errorf("ordinary Enter has not focused only the native diagnostic reader")
			}
			if phase == 4 {
				if scroll != 0 || selected || strings.Contains(diagnosticVisualLetters(pixels), diagnosticVisualLastField) {
					return fmt.Errorf("first native reader is not top-of-original-content/long enough")
				}
				m.reader, m.body = area, body
			} else if area != m.reader || body != m.body {
				return fmt.Errorf("browsing replaced or mutated original reader text")
			}
			if phase == 5 && selected {
				if !m.sawField || !m.sawTail || area.SelectedText() == "" || v.rootHost.HasPointerCapture() {
					return fmt.Errorf("Shift+Right selected before original provider tail was paged or captured pointer")
				}
				m.selected = area.EditState()
			}
			if phase == 6 && (!selected || area.EditState() != m.selected) {
				return fmt.Errorf("inert typing changed read-only body/caret/selection/scroll: before=%+v after=%+v", m.selected, area.EditState())
			}
			if phase == 7 && (selected || area.CursorFlat() != m.selected.Cursor || scroll != m.selected.Scroll) {
				return fmt.Errorf("first Escape did not clear only the original reader selection: before=%+v after=%+v", m.selected, area.EditState())
			}
		}
		return nil
	}); err != nil {
		return false, err
	}
	// These authoritative document/disk/provider reads are commands, never App
	// checks on the Update lane. Equal editor text alone would not prove safety.
	snapshot, err := m.retained.LiveDocuments.Snapshot(m.initial.DocumentID)
	if err != nil || snapshot.Revision != m.initial.Revision || string(snapshot.Text) != diagnosticVisualSource {
		return false, fmt.Errorf("retained canonical document changed: snapshot=%+v err=%v", snapshot, err)
	}
	disk, err := os.ReadFile(m.path)
	if err != nil || string(disk) != diagnosticVisualSource {
		return false, fmt.Errorf("disk source changed: %q err=%v", disk, err)
	}
	if err := m.verifyDiagnosticTrace(false); err != nil {
		return false, err
	}
	if phase == 5 && !selected {
		m.pageCount++
		literalPixels := diagnosticVisualLetters(pixels)
		m.sawField = m.sawField || strings.Contains(literalPixels, diagnosticVisualLastField)
		m.sawTail = m.sawTail || strings.Contains(literalPixels, diagnosticVisualLetters(diagnosticVisualMessageTail))
		if scroll == 0 {
			return false, fmt.Errorf("PageDown did not leave original first page")
		}
		if scroll != m.pageScroll {
			m.pageScroll = scroll
			if err := m.recordDiagnostic(fmt.Sprintf("F4 PAGE observation=%d scroll=%d originalLastFieldPainted=%t originalTailPainted=%t sourceSHA256=%x\nPUBLISHED_BODY_CELLS\n%s\nFRAME\n%s",
				m.pageCount, scroll, m.sawField, m.sawTail, sha256.Sum256(disk), pixels, paint)); err != nil {
				return false, err
			}
		} else if err := m.recordDiagnostic(fmt.Sprintf("F4 PAGE observation=%d scroll=%d clamped=true originalLastFieldPainted=%t originalTailPainted=%t sourceUnchanged=true",
			m.pageCount, scroll, m.sawField, m.sawTail)); err != nil {
			return false, err
		}
		return false, nil // Only an ordinary selection completes the paging phase.
	}
	if phase == 11 {
		if err := m.recordDiagnostic(fmt.Sprintf("DIAGNOSTIC_NATIVE_DRIVER viewport=%dx%d SOURCE=%d,%d AWAY=%d,%d HOVER=%d,%d MENU_DOWN=%d PAGE_OBSERVATIONS=%d originalLastFieldPainted=%t originalTailPainted=%t",
			m.width, m.height, m.point[0], m.point[1], m.away[0], m.away[1], m.hoverPoint[0], m.hoverPoint[1],
			m.menuDown, m.pageCount, m.sawField, m.sawTail)); err != nil {
			return false, err
		}
	}
	return true, m.recordDiagnostic(fmt.Sprintf("F4 VERIFIED phase=%d source=%+v canonicalSHA256=%x batch=%s sourceCaret=2:25 SOURCE=%d,%d AWAY=%d,%d HOVER=%d,%d MENU_DOWN=%d scroll=%d selected=%t bodyRect=%+v draftUnchanged=true\nBODY %s\nPUBLISHED_BODY_CELLS\n%s\nFRAME\n%s",
		phase, m.initial, sha256.Sum256(disk), m.facts.ID, m.point[0], m.point[1], m.away[0], m.away[1],
		m.hoverPoint[0], m.hoverPoint[1], m.menuDown, scroll, selected, readerRect, body, pixels, paint))
}

func (m *diagnosticDetailsVisualModel) verifyDiagnosticTrace(record bool) error {
	f, err := os.Open(m.tracePath)
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	closeErr := f.Close()
	if readErr != nil || closeErr != nil || len(data) > 1<<20 {
		return fmt.Errorf("bounded original trace read: read=%v close=%v bytes=%d", readErr, closeErr, len(data))
	}
	trace := string(data)
	opens, err := automaticSignatureTraceEvents(trace, "notification 'textDocument/didOpen", "Params: ")
	if err != nil {
		return err
	}
	didOpen := false
	for _, event := range opens {
		var params struct {
			TextDocument struct {
				URI, Text string
				Version   int
			} `json:"textDocument"`
		}
		if err := json.Unmarshal(event.raw, &params); err != nil {
			return err
		}
		if params.TextDocument.URI == fileuri.FromPath(m.path) && params.TextDocument.Text == diagnosticVisualSource &&
			params.TextDocument.Version == m.facts.WireVersion {
			didOpen = true
		}
	}
	if !didOpen {
		return fmt.Errorf("original didOpen not tied to literal canonical source/wire version")
	}
	changes, err := automaticSignatureTraceEvents(trace, "notification 'textDocument/didChange", "Params: ")
	if err != nil {
		return err
	}
	if len(changes) != 0 {
		return fmt.Errorf("read-only interaction emitted %d original didChange notifications", len(changes))
	}
	publications, err := automaticSignatureTraceEvents(trace, "notification 'textDocument/publishDiagnostics", "Params: ")
	if err != nil {
		return err
	}
	want := m.facts.Diagnostics[0]
	for _, event := range publications {
		var params struct {
			URI         string `json:"uri"`
			Version     *int   `json:"version"`
			Diagnostics []struct {
				Range struct {
					Start, End struct{ Line, Character int }
				} `json:"range"`
				Severity        int `json:"severity"`
				Message, Source string
			} `json:"diagnostics"`
		}
		if err := json.Unmarshal(event.raw, &params); err != nil {
			return err
		}
		if params.URI != fileuri.FromPath(m.path) || params.Version == nil ||
			m.facts.ReportedVersion == nil || *params.Version != *m.facts.ReportedVersion {
			continue
		}
		for _, d := range params.Diagnostics {
			if d.Message != want.Message || d.Source != want.Source || d.Severity != int(want.Severity) ||
				d.Range.Start.Line != want.Line || d.Range.Start.Character != want.Col ||
				d.Range.End.Line != want.EndLine || d.Range.End.Character != want.EndCol {
				continue
			}
			if !record {
				return nil
			}
			facts, err := json.Marshal(m.facts)
			if err != nil {
				return err
			}
			return m.recordDiagnostic(fmt.Sprintf("GOPLS_ORIGINAL_PUBLISH_DIAGNOSTICS traceByte=%d rawSHA256=%x batch=%s wireVersion=%d providerInstance=%s\nPARAMS %s\nFACTS %s",
				event.offset, sha256.Sum256(event.raw), m.facts.ID, m.facts.WireVersion,
				m.facts.Boundary.Producer.InstanceID, event.raw, facts))
		}
	}
	return fmt.Errorf("original gopls publishDiagnostics identity/message/range/version missing for batch %s", m.facts.ID)
}
