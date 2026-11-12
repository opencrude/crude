package viewmodel

import (
	"context"
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
	"github.com/charmbracelet/x/ansi"

	"crude/shellport"
	"crude/tui/rendering"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

// Native capture is owned by the sequential scripts/tuishot.sh driver:
// CRUDE_VISUAL_INLAY=1 CRUDE_ARGS=-test.run=^TestInlayVisualAcceptance$
// CRUDE_INLAY_VISUAL_RESULT receives read-only observations and exact click cells.
//
// Driver: F3; Alt+j F4; Enter F4; [if RESOLVE_SUPPORTED=true:
// Left Left Enter] F4; Escape F4; Escape F4;
// Ctrl+Alt+p, type "Hide Inlay Hints", Enter F4;
// Ctrl+Alt+p, type "Show Inlay Hints", Enter F4;
// Ctrl+Alt+p, type "Refresh Inlay Hints", Enter F4;
// Ctrl+Home, Down x5, End, Left x11, F4;
// click/release the printed POINTER_COUNT x/y cell, F4; F9.
// F3 alone sets up/opens the fixture. F4 never invokes a feature action.
func TestInlayVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_INLAY") != "1" {
		t.Skip("dedicated terminal real-provider inlay acceptance only")
	}
	runInlayVisual(t, false, 0, 0)
}

func TestInlayVisualHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		if !t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			runInlayVisual(t, true, size[0], size[1])
		}) {
			return // Preserve the first unchanged real-provider blocker.
		}
	}
}

const inlayVisualDraft = "Private inlay draft stays unsent."
const inlayVisualSource = "package journey\n\nfunc join(first string, count int) string { return first }\n\nfunc Call() string {\n\tanswer := join(\"hello\", 2)\n\treturn answer\n}\n"
const inlayVisualMod = "module journey\n\ngo 1.22\n"
const inlayVisualPainted = `answer string := join(first: "hello", count: 2)`

// These are independent literal source coordinates, not product conversions.
// source.go line 5: TAB=0, answer=[1,7), join=[11,15),
// opening quote=16, integer=25. Every character here is one UTF-16 code unit.
var inlayVisualExpected = []struct {
	label       string
	line, col   int
	kind        int
	left, right bool
}{
	{"string", 5, 7, 1, true, false},
	{"first:", 5, 16, 2, false, true},
	{"count:", 5, 25, 2, false, true},
}

type inlayVisualModel struct {
	*featureVisualModel
	retained       *workspace.Workspace
	phase          int
	executable     string
	tracePath      string
	pids           []int
	transcript     strings.Builder
	source         types.InsightSource
	revision       uint64
	settings       types.LSPSettings
	trust          types.WorkspaceAnalysisSettings
	firstSet       types.LSPInlaySet
	shownSet       types.LSPInlaySet
	inspection     types.LSPInlayInspection
	inspectionErr  error
	selectedReader *inlayInspectorState
	readerSequence uint64
	resolve        chan bool
	click          chan [2]int
}

type inlayVisualDone struct {
	phase int
	err   error
}

