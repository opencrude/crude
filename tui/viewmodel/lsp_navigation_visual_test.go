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
	"github.com/charmbracelet/x/ansi"

	"crude/shellport"
	"crude/types"
	"crude/workspace"
)

// Native capture belongs to the sequential scripts/tuishot.sh driver:
// CRUDE_VISUAL_NAVIGATION=1 CRUDE_ARGS=-test.run=^TestNavigationVisualAcceptance$
// CRUDE_NAVIGATION_VISUAL_RESULT receives each side-effect-free observation.
//
// F3 prepares the disposable fixture. Thereafter the DRIVER supplies:
// Alt+t F4; Alt+m F4; Enter F4; Enter F4;
// Alt+m F4; Enter F4; Escape F4; Escape F4;
// Alt+m F4; Down Enter F4; Left Left Enter F4;
// Ctrl+Home Down Down Down End Left x10 Alt+. F4;
// Alt+; F4; Enter F4; Escape F4; Escape F4; F9.
// F4 never navigates, chooses a row, opens, closes, or changes focus.
func TestNavigationVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_NAVIGATION") != "1" {
		t.Skip("dedicated terminal real-provider navigation acceptance only")
	}
	runNavigationVisual(t, false, 0, 0)
}

func TestNavigationVisualHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		if !t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			runNavigationVisual(t, true, size[0], size[1])
		}) {
			return // Do not repeat unchanged failed real-provider launches.
		}
	}
}

const navigationVisualDraft = "Private navigation draft stays unsent."

// Coordinates below are independently literal zero-based source/UTF-16 positions,
// not computed using the product's location parser or editor conversion helpers.
var navigationVisualSources = map[string]string{
	"go.mod":      "module journey\n\ngo 1.22\n",
	"contract.go": "package journey\n\ntype Speaker interface { Speak() string }\n",
	"alpha.go":    "package journey\n\ntype Alpha struct{}\nfunc (Alpha) Speak() string { return Shared() }\n",
	"beta.go":     "package journey\n\ntype Beta struct{}\nfunc (Beta) Speak() string { return Shared() }\n",
	"helper.go":   "package journey\n\nfunc Shared() string { return \"navigation fixture\" }\n",
	"caller.go":   "package journey\n\nvar Selected Speaker = Alpha{}\n\nfunc Call() string {\n\treturn Selected.Speak()\n}\n",
}

type navigationVisualModel struct {
	*featureVisualModel
	phase           int
	executable      string
	tracePath       string
	pids            []int
	transcript      strings.Builder
	invocation      types.InsightSource
	invokeLine      int
	invokeCol       int
	lastResult      types.LSPNavigation
	lastResultErr   error
	firstBoundary   types.InsightBoundary
	barrierBoundary types.InsightBoundary
	implementation  types.LSPNavigation
	second          types.LSPNavigationTarget
}

type navigationVisualDone struct {
	phase int
	err   error
}