func runInlayVisual(t *testing.T, headless bool, width, height int) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("exact fixture provider process oracle uses Linux /proc")
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
	version, err := exec.Command(executable, "version").CombinedOutput()
	if err != nil || !strings.Contains(string(version), "v0.21.1") {
		t.Fatalf("fixture requires installed gopls v0.21.1: %s %v", version, err)
	}
	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	if types.DefaultLSPSettings().InlayHints {
		t.Fatal("new profiles must not opt into inlay hints by default")
	}
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CRUDE_HOME", home)
	for name, text := range map[string]string{"go.mod": inlayVisualMod, "source.go": inlayVisualSource} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	tracePath := filepath.Join(home, "gopls-inlay-rpc.log")
	var retained *workspace.Workspace
	coreRuntime := remoteJourneyRuntime(t, "http://127.0.0.1:1", func(w *workspace.Workspace) {
		retained = w
		settings := w.AppDefaults()
		settings.LSP = types.DefaultLSPSettings()
		settings.LSP.AutoDetect, settings.LSP.AutoComplete = false, false
		settings.LSP.InlayHints = true // Explicit fixture opt-in, not a product default.
		language := types.LSPLanguageSettings{
			Enabled: true, Command: executable, Args: []string{"serve", "-rpc.trace", "-logfile=" + tracePath},
			RootMarkers: []string{"go.mod"},
			Environment: map[string]string{
				"HOME": home, "GOROOT": strings.TrimSpace(string(goroot)), "GOPATH": filepath.Join(home, "go"),
				"GOCACHE": filepath.Join(home, "cache"), "GOENV": "off", "GOPROXY": "off",
				"GOSUMDB": "off", "GOTOOLCHAIN": "local",
			},
		}
		// Generic per-language Settings, with no gopls branch in product code.
		if err := json.Unmarshal([]byte(`{"gopls":{"hints":{"parameterNames":true,"assignVariableTypes":true}}}`), &language.Settings); err != nil {
			t.Fatal(err)
		}
		settings.LSP.Languages = map[string]types.LSPLanguageSettings{"go": language}
		w.SetAppDefaults(settings)
		grantSemanticFixture(t, w)
	})
	shell := newRemoteJourneyShell(t, coreRuntime, root)
	remoteJourneyCreate(t, shell) // Retained session/composer through normal receipt.
	m := &inlayVisualModel{
		featureVisualModel: &featureVisualModel{
			controlVisualModel: &controlVisualModel{App: shell.app, runtime: coreRuntime, root: root,
				path: filepath.Join(root, "source.go"), ctx: t.Context(), completed: map[int]bool{}},
			settled: make(chan struct{}, 1), resultPath: os.Getenv("CRUDE_INLAY_VISUAL_RESULT"),
		},
		retained: retained, executable: executable, tracePath: tracePath,
		resolve: make(chan bool, 1), click: make(chan [2]int, 1),
	}
	defer func() {
		if len(m.pids) == 0 {
			// An earlier oracle failure must not erase exact-process ownership
			// evidence before the same core-owned shutdown path joins it.
			if observed, err := lspVisualProcesses(root, executable); err == nil {
				m.pids = observed
			}
		}
		if m.client != nil {
			if err := m.client.Close(); err != nil {
				t.Error(err)
			}
		}
		m.StopAsyncIO()
		if err := m.CloseWorkspaceAttachment(); err != nil {
			t.Error(err)
		}
		if err := coreRuntime.Close(); err != nil {
			t.Error(err)
		}
		remaining, processErr := lspVisualProcesses(root, executable)
		joined := processErr == nil && len(remaining) == 0 && len(m.pids) != 0
		if !joined {
			t.Errorf("inlay provider ownership unproven: started=%v remaining=%v err=%v", m.pids, remaining, processErr)
		}
		if err := m.recordInlay(fmt.Sprintf("INLAY_PROVIDER_JOIN started=%v remaining=%v joined=%t err=%v",
			m.pids, remaining, joined, processErr)); err != nil {
			t.Error(err)
		}
		if t.Failed() {
			data, readErr := os.ReadFile(tracePath)
			if readErr == nil {
				if len(data) > 24<<10 {
					data = data[len(data)-(24<<10):]
				}
				t.Logf("private fixture RPC tail:\n%s", data)
			}
		} else if err := m.recordInlay("INLAY_VISUAL_SUCCESS literal real-gopls labels; ordinary feature inputs; unchanged canonical bytes/revision/private draft/Trust/master; joined local framed core only"); err != nil {
			t.Error(err)
		}
		t.Log(m.transcript.String())
	}()
	if err := m.recordInlay(fmt.Sprintf("INLAY_PROVIDER %s executable=%s viewport=%dx%d fixture=%s source=%s sourceSHA256=%x trace=%s",
		strings.TrimSpace(string(version)), executable, width, height, root, m.path, sha256.Sum256([]byte(inlayVisualSource)), tracePath)); err != nil {
		t.Fatal(err)
	}
	opts := []tea.ProgramOption{tea.WithWidthMethod(ansi.GraphemeWidth)}
	if headless {
		opts = append(opts, tea.WithInput(nil), tea.WithOutput(io.Discard), tea.WithoutRenderer(), tea.WithWindowSize(width, height))
	}
	program := tea.NewProgram(m, opts...)
	m.program = program
	m.SetProgram(program)
	pumpCtx, stopPump := context.WithCancel(t.Context())
	var workers sync.WaitGroup
	defer func() {
		stopPump()
		workers.Wait()
	}()
	workers.Add(1)
	go func() {
		defer workers.Done()
		for {
			select {
			case msg := <-shell.fifo:
				program.Send(msg)
			case <-pumpCtx.Done():
				return
			}
		}
	}()
	m.ws.(shellport.ShellControl).StartShellControl()
	if headless {
		workers.Add(1)
		go func() {
			defer workers.Done()
			m.driveInlayVisual(pumpCtx)
		}()
	}
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if m.failure != nil {
		if err := m.recordInlay("INLAY_VISUAL_BLOCKED " + m.failure.Error()); err != nil {
			t.Error(err)
		}
		t.Fatal(m.failure)
	}
	if m.phase != 11 {
		t.Fatalf("inlay observations incomplete: %d/11", m.phase)
	}
}