func runNavigationVisual(t *testing.T, headless bool, width, height int) {
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
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CRUDE_HOME", home)
	for name, text := range navigationVisualSources {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	tracePath := filepath.Join(home, "gopls-navigation-rpc.log")
	coreRuntime := remoteJourneyRuntime(t, "http://127.0.0.1:1", func(w *workspace.Workspace) {
		settings := w.AppDefaults()
		settings.LSP = types.DefaultLSPSettings()
		settings.LSP.AutoDetect, settings.LSP.AutoComplete = false, false
		settings.LSP.Languages = map[string]types.LSPLanguageSettings{"go": {
			Enabled: true, Command: executable, Args: []string{"serve", "-rpc.trace", "-logfile=" + tracePath},
			RootMarkers: []string{"go.mod"},
			Environment: map[string]string{
				"HOME": home, "GOROOT": strings.TrimSpace(string(goroot)), "GOPATH": filepath.Join(home, "go"),
				"GOCACHE": filepath.Join(home, "cache"), "GOENV": "off", "GOPROXY": "off",
				"GOSUMDB": "off", "GOTOOLCHAIN": "local",
			},
		}}
		w.SetAppDefaults(settings)
		grantSemanticFixture(t, w)
	})
	shell := newRemoteJourneyShell(t, coreRuntime, root)
	// Establish a real retained session through the existing launcher command
	// and its normal App.Update receipt before the Program owns the Update lane.
	// Without a session the workspace home intentionally has no composer.
	remoteJourneyCreate(t, shell)
	m := &navigationVisualModel{
		featureVisualModel: &featureVisualModel{
			controlVisualModel: &controlVisualModel{App: shell.app, runtime: coreRuntime, root: root,
				path: filepath.Join(root, "caller.go"), ctx: t.Context(), completed: map[int]bool{}},
			settled: make(chan struct{}, 1), resultPath: os.Getenv("CRUDE_NAVIGATION_VISUAL_RESULT"),
		},
		executable: executable, tracePath: tracePath,
	}
	defer func() {
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
			t.Errorf("navigation provider ownership unproven: started=%v remaining=%v err=%v", m.pids, remaining, processErr)
		}
		if err := m.recordNavigation(fmt.Sprintf("NAVIGATION_PROVIDER_JOIN started=%v remaining=%v joined=%t err=%v",
			m.pids, remaining, joined, processErr)); err != nil {
			t.Error(err)
		}
		if !t.Failed() {
			if err := m.recordNavigation("NAVIGATION_VISUAL_SUCCESS four real gopls operations; ordinary inputs; second-target Open; unchanged bytes/private draft; local framed core only"); err != nil {
				t.Error(err)
			}
		} else {
			// Only this private fixture's bounded trace, never a live user's logs.
			data, readErr := os.ReadFile(tracePath)
			if readErr == nil {
				if len(data) > 24<<10 {
					data = data[len(data)-(24<<10):]
				}
				t.Logf("fixture RPC tail:\n%s", data)
			}
		}
		t.Log(m.transcript.String())
	}()
	if err := m.recordNavigation(fmt.Sprintf("NAVIGATION_PROVIDER %s executable=%s viewport=%dx%d fixture=%s",
		strings.TrimSpace(string(version)), executable, width, height, root)); err != nil {
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
			m.driveNavigationVisual(pumpCtx)
		}()
	}
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if m.failure != nil {
		if err := m.recordNavigation("NAVIGATION_VISUAL_BLOCKED " + m.failure.Error()); err != nil {
			t.Error(err)
		}
		t.Fatal(m.failure)
	}
	if m.phase != 11 {
		t.Fatalf("navigation observations incomplete: %d/11", m.phase)
	}
}

func (m *navigationVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case navigationResultMsg:
		m.lastResult, m.lastResultErr = v.result, v.err
	case navigationVisualDone:
		m.busy = false
		if v.err != nil {
			m.failure = fmt.Errorf("navigation boundary %d: %w", v.phase, v.err)
			return m, tea.Quit
		}
		m.phase = v.phase + 1
		select {
		case m.settled <- struct{}{}:
		default:
		}
		return m, nil
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
			return m, func() tea.Msg { return navigationVisualDone{phase, m.observeNavigation(phase)} }
		}
		// Read-only invocation capture on the same Update lane, before the REAL
		// registry sees the key. No requestNavigationCmd calls or fake messages.
		if v.Mod == tea.ModAlt && strings.ContainsRune(".tm;", v.Code) {
			if ed := m.EditorState.Editor; ed != nil {
				m.invocation, _ = ed.CurrentInsightSource()
				m.invokeLine, m.invokeCol = ed.LSPCursorPosition()
				m.lastResult, m.lastResultErr = types.LSPNavigation{}, nil
				m.barrierBoundary = types.InsightBoundary{}
			}
		}
	}
	_, cmd := m.App.Update(msg)
	// Observe the original pushed-context barrier while it is still current,
	// before target opening can legitimately advance the manager context.
	if s := m.navigation; s != nil && s.shown && s.result != nil && !s.landing {
		owner, generation := m.ws.LSPMgr().ContextCursor()
		if s.result.Boundary.Source.WorkspaceGeneration == owner && s.result.Boundary.Context.Generation == generation {
			m.barrierBoundary = s.result.Boundary
		}
	}
	return m, cmd
}