func (m *inlayVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case inlayInspectionMsg:
		m.inspection, m.inspectionErr = v.inspection, v.err
	case inlayVisualDone:
		m.busy = false
		if v.err != nil {
			m.failure = fmt.Errorf("inlay boundary %d: %w", v.phase, v.err)
			return m, tea.Quit
		}
		m.phase = v.phase + 1
		select {
		case m.settled <- struct{}{}:
		default:
		}
		phase := m.phase
		return m, func() tea.Msg {
			if err := m.evidence(fmt.Sprintf("INLAY_READY phase=%d", phase)); err != nil {
				return inlayVisualDone{phase: -1, err: err}
			}
			return nil
		}
	case tea.KeyPressMsg:
		if v.Code == tea.KeyF9 && !m.busy {
			return m, tea.Quit
		}
		if v.Code == tea.KeyF3 && m.phase == 0 || v.Code == tea.KeyF4 && m.phase > 0 && m.phase < 11 {
			if m.busy {
				return m, nil
			}
			m.busy = true
			phase := m.phase
			return m, func() tea.Msg { return inlayVisualDone{phase, m.observeInlay(phase)} }
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}

func (m *inlayVisualModel) driveInlayVisual(ctx context.Context) {
	key := func(code rune) { m.program.Send(tea.KeyPressMsg{Code: code}) }
	observe := func(code rune) bool {
		key(code)
		select {
		case <-m.settled:
			return true
		case <-ctx.Done():
			return false
		}
	}
	if !observe(tea.KeyF3) {
		return
	}
	m.program.Send(tea.KeyPressMsg{Code: 'j', Mod: tea.ModAlt})
	if !observe(tea.KeyF4) {
		return
	}
	key(tea.KeyEnter)
	if !observe(tea.KeyF4) {
		return
	}
	select {
	case supported := <-m.resolve:
		if supported {
			key(tea.KeyLeft)
			key(tea.KeyLeft)
			key(tea.KeyEnter)
		}
	case <-ctx.Done():
		return
	}
	if !observe(tea.KeyF4) {
		return
	}
	for range 2 {
		key(tea.KeyEsc)
		if !observe(tea.KeyF4) {
			return
		}
	}
	for _, label := range []string{"Hide Inlay Hints", "Show Inlay Hints", "Refresh Inlay Hints"} {
		m.program.Send(tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl | tea.ModAlt})
		for _, r := range label {
			m.program.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
		}
		key(tea.KeyEnter)
		if !observe(tea.KeyF4) {
			return
		}
	}
	m.program.Send(tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModCtrl})
	for range 5 {
		key(tea.KeyDown)
	}
	key(tea.KeyEnd)
	for range 11 {
		key(tea.KeyLeft)
	}
	if !observe(tea.KeyF4) {
		return
	}
	select {
	case pos := <-m.click:
		m.program.Send(tea.MouseClickMsg{X: pos[0], Y: pos[1], Button: tea.MouseLeft})
		m.program.Send(tea.MouseReleaseMsg{X: pos[0], Y: pos[1], Button: tea.MouseLeft})
	case <-ctx.Done():
		return
	}
	if !observe(tea.KeyF4) {
		return
	}
	key(tea.KeyF9)
}