func (m *navigationVisualModel) driveNavigationVisual(ctx context.Context) {
	key := func(code rune) { m.program.Send(tea.KeyPressMsg{Code: code}) }
	alt := func(code rune) { m.program.Send(tea.KeyPressMsg{Code: code, Mod: tea.ModAlt}) }
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
	alt('t')
	if !observe(tea.KeyF4) {
		return
	}
	for invocation := 0; invocation < 3; invocation++ {
		alt('m')
		if !observe(tea.KeyF4) {
			return
		}
		if invocation == 2 {
			key(tea.KeyDown)  // Deliberately not the initial/first implementation.
			key(tea.KeyEnter) // Choosing the second row goes straight to its code.
		} else {
			key(tea.KeyEsc) // Close without opening anything.
		}
		if !observe(tea.KeyF4) {
			return
		}
	}
	m.program.Send(tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModCtrl})
	for range 3 {
		key(tea.KeyDown)
	}
	key(tea.KeyEnd)
	for range 10 {
		key(tea.KeyLeft) // Shared call: same suffix in either implementation.
	}
	alt('.')
	if !observe(tea.KeyF4) {
		return
	}
	alt(';')
	if !observe(tea.KeyF4) {
		return
	}
	key(tea.KeyEsc) // Close the references list without opening anything.
	if !observe(tea.KeyF4) {
		return
	}
	key(tea.KeyF9)
}

func (m *navigationVisualModel) recordNavigation(line string) error {
	fmt.Fprintln(&m.transcript, line)
	return m.evidence(line)
}

func (m *navigationVisualModel) navigationUnchanged() error {
	if err := m.check(func(v *controlVisualModel) error {
		if v.Input.Value() != navigationVisualDraft {
			return fmt.Errorf("navigation changed private draft: %q", v.Input.Value())
		}
		for path, tab := range v.EditorState.Tabs {
			ed := tab.Editor
			want, known := navigationVisualSources[filepath.Base(path)]
			if !known || ed.Value() != want || ed.Modified() || ed.HasSelection() {
				return fmt.Errorf("navigation edited or armed replacement selection in %s: modified=%t selected=%q value=%q",
					path, ed.Modified(), ed.SelectedText(), ed.Value())
			}
		}
		return nil
	}); err != nil {
		return err
	}
	for name, want := range navigationVisualSources {
		got, err := os.ReadFile(filepath.Join(m.root, name))
		if err != nil || string(got) != want {
			return fmt.Errorf("navigation changed fixture disk %s: %q err=%v", name, got, err)
		}
	}
	return nil
}

func (m *navigationVisualModel) navigationLanding(name string, line, col int) error {
	return m.await(func(v *controlVisualModel) error {
		ed := v.EditorState.Editor
		if ed == nil {
			return fmt.Errorf("strict nonediting %s landing has no active editor", name)
		}
		if v.navigation != nil || v.overlayHost.IsOpen(navigationOverlay) ||
			ed.FilePath() != filepath.Join(m.root, name) || ed.CursorLine() != line || ed.CursorCol() != col ||
			ed.HasSelection() || ed.Value() != navigationVisualSources[name] || v.focusTarget() != FocusEditor {
			return fmt.Errorf("strict nonediting %s landing not visible: navigation=%t path=%s caret=%d:%d selected=%q\n%s",
				name, v.navigation != nil, ed.FilePath(), ed.CursorLine(), ed.CursorCol(), ed.SelectedText(), ansi.Strip(v.View().RenderContent()))
		}
		if _, current := ed.CurrentInsightSource(); !current {
			return fmt.Errorf("landing not canonically synchronized")
		}
		// Opening the target and publishing its active-file LSP state use
		// different asynchronous owners. A painted, synchronized document alone
		// is not readiness for the driver's next language-server command.
		if v.ws.LSPMgr() == nil || !v.ws.LSPMgr().ServerReady() {
			return fmt.Errorf("landed file's language server state is not ready")
		}
		return featureVisualPaint(v, strings.TrimSpace(strings.Split(navigationVisualSources[name], "\n")[line]))
	})
}