func (m *inlayVisualModel) recordInlay(line string) error {
	fmt.Fprintln(&m.transcript, line)
	return m.evidence(line)
}

func (m *inlayVisualModel) unchangedInlay() error {
	if err := m.check(func(v *controlVisualModel) error {
		ed := v.EditorState.Editor
		source, current := ed.CurrentInsightSource()
		if !current || source != m.source || ed.Revision() != m.revision ||
			ed.Value() != inlayVisualSource || ed.Modified() || ed.HasSelection() ||
			ed.DocumentState().PendingEdits != 0 || ed.FilePath() != m.path ||
			v.Input.Value() != inlayVisualDraft || len(v.EditorState.Tabs) != 1 {
			return fmt.Errorf("read-only hints changed source/revision/draft/open targets: source=%+v revision=%d modified=%t selected=%q pending=%d draft=%q tabs=%d",
				source, ed.Revision(), ed.Modified(), ed.SelectedText(), ed.DocumentState().PendingEdits, v.Input.Value(), len(v.EditorState.Tabs))
		}
		if !reflect.DeepEqual(v.ws.LSPMgr().Settings(), m.settings) ||
			!reflect.DeepEqual(m.retained.LoadAppSettings().Analysis, m.trust) {
			return fmt.Errorf("view-local feature action changed Trust or master/language settings")
		}
		return nil
	}); err != nil {
		return err
	}
	for name, want := range map[string]string{"go.mod": inlayVisualMod, "source.go": inlayVisualSource} {
		got, err := os.ReadFile(filepath.Join(m.root, name))
		if err != nil || string(got) != want {
			return fmt.Errorf("read-only hints changed fixture disk %s: %q err=%v", name, got, err)
		}
	}
	return nil
}

func (m *inlayVisualModel) qualifiedInlay(v *controlVisualModel) (*types.LSPInlaySet, error) {
	view := v.inlayViews[v.EditorState.Editor]
	if view == nil || view.hidden || view.request == nil {
		return nil, fmt.Errorf("automatic visible-source request absent")
	}
	r := view.request
	if r.err != nil {
		return nil, fmt.Errorf("real automatic inlay request: %w", r.err)
	}
	if !r.installed || r.result == nil || !v.EditorState.Editor.HasInlayProjection() {
		return nil, fmt.Errorf("automatic debounced request not installed: started=%t installed=%t", r.started, r.installed)
	}
	set := r.result
	b := set.Boundary
	owner, generation := v.ws.LSPMgr().ContextCursor()
	if set.Answer != types.LSPInlayHints || set.ID == "" || len(set.Digest) != 64 || set.WireVersion <= 0 ||
		set.ViewID != view.id || set.Sequence != r.sequence || set.Range != r.span ||
		set.Range.Start != (types.LSPInlayPosition{}) || set.Range.End != (types.LSPInlayPosition{Line: 8}) ||
		b.Source != m.source || b.Source.Kind != "canonical" || b.Source.DocumentID == "" || b.Source.Lifetime == "" ||
		b.Source.WorkspaceGeneration != owner || b.Context.Generation != generation || b.Context.ID == "" || b.Context.Root != m.root ||
		b.Producer.Provider != m.executable || b.Producer.InstanceID == "" || b.Producer.ToolIdentity != "launch-config:"+b.Context.ID ||
		len(b.Context.UnknownInputs) == 0 {
		return nil, fmt.Errorf("inlay set lost canonical source/request-wire/context/provider qualification: %+v cursor=%s/%d", set, owner, generation)
	}
	if len(set.Hints) != len(inlayVisualExpected) {
		return nil, fmt.Errorf("real provider labels count=%d want=3: %+v", len(set.Hints), set.Hints)
	}
	for _, want := range inlayVisualExpected {
		found := false
		for _, hint := range set.Hints {
			if hint.Label != want.label {
				continue
			}
			if found || hint.Position != (types.LSPInlayPosition{Line: want.line, Character: want.col}) ||
				!hint.HasKind || hint.Kind != want.kind || hint.PaddingLeft != want.left || hint.PaddingRight != want.right {
				return nil, fmt.Errorf("literal provider label/anchor/kind/padding mismatch: %+v want=%+v", hint, want)
			}
			// The literal prefix locates the independent byte start of line 5.
			prefix := "package journey\n\nfunc join(first string, count int) string { return first }\n\nfunc Call() string {\n"
			if hint.SourceByte != len(prefix)+want.col {
				return nil, fmt.Errorf("label %q source byte=%d want=%d", hint.Label, hint.SourceByte, len(prefix)+want.col)
			}
			found = true
		}
		if !found {
			return nil, fmt.Errorf("missing literal real-provider label %q", want.label)
		}
	}
	return set, nil
}

// Independently decode the installed provider's original RPC trace, not lspinlay's
// normalization. The trace logs framed transport responses after decoding; it is
// not a fabricated response and does not claim a standard response source version.
func (m *inlayVisualModel) originalInlayResponse(method string) (json.RawMessage, error) {
	f, err := os.Open(m.tracePath)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	closeErr := f.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if len(data) > 1<<20 {
		return nil, fmt.Errorf("private RPC trace exceeded 1 MiB observer bound")
	}
	trace := string(data)
	index := strings.LastIndex(trace, "response '"+method+" -")
	if index < 0 {
		return nil, fmt.Errorf("real gopls trace has no original %s response", method)
	}
	result := strings.Index(trace[index:], "Result: ")
	if result < 0 {
		return nil, fmt.Errorf("real gopls %s response trace has no Result", method)
	}
	var raw json.RawMessage
	if err := json.NewDecoder(strings.NewReader(trace[index+result+len("Result: "):])).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode original gopls %s trace: %w", method, err)
	}
	return raw, nil
}

func (m *inlayVisualModel) verifyOriginalInlays(set types.LSPInlaySet) error {
	raw, err := m.originalInlayResponse("textDocument/inlayHint")
	if err != nil {
		return err
	}
	var rows []struct {
		Position                  types.LSPInlayPosition `json:"position"`
		Label                     json.RawMessage        `json:"label"`
		Kind                      int                    `json:"kind"`
		PaddingLeft, PaddingRight bool
	}
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) != 3 {
		return fmt.Errorf("original gopls inlay corpus differs: %s err=%v", raw, err)
	}
	for i, row := range rows {
		label := ""
		if err := json.Unmarshal(row.Label, &label); err != nil {
			var parts []struct{ Value string }
			if err := json.Unmarshal(row.Label, &parts); err != nil {
				return err
			}
			for _, part := range parts {
				label += part.Value
			}
		}
		h := set.Hints[i]
		if h.Index != i || h.Position != row.Position || h.Label != label || h.Kind != row.Kind ||
			h.PaddingLeft != row.PaddingLeft || h.PaddingRight != row.PaddingRight {
			return fmt.Errorf("original RPC order/label/geometry lost: raw=%s normalized=%+v", raw, set.Hints)
		}
	}
	return m.recordInlay(fmt.Sprintf("GOPLS_ORIGINAL textDocument/inlayHint standardResponseVersion=unreported %s", raw))
}

func inlayVisualReader(s *inlayInspectorState) string {
	for _, child := range s.screen.Children() {
		if reader, ok := child.(interface{ Value() string }); ok {
			return reader.Value()
		}
	}
	return ""
}