func (m *navigationVisualModel) navigationQualification(kind, sourceName string, line, col int) error {
	return m.check(func(v *controlVisualModel) error {
		r := m.lastResult
		b := r.Boundary
		owner, generation := v.ws.LSPMgr().ContextCursor()
		if m.lastResultErr != nil || r.Kind != kind || r.Answer != types.LSPNavigationTargets ||
			r.Line != line || r.Col != col || m.invokeLine != line || m.invokeCol != col ||
			r.IncludeDeclaration != (kind == types.LSPNavigationReferences) || r.WireVersion <= 0 ||
			b.Source != m.invocation || b.Source.Kind != "canonical" || b.Source.DocumentID == "" || b.Source.Lifetime == "" ||
			b.Source.Path != filepath.Join(m.root, sourceName) || b.Source.WorkspaceGeneration != owner ||
			b.Context.Root != m.root || b.Context.ID == "" || b.Context.Generation > generation ||
			!reflect.DeepEqual(b, m.barrierBoundary) || v.navigation != nil && b.Context.Generation != generation ||
			b.Producer.Provider != m.executable || b.Producer.InstanceID == "" || b.Producer.ToolIdentity != "launch-config:"+b.Context.ID ||
			len(b.Context.UnknownInputs) == 0 {
			return fmt.Errorf("actual navigation lost exact source/provider/context/root: result=%+v invocation=%+v cursor=%s/%d error=%v",
				r, m.invocation, owner, generation, m.lastResultErr)
		}
		if m.firstBoundary.Producer.InstanceID == "" {
			m.firstBoundary = b
		} else if b.Producer != m.firstBoundary.Producer || b.Source.WorkspaceGeneration != m.firstBoundary.Source.WorkspaceGeneration {
			return fmt.Errorf("navigation silently switched real provider or workspace")
		}
		return nil
	})
}

func navigationVisualRange(line, start, end int) types.LSPNavigationRange {
	return types.LSPNavigationRange{
		Start: types.LSPNavigationPosition{Line: line, Character: start},
		End:   types.LSPNavigationPosition{Line: line, Character: end},
	}
}

func (m *navigationVisualModel) navigationTargets(kind string) error {
	expected := map[string]types.LSPNavigationRange{}
	switch kind {
	case types.LSPNavigationTypeDefinition:
		expected["contract.go"] = navigationVisualRange(2, 5, 12)
	case types.LSPNavigationImplementation:
		expected["alpha.go"] = navigationVisualRange(2, 5, 10)
		expected["beta.go"] = navigationVisualRange(2, 5, 9)
	case types.LSPNavigationDefinition:
		expected["helper.go"] = navigationVisualRange(2, 5, 11)
	case types.LSPNavigationReferences:
		expected["alpha.go"] = navigationVisualRange(3, 37, 43)
		expected["beta.go"] = navigationVisualRange(3, 36, 42)
		expected["helper.go"] = navigationVisualRange(2, 5, 11)
	}
	var observed types.LSPNavigation
	if err := m.check(func(*controlVisualModel) error {
		observed = m.lastResult
		if len(m.lastResult.Targets) != len(expected) {
			return fmt.Errorf("real %s target count=%d want=%d: %+v", kind, len(m.lastResult.Targets), len(expected), m.lastResult.Targets)
		}
		seen := map[string]bool{}
		for _, target := range m.lastResult.Targets {
			name := filepath.Base(target.Path)
			want, ok := expected[name]
			uri := fileuri.FromPath(filepath.Join(m.root, name))
			if !ok || seen[name] || target.URI != uri || target.Selection != want || target.External || target.UnavailableReason != "" {
				return fmt.Errorf("real %s target differs from independent literal source position: %+v want=%+v", kind, target, want)
			}
			// A Location's range is exactly its selection. LocationLinks may
			// legitimately retain a larger targetRange and an optional origin.
			if !target.Link && (target.Range != want || target.Origin != nil) {
				return fmt.Errorf("Location invented LocationLink metadata: %+v", target)
			}
			seen[name] = true
		}
		return nil
	}); err != nil {
		return err
	}
	return m.navigationOriginalResponse(observed)
}

// Independently inspect the ORIGINAL installed server's trace, not lspnav's
// normalized answer. This guards LocationLink flag/range/origin preservation.
func (m *navigationVisualModel) navigationOriginalResponse(observed types.LSPNavigation) error {
	f, err := os.Open(m.tracePath)
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	closeErr := f.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if len(data) > 1<<20 {
		return fmt.Errorf("fixture RPC trace exceeded 1 MiB observer bound")
	}
	method := map[string]string{
		types.LSPNavigationDefinition: "definition", types.LSPNavigationTypeDefinition: "typeDefinition",
		types.LSPNavigationImplementation: "implementation", types.LSPNavigationReferences: "references",
	}[observed.Kind]
	trace := string(data)
	index := strings.LastIndex(trace, "response 'textDocument/"+method+" -")
	if index < 0 {
		return fmt.Errorf("real gopls trace has no original %s response", method)
	}
	result := strings.Index(trace[index:], "Result: ")
	if result < 0 {
		return fmt.Errorf("real gopls %s response trace has no Result", method)
	}
	var raw json.RawMessage
	if err := json.NewDecoder(strings.NewReader(trace[index+result+len("Result: "):])).Decode(&raw); err != nil {
		return fmt.Errorf("decode original gopls %s trace: %w", method, err)
	}
	var rows []struct {
		URI                  string                    `json:"uri"`
		Range                types.LSPNavigationRange  `json:"range"`
		TargetURI            string                    `json:"targetUri"`
		TargetRange          types.LSPNavigationRange  `json:"targetRange"`
		TargetSelectionRange types.LSPNavigationRange  `json:"targetSelectionRange"`
		Origin               *types.LSPNavigationRange `json:"originSelectionRange"`
	}
	if err := json.Unmarshal(raw, &rows); err != nil || len(rows) != len(observed.Targets) {
		return fmt.Errorf("original gopls %s targets differ: raw=%s err=%v", method, raw, err)
	}
	for i, row := range rows {
		target := observed.Targets[i]
		if row.TargetURI != "" {
			if !target.Link || target.URI != row.TargetURI || target.Range != row.TargetRange ||
				target.Selection != row.TargetSelectionRange || !reflect.DeepEqual(target.Origin, row.Origin) {
				return fmt.Errorf("original LocationLink metadata/order lost: original=%s normalized=%+v", raw, observed.Targets)
			}
		} else if target.Link || target.URI != row.URI || target.Range != row.Range ||
			target.Selection != row.Range || target.Origin != nil {
			return fmt.Errorf("original Location metadata/order lost: original=%s normalized=%+v", raw, observed.Targets)
		}
	}
	return m.recordNavigation(fmt.Sprintf("GOPLS_ORIGINAL %s %s", method, raw))
}