func (m *inlayVisualModel) observeInlay(phase int) error {
	if phase == 0 {
		if err := m.connect(); err != nil {
			return err
		}
		m.program.Send(tea.KeyPressMsg{Code: 'P', Text: inlayVisualDraft})
		if err := m.await(func(v *controlVisualModel) error {
			if v.Input.Value() != inlayVisualDraft {
				return fmt.Errorf("ordinary typing did not seed private draft: %q", v.Input.Value())
			}
			return nil
		}); err != nil {
			return err
		}
		for _, setup := range []struct {
			id string
			in map[string]any
		}{
			{"file.open", map[string]any{"path": m.path}},
			{"tree.set_visible", map[string]any{"visible": false}},
			{"editor.navigate", map[string]any{"line": 5, "column": 4}},
		} {
			if _, err := m.execute(setup.id, setup.in, nil); err != nil {
				return err
			}
		}
		if err := m.await(func(v *controlVisualModel) error {
			ed := v.EditorState.Editor
			source, current := ed.CurrentInsightSource()
			if !current || !v.ws.LSPMgr().ServerReady() || v.focusTarget() != FocusEditor {
				return fmt.Errorf("fixture has no acknowledged focused editor/ready real provider")
			}
			m.source, m.revision = source, ed.Revision()
			m.settings = types.CloneLSPSettings(v.ws.LSPMgr().Settings())
			m.trust = m.retained.LoadAppSettings().Analysis
			set, err := m.qualifiedInlay(v)
			if err != nil {
				return err
			}
			m.firstSet = *set
			return featureVisualPaint(v, inlayVisualPainted)
		}); err != nil {
			return err
		}
		pids, err := lspVisualProcesses(m.root, m.executable)
		if err != nil || len(pids) == 0 {
			return fmt.Errorf("exact real gopls fixture process not observed: pids=%v err=%v", pids, err)
		}
		m.pids = pids
		if err := m.verifyOriginalInlays(m.firstSet); err != nil {
			return err
		}
		if err := m.unchangedInlay(); err != nil {
			return err
		}
		return m.recordInlay(fmt.Sprintf("F3 SETUP automatic debounced visible-range hints installed; real gopls pids=%v RESOLVE_SUPPORTED=%t source=%+v revision=%d",
			pids, m.firstSet.ResolveSupported, m.source, m.revision))
	}
	label := ""
	switch phase {
	case 1, 4:
		label = "NATIVE_LIST"
		if err := m.await(func(v *controlVisualModel) error {
			s := v.inlayInspector
			if s == nil || !s.shown || !s.screen.InList() || s.reading || !v.overlayHost.IsOpen(ui.OverlayInlayHints) ||
				s.request.result == nil || !reflect.DeepEqual(*s.request.result, m.firstSet) {
				return fmt.Errorf("actual Alt+j or detail Escape did not leave exact native list:\n%s", ansi.Strip(v.View().RenderContent()))
			}
			if phase == 4 && (s != m.selectedReader || s.sequence <= m.readerSequence ||
				s.index != -1 || s.readCancel != nil) {
				return fmt.Errorf("Escape Back did not retire chosen-row reader/advance its sequence")
			}
			return featureVisualPaint(v, `"string"`, `"first:"`, `"count:"`, "Inspect", "Close")
		}); err != nil {
			return err
		}
	case 2, 3:
		label = "SELECTED_ROW_RAW_DETAIL"
		if phase == 3 {
			label = "EXPLICIT_RESOLVE_OR_UNSUPPORTED"
		}
		if err := m.await(func(v *controlVisualModel) error {
			s := v.inlayInspector
			if s == nil || !s.screen.InDetail() || s.reading || m.inspectionErr != nil {
				return fmt.Errorf("selected-row read-only detail not settled: err=%v", m.inspectionErr)
			}
			index, ok := s.screen.SelectedIndex()
			in := m.inspection
			if !ok || index != 0 || in.SetID != m.firstSet.ID || in.Digest != m.firstSet.Digest ||
				in.Index != index || in.WireVersion != m.firstSet.WireVersion || in.Range != m.firstSet.Range ||
				!reflect.DeepEqual(in.Boundary, m.firstSet.Boundary) || !json.Valid(in.OriginalJSON) {
				return fmt.Errorf("actual selected-row receipt lost original raw identity: %+v", in)
			}
			if phase == 2 && len(in.ResolvedJSON) != 0 {
				return fmt.Errorf("Inspect silently resolved provider tooltip")
			}
			if phase == 3 && m.firstSet.ResolveSupported && (len(in.ResolvedJSON) == 0 || !json.Valid(in.ResolvedJSON)) {
				return fmt.Errorf("advertised explicit Resolve has no original resolved raw evidence: %+v", in)
			}
			detail := inlayVisualReader(s)
			for _, want := range []string{
				"Position (zero-based UTF-16):", "Tooltip present:", "Original raw JSON:",
				"Read-only: unversioned standard hints.", "Source wire version:", "Source revision:",
				"Provider instance:", "Context generation:", "Unknown-input limits:",
				"Commands, text edits and label locations are inert metadata; never executed.",
				"Resolve state (literal):",
			} {
				if !strings.Contains(detail, want) {
					return fmt.Errorf("mounted native reader missing %q:\n%s", want, detail)
				}
			}
			if !strings.Contains(detail, fmt.Sprintf("%q", string(in.OriginalJSON))) {
				return fmt.Errorf("mounted raw preview is not literal original JSON: %s", detail)
			}
			m.selectedReader, m.readerSequence = s, s.sequence
			return featureVisualPaint(v, "Inlay hint #0", "Resolve tooltips", "Back", "Close")
		}); err != nil {
			return err
		}
		if phase == 2 {
			m.resolve <- m.firstSet.ResolveSupported
		}
		if phase == 3 && m.firstSet.ResolveSupported {
			raw, err := m.originalInlayResponse("inlayHint/resolve")
			if err != nil {
				return err
			}
			if err := m.recordInlay("GOPLS_ORIGINAL inlayHint/resolve " + string(raw)); err != nil {
				return err
			}
		}
	case 5:
		label = "ESCAPE_CLOSE"
		if err := m.await(func(v *controlVisualModel) error {
			if v.inlayInspector != nil || v.overlayHost.IsOpen(ui.OverlayInlayHints) ||
				m.selectedReader.ctx.Err() == nil || v.focusTarget() != FocusEditor {
				return fmt.Errorf("second Escape did not close/cancel native inspector and restore editor")
			}
			return featureVisualPaint(v, inlayVisualPainted)
		}); err != nil {
			return err
		}
	case 6:
		label = "PALETTE_HIDE_VIEW_LOCAL"
		if err := m.await(func(v *controlVisualModel) error {
			view := v.inlayViews[v.EditorState.Editor]
			if view == nil || !view.hidden || view.request != nil || v.EditorState.Editor.HasInlayProjection() ||
				v.overlayHost.IsOpen(ui.OverlayCommandPalette) {
				return fmt.Errorf("ordinary palette Hide did not release just this view")
			}
			paint := ansi.Strip(v.View().RenderContent())
			if strings.Contains(paint, "first:") || strings.Contains(paint, "count:") || strings.Contains(paint, "answer string") {
				return fmt.Errorf("hidden inlay labels remained painted:\n%s", paint)
			}
			return featureVisualPaint(v, `answer := join("hello", 2)`)
		}); err != nil {
			return err
		}
	case 7, 8:
		label = "PALETTE_SHOW"
		if phase == 8 {
			label = "PALETTE_REFRESH"
		}
		var observed types.LSPInlaySet
		if err := m.await(func(v *controlVisualModel) error {
			set, err := m.qualifiedInlay(v)
			if err != nil {
				return err
			}
			if set.ID == m.firstSet.ID || set.Sequence <= m.firstSet.Sequence ||
				phase == 8 && (set.ID == m.shownSet.ID || set.Sequence <= m.shownSet.Sequence) ||
				v.overlayHost.IsOpen(ui.OverlayCommandPalette) {
				return fmt.Errorf("ordinary Show/Refresh did not obtain a fresh exact-source set")
			}
			observed = *set
			return featureVisualPaint(v, inlayVisualPainted)
		}); err != nil {
			return err
		}
		if phase == 7 {
			m.shownSet = observed
		}
		if err := m.verifyOriginalInlays(observed); err != nil {
			return err
		}
	case 9, 10:
		label = "KEYBOARD_SOURCE_CARET"
		if phase == 10 {
			label = "POINTER_LABEL_SOURCE_ANCHOR"
		}
		var click [2]int
		if err := m.await(func(v *controlVisualModel) error {
			ed := v.EditorState.Editor
			line, col := ed.LSPCursorPosition()
			wantCol, sourceCol, paintCol := 16, 19, 33
			if phase == 10 {
				wantCol, sourceCol, paintCol = 25, 28, 49
			}
			if line != 5 || col != wantCol || ed.CursorCol() != sourceCol || ed.HasSelection() {
				return fmt.Errorf("ordinary source caret=%d:%d/display-source=%d want=5:%d/%d", line, col, ed.CursorCol(), wantCol, sourceCol)
			}
			if err := featureVisualPaint(v, inlayVisualPainted); err != nil {
				return err
			}
			rect, ok := v.rootHost.ScreenRect(v.editorScreen.ID())
			if !ok || rect.Empty() || ed.ScrollY() != 0 {
				return fmt.Errorf("fixture editor not mounted at original visible source")
			}
			x, y := ed.CursorScreenPosition(rect.X, rect.Y)
			if x != rect.X+ed.PadL+ed.GutterWidth()+paintCol || y != rect.Y+5 {
				return fmt.Errorf("painted caret x/y=%d/%d does not follow literal labels at source anchor (paint col=%d)", x, y, paintCol)
			}
			// Actual label cell, independent of source-to-display conversion:
			// tab=4, answer/type insertion=13, " := join("=9, first label=7,
			// "\"hello\", "=9 => count starts at display 42.
			countX := rect.X + ed.PadL + ed.GutterWidth() + 42
			countY := rect.Y + 5
			row := rowText(v.App, countY)
			// The shell gutter contains multi-byte icons. A terminal cell
			// coordinate must never be used as a UTF-8 string byte offset.
			labelByte := strings.Index(row, "count:")
			if labelByte < 0 || rendering.VisibleWidth(row[:labelByte]) != countX {
				return fmt.Errorf("literal count label click cell does not identify actual paint: x=%d row=%q", countX, row)
			}
			hintIndex, hit := ed.InlayHintAt(ed.GutterWidth()+42, 5)
			view := v.inlayViews[ed]
			if !hit || view == nil || view.request == nil || view.request.result == nil ||
				hintIndex < 0 || hintIndex >= len(view.request.result.Hints) ||
				view.request.result.Hints[hintIndex].Label != "count:" {
				return fmt.Errorf("actual painted label hit does not identify original count row")
			}
			click = [2]int{countX, countY}
			return nil
		}); err != nil {
			return err
		}
		if phase == 9 {
			m.click <- click
			if err := m.recordInlay(fmt.Sprintf("POINTER_COUNT x=%d y=%d expectedSourceUTF16=5:25", click[0], click[1])); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unknown inlay observation %d", phase)
	}
	if err := m.unchangedInlay(); err != nil {
		return err
	}
	var proof, paint string
	if err := m.check(func(v *controlVisualModel) error {
		var evidence any = m.firstSet
		if phase == 2 || phase == 3 {
			evidence = m.inspection
		} else if view := v.inlayViews[v.EditorState.Editor]; view != nil && view.request != nil && view.request.result != nil {
			evidence = view.request.result
		}
		raw, err := json.Marshal(evidence)
		if err != nil {
			return err
		}
		proof, paint = string(raw), ansi.Strip(v.View().RenderContent())
		return nil
	}); err != nil {
		return err
	}
	return m.recordInlay(fmt.Sprintf("F4 VERIFIED phase=%d %s unchanged=true standardResponseVersion=unreported\nRESULT %s\nFRAME\n%s",
		phase, label, proof, paint))
}