func (m *navigationVisualModel) observeNavigation(phase int) error {
	if phase == 0 {
		if err := m.connect(); err != nil {
			return err
		}
		// Seed the private draft through ordinary typing before opening a file.
		// No editor/composer fields are patched by this acceptance harness.
		m.program.Send(tea.KeyPressMsg{Code: 'P', Text: navigationVisualDraft})
		if err := m.await(func(v *controlVisualModel) error {
			if v.Input.Value() != navigationVisualDraft {
				return fmt.Errorf("fixture ordinary typing did not seed private draft: %q", v.Input.Value())
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
			// editor.navigate uses editor display columns: the initial tab is
			// four cells, so raw UTF-16 Selected at 8 is display column 11.
			{"editor.navigate", map[string]any{"line": 5, "column": 11}},
		} {
			if _, err := m.execute(setup.id, setup.in, nil); err != nil {
				return err
			}
		}
		if err := m.await(func(v *controlVisualModel) error {
			ed := v.EditorState.Editor
			if _, current := ed.CurrentInsightSource(); !current || !v.ws.LSPMgr().ServerReady() ||
				ed.CursorLine() != 5 || ed.CursorCol() != 11 || v.focusTarget() != FocusEditor {
				return fmt.Errorf("fixture caller not ready for actual Alt+t")
			}
			return featureVisualPaint(v, "Selected.Speak()")
		}); err != nil {
			return err
		}
		pids, err := lspVisualProcesses(m.root, m.executable)
		if err != nil || len(pids) == 0 {
			return fmt.Errorf("exact real gopls fixture process not observed: pids=%v err=%v", pids, err)
		}
		m.pids = pids
		if err := m.navigationUnchanged(); err != nil {
			return err
		}
		return m.recordNavigation(fmt.Sprintf("F3 SETUP caller.go raw UTF16=5:8; private draft preserved; gopls pids=%v", pids))
	}
	var label string
	switch phase {
	case 1:
		label = "TYPE_DEFINITION_AUTO"
		if err := m.navigationLanding("contract.go", 2, 5); err != nil {
			return err
		}
		if err := m.navigationQualification(types.LSPNavigationTypeDefinition, "caller.go", 5, 8); err != nil {
			return err
		}
		if err := m.navigationTargets(types.LSPNavigationTypeDefinition); err != nil {
			return err
		}
	case 2, 4, 6, 9:
		kind, sourceName, line, col := types.LSPNavigationImplementation, "contract.go", 2, 5
		if phase == 9 {
			kind, sourceName = types.LSPNavigationReferences, "helper.go"
		}
		label = strings.ToUpper(kind) + "_LIST"
		if err := m.await(func(v *controlVisualModel) error {
			s := v.navigation
			if s == nil || !s.shown || s.result == nil || !s.screen.InList() || !v.overlayHost.IsOpen(navigationOverlay) {
				return fmt.Errorf("native %s chooser not visible: state=%+v result=%+v error=%v invocation=%+v status=%q\n%s",
					kind, s, m.lastResult, m.lastResultErr, m.invocation, testStatusText(v.App), ansi.Strip(v.View().RenderContent()))
			}
			if v.EditorState.Editor.FilePath() != filepath.Join(m.root, sourceName) {
				return fmt.Errorf("listing silently chose/opened a target")
			}
			paint := ansi.Strip(v.View().RenderContent())
			if strings.Contains(paint, "[ Close ]") {
				return fmt.Errorf("navigation list kept a redundant Close footer")
			}
			return nil
		}); err != nil {
			return err
		}
		if err := m.navigationQualification(kind, sourceName, line, col); err != nil {
			return err
		}
		if err := m.navigationTargets(kind); err != nil {
			return err
		}
		if kind == types.LSPNavigationImplementation {
			if err := m.check(func(*controlVisualModel) error {
				m.implementation = m.lastResult
				m.second = m.lastResult.Targets[1]
				return nil
			}); err != nil {
				return err
			}
		}
	case 3, 5, 10:
		label = "CLOSE_NO_LANDING"
		name := "contract.go"
		if phase == 10 {
			name = "helper.go"
		}
		if err := m.navigationLanding(name, 2, 5); err != nil {
			return err
		}
	case 7:
		label = "SECOND_IMPLEMENTATION_OPEN"
		if err := m.navigationLanding(filepath.Base(m.second.Path), 2, 5); err != nil {
			return err
		}
		if m.second.Path == m.implementation.Targets[0].Path {
			return fmt.Errorf("second-target oracle accidentally selected first target")
		}
	case 8:
		label = "DEFINITION_AUTO"
		if err := m.navigationLanding("helper.go", 2, 5); err != nil {
			return err
		}
		sourceName := filepath.Base(m.second.Path)
		col := 36
		if sourceName == "alpha.go" {
			col = 37
		}
		if err := m.navigationQualification(types.LSPNavigationDefinition, sourceName, 3, col); err != nil {
			return err
		}
		if err := m.navigationTargets(types.LSPNavigationDefinition); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown navigation observation %d", phase)
	}
	if err := m.navigationUnchanged(); err != nil {
		return err
	}
	var proof, paint string
	if err := m.check(func(v *controlVisualModel) error {
		raw, err := json.Marshal(m.lastResult)
		if err != nil {
			return err
		}
		proof = string(raw)
		paint = ansi.Strip(v.View().RenderContent())
		return nil
	}); err != nil {
		return err
	}
	return m.recordNavigation(fmt.Sprintf("F4 VERIFIED phase=%d %s unchanged=true targetVersion=unreported sourceSHA256=%x\nRESULT %s\nFRAME\n%s",
		phase, label, sha256.Sum256([]byte(navigationVisualSources["caller.go"])), proof, paint))
}
