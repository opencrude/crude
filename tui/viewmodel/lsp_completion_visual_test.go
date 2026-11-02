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
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

// Sequential scripts/tuishot.sh captures use a prebuilt test binary,
// CRUDE_VISUAL_COMPLETION=1, CRUDE_ARGS=-test.run=^TestCompletionVisualAcceptance$,
// and CRUDE_COMPLETION_VISUAL_RESULT=/absolute/evidence.txt.
// Capture COLS=160 ROWS=40, close it, then COLS=80 ROWS=24.
//
// Outer driver (wait for F3 SETUP; allow each F4 VERIFIED before continuing):
// key:F3 sleep:3 key:ctrl+End key:Up key:End key:F4
// key:ctrl+space key:F4 key:Down key:F4 key:Up key:F4
// key:Escape key:F4
// key:ctrl+space sleep:1 key:Down sleep:1 key:Tab key:F4
// type:Z key:F4 key:Escape key:ctrl+z key:ctrl+z key:F4 quit:F9
//
// F3 is the only setup entry. F4 only observes the real App and retained source;
// it never requests completion/resolve, moves the caret, or repairs selection.
func TestCompletionVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_COMPLETION") != "1" {
		t.Skip("dedicated-terminal installed-gopls completion acceptance")
	}
	runCompletionVisual(t, false, 0, 0)
}

func TestCompletionVisualHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		if !t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			runCompletionVisual(t, true, size[0], size[1])
		}) {
			return
		}
	}
}

const completionVisualSource = "package journey\n\nvar _ = Doc\n"
const completionVisualAccepted = "package journey\n\nvar _ = DocBeta(value string)\n"
const completionVisualEdited = "package journey\n\nvar _ = DocBeta(Z)\n"
const completionVisualLib = `package journey

// DocAlpha returns COMPLETION_ALPHA_NATIVE_DOC for its argument.
func DocAlpha(value string) string { return value }

// DocBeta returns COMPLETION_BETA_NATIVE_DOC for its argument.
func DocBeta(value string) string { return value }
`
const completionVisualDraft = "Private completion draft stays unsent."

type completionVisualModel struct {
	*featureVisualModel
	retained              *workspace.Workspace
	phase                 int
	executable, tracePath string
	pids                  []int
	transcript            strings.Builder
	initial, last         types.InsightSource
	settings              types.LSPSettings
	trust                 types.WorkspaceAnalysisSettings
	firstSet              *types.LSPCompletionSet
	initialEntries        []types.LSPCompletionEntry
	alpha, beta           types.LSPCompletionEntry
	resolveSupported      bool // original installed-provider initialization evidence
}

type completionVisualDone struct {
	phase int
	err   error
}

func runCompletionVisual(t *testing.T, headless bool, width, height int) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("exact provider ownership oracle requires Linux /proc")
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
	sources := map[string]string{"go.mod": "module journey\n\ngo 1.22\n",
		"source.go": completionVisualSource, "lib.go": completionVisualLib, "unrelated.txt": "Unchanged private fixture.\n"}
	for name, text := range sources {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	tracePath := filepath.Join(home, "gopls-completion-rpc.log")
	var retained *workspace.Workspace
	coreRuntime := remoteJourneyRuntime(t, "http://127.0.0.1:1", func(w *workspace.Workspace) {
		retained = w
		settings := w.AppDefaults()
		settings.LSP = types.DefaultLSPSettings()
		settings.LSP.AutoDetect, settings.LSP.AutoComplete = false, false
		settings.LSP.Languages = map[string]types.LSPLanguageSettings{"go": {
			Enabled: true, Command: executable, Args: []string{"serve", "-rpc.trace", "-logfile=" + tracePath},
			RootMarkers: []string{"go.mod"}, Settings: map[string]any{"gopls": map[string]any{"usePlaceholders": true}},
			Environment: map[string]string{"HOME": home, "GOROOT": strings.TrimSpace(string(goroot)),
				"GOPATH": filepath.Join(home, "go"), "GOCACHE": filepath.Join(home, "cache"),
				"GOENV": "off", "GOPROXY": "off", "GOSUMDB": "off", "GOTOOLCHAIN": "local"},
		}}
		w.SetAppDefaults(settings)
		grantSemanticFixture(t, w) // durable private-root grant before attachment
	})
	shell := newRemoteJourneyShell(t, coreRuntime, root)
	remoteJourneyCreate(t, shell)
	ctx, cancel := context.WithCancel(t.Context())
	m := &completionVisualModel{featureVisualModel: &featureVisualModel{
		controlVisualModel: &controlVisualModel{App: shell.app, runtime: coreRuntime, root: root,
			path: filepath.Join(root, "source.go"), ctx: ctx, completed: map[int]bool{}},
		settled: make(chan struct{}, 1), resultPath: os.Getenv("CRUDE_COMPLETION_VISUAL_RESULT"),
	}, retained: retained, executable: executable, tracePath: tracePath}
	var workers sync.WaitGroup
	defer func() {
		if m.client != nil {
			if err := m.client.Close(); err != nil {
				t.Error(err)
			}
		}
		// The controller's original context must outlive its close receipt.
		// Only after that exchange settles may the fixture cancel/join pumps.
		cancel()
		workers.Wait()
		m.StopAsyncIO()
		if err := m.CloseWorkspaceAttachment(); err != nil {
			t.Error(err)
		}
		if err := coreRuntime.Close(); err != nil {
			t.Error(err)
		}
		remaining, err := lspVisualProcesses(root, executable)
		joined := err == nil && len(m.pids) > 0 && len(remaining) == 0
		if !joined {
			t.Errorf("completion provider join unproven: started=%v remaining=%v err=%v", m.pids, remaining, err)
		}
		for name, want := range sources {
			got, err := os.ReadFile(filepath.Join(root, name))
			if err != nil || string(got) != want {
				t.Errorf("unsaved journey changed disk %s: %q err=%v", name, got, err)
			}
		}
		if err := m.recordCompletion(fmt.Sprintf("COMPLETION_PROVIDER_JOIN started=%v remaining=%v joined=%t; journeyPassed=%t headless=%t",
			m.pids, remaining, joined, !t.Failed(), headless)); err != nil {
			t.Error(err)
		}
		t.Log(m.transcript.String())
	}()
	if err := m.recordCompletion(fmt.Sprintf("COMPLETION_PROVIDER %s executable=%s headless=%t viewport=%dx%d root=%s sourceSHA256=%x trace=%s",
		strings.TrimSpace(string(version)), executable, headless, width, height, root, sha256.Sum256([]byte(completionVisualSource)), tracePath)); err != nil {
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
		go func() { defer workers.Done(); m.driveCompletionVisual(ctx) }()
	}
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if m.failure != nil {
		t.Fatal(m.failure)
	}
	if m.phase != 9 || m.rootHost.HasPointerCapture() {
		t.Fatalf("completion journey incomplete/captured: phase=%d", m.phase)
	}
}

func (m *completionVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case completionVisualDone:
		m.busy = false
		if v.err != nil {
			m.failure = fmt.Errorf("completion boundary %d: %w", v.phase, v.err)
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
		if v.Code == tea.KeyF3 || v.Code == tea.KeyF4 {
			if m.busy || v.Code == tea.KeyF3 && m.phase != 0 || v.Code == tea.KeyF4 && (m.phase == 0 || m.phase >= 9) {
				return m, nil
			}
			m.busy = true
			phase := m.phase
			return m, func() tea.Msg { return completionVisualDone{phase, m.observeCompletion(phase)} }
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}

func (m *completionVisualModel) driveCompletionVisual(ctx context.Context) {
	send := func(code rune, mod tea.KeyMod) { m.program.Send(tea.KeyPressMsg{Code: code, Mod: mod}) }
	observe := func(code rune) bool {
		send(code, 0)
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
	send(tea.KeyEnd, tea.ModCtrl)
	send(tea.KeyUp, 0)
	send(tea.KeyEnd, 0)
	if !observe(tea.KeyF4) {
		return
	}
	for _, key := range []tea.KeyPressMsg{{Code: ' ', Mod: tea.ModCtrl}, {Code: tea.KeyDown}, {Code: tea.KeyUp}, {Code: tea.KeyEscape}} {
		m.program.Send(key)
		if !observe(tea.KeyF4) {
			return
		}
	}
	send(' ', tea.ModCtrl)
	// Passive mount wait substitutes only for the outer driver's pacing.
	if err := m.await(func(v *controlVisualModel) error {
		if v.completion == nil || v.completion.screen == nil || v.completion.pending != nil {
			return fmt.Errorf("second native invocation not settled")
		}
		return nil
	}); err != nil {
		m.program.Send(completionVisualDone{6, err})
		return
	}
	send(tea.KeyDown, 0)
	send(tea.KeyTab, 0)
	if !observe(tea.KeyF4) {
		return
	}
	m.program.Send(tea.KeyPressMsg{Code: 'Z', Text: "Z"})
	if !observe(tea.KeyF4) {
		return
	}
	send(tea.KeyEscape, 0)
	send('z', tea.ModCtrl)
	send('z', tea.ModCtrl)
	if observe(tea.KeyF4) {
		send(tea.KeyF9, 0)
	}
}

func (m *completionVisualModel) recordCompletion(line string) error {
	fmt.Fprintln(&m.transcript, line)
	return m.evidence(line)
}

func (m *completionVisualModel) setupCompletion() error {
	if err := m.connect(); err != nil {
		return err
	}
	m.program.Send(tea.KeyPressMsg{Code: 'P', Text: completionVisualDraft})
	if err := m.await(func(v *controlVisualModel) error {
		if v.Input.Value() != completionVisualDraft {
			return fmt.Errorf("ordinary private composer input not retained")
		}
		return nil
	}); err != nil {
		return err
	}
	if _, err := m.execute("file.open", map[string]any{"path": m.path}, nil); err != nil {
		return err
	}
	if _, err := m.execute("tree.set_visible", map[string]any{"visible": false}, nil); err != nil {
		return err
	}
	if err := m.await(func(v *controlVisualModel) error {
		ed := v.EditorState.Editor
		source, ready := ed.CurrentInsightSource()
		if !ready || !v.ws.LSPMgr().ServerReady() || v.focusTarget() != FocusEditor ||
			ed.Value() != completionVisualSource || ed.Modified() || ed.HasSelection() ||
			ed.DocumentState().PendingEdits != 0 || source.Kind != "canonical" || source.DocumentID == "" ||
			source.Lifetime == "" || source.WorkspaceGeneration == "" || v.completion != nil {
			return fmt.Errorf("setup source/provider/focus not ready")
		}
		m.initial, m.last = source, source
		m.settings = types.CloneLSPSettings(v.ws.LSPMgr().Settings())
		m.trust = m.retained.LoadAppSettings().Analysis
		return featureVisualPaint(v, "var _ = Doc")
	}); err != nil {
		return err
	}
	pids, err := lspVisualProcesses(m.root, m.executable)
	if err != nil || len(pids) == 0 {
		return fmt.Errorf("nonvacuous real provider ownership missing: %v %v", pids, err)
	}
	m.pids = pids
	return m.recordCompletion(fmt.Sprintf("F3 SETUP pids=%v source=%+v; next ordinary Ctrl+End Up End; F4 passive", pids, m.initial))
}

func (m *completionVisualModel) observeCompletion(phase int) error {
	if phase == 0 {
		return m.setupCompletion()
	}
	want, selected := completionVisualSource, ""
	switch phase {
	case 6:
		want, selected = completionVisualAccepted, "value string"
	case 7:
		want = completionVisualEdited
	}
	var source types.InsightSource
	var paint, docs string
	var set *types.LSPCompletionSet
	var row types.LSPCompletionEntry
	if err := m.await(func(v *controlVisualModel) error {
		ed := v.EditorState.Editor
		current, ready := ed.CurrentInsightSource()
		ds := ed.DocumentState()
		if !ready || current.Kind != "canonical" || current.Path != m.path ||
			current.DocumentID != m.initial.DocumentID || current.Lifetime != m.initial.Lifetime ||
			current.WorkspaceGeneration != m.initial.WorkspaceGeneration || ed.Value() != want ||
			ed.SelectedText() != selected || ed.SnippetActive() != (phase == 6 || phase == 7) ||
			ed.Modified() != (phase == 6 || phase == 7) || ds.Conflict != nil || ds.InFlight ||
			ds.PendingEdits != 0 || ds.UndoPending || ds.HistoryQueued || v.Input.Value() != completionVisualDraft ||
			len(v.EditorState.Tabs) != 1 || !reflect.DeepEqual(v.ws.LSPMgr().Settings(), m.settings) ||
			!reflect.DeepEqual(m.retained.LoadAppSettings().Analysis, m.trust) {
			return fmt.Errorf("literal source/field/canonical/draft/settings mismatch: source=%+v text=%q selected=%q snippet=%t state=%+v",
				current, ed.Value(), ed.SelectedText(), ed.SnippetActive(), ds)
		}
		if phase <= 5 && current != m.last || phase >= 6 && current.Revision <= m.last.Revision {
			return fmt.Errorf("source revision changed during browsing or failed to advance after edit/history: %+v -> %+v", m.last, current)
		}
		paint = ansi.Strip(v.View().RenderContent())
		shown := phase >= 2 && phase <= 4
		if !shown {
			if v.completion != nil || v.overlayHost.IsOpen(ui.OverlayCompletion) || v.focusedPopup() != nil ||
				v.focusTarget() != FocusEditor || strings.Contains(paint, "Completions ·") {
				return fmt.Errorf("completion chooser retained or editor focus lost:\n%s", paint)
			}
			if phase == 1 {
				line, col := ed.LSPCursorPosition()
				if line != 2 || col != 11 {
					return fmt.Errorf("ordinary Ctrl+End Up End caret=%d:%d want=2:11", line, col)
				}
			}
			if !strings.Contains(paint, strings.TrimSpace(strings.TrimPrefix(want, "package journey\n\n"))) {
				return fmt.Errorf("literal edited source absent from frame:\n%s", paint)
			}
		} else {
			s := v.completion
			if phase == 2 && m.firstSet == nil && s != nil && s.set != nil {
				m.firstSet = s.set
				for _, original := range s.set.Entries {
					original.Insertion = types.CloneCompletionItem(original.Insertion)
					m.initialEntries = append(m.initialEntries, original)
				}
			}
			if s == nil || s.screen == nil || s.set == nil || s.pending != nil || !v.completionCurrent(s) || s.automatic {
				return fmt.Errorf("ordinary Ctrl+Space native chooser/resolve not settled")
			}
			set = s.set
			b := set.Boundary
			owner, generation := v.ws.LSPMgr().ContextCursor()
			enabled, feature := v.ws.LSPMgr().CompletionsState()
			if b.Source != current || owner != current.WorkspaceGeneration || generation != b.Context.Generation ||
				b.Context.ID == "" || b.Context.Root != m.root || len(b.Context.UnknownInputs) == 0 ||
				b.Producer.Provider != m.executable || b.Producer.InstanceID == "" ||
				b.Producer.ToolIdentity != "launch-config:"+b.Context.ID || !enabled || feature != set.FeatureGeneration ||
				set.WireVersion <= 0 || set.Line != 2 || set.Col != 11 || set.ResolveSupported != m.resolveSupported {
				return fmt.Errorf("completion source/wire/producer/context/feature boundary mismatch: %+v", set)
			}
			for _, candidate := range set.Entries {
				if candidate.ID == s.screen.SelectedID() {
					row = candidate
				}
			}
			name, marker, absent := "DocAlpha", "COMPLETION_ALPHA_NATIVE_DOC", "COMPLETION_BETA_NATIVE_DOC"
			if phase == 3 {
				name, marker, absent = "DocBeta", absent, marker
			}
			if row.ID == "" || strings.Split(row.Insertion.Label, "(")[0] != name || m.resolveSupported && !s.observed[row.ID] {
				return fmt.Errorf("ordinary first/Down/Up selected unexpected original row: selected=%+v entries=%+v", row, set.Entries)
			}
			if set != m.firstSet || !reflect.DeepEqual(set.Entries, m.initialEntries) {
				return fmt.Errorf("documentation resolve changed initial original rows/insertions")
			}
			if phase == 3 {
				plan := row.Insertion.Snippet
				if row.Insertion.InsertText != "DocBeta(value string)" || plan == nil {
					return fmt.Errorf("actual provider insertion/placeholder differs: %+v", row.Insertion)
				}
				fields := 0
				for _, occurrence := range plan.Occurrences {
					if occurrence.Stop > 0 {
						fields++
						if occurrence.Stop != 1 || occurrence.StartByte != 8 || occurrence.EndByte != 20 {
							return fmt.Errorf("actual numeric field differs: %+v", plan)
						}
					}
				}
				if fields != 1 {
					return fmt.Errorf("unexpected numeric mirrors/fields: %+v", plan)
				}
			}
			fw := v.overlayHost.Window(ui.OverlayCompletion)
			if fw == nil {
				return fmt.Errorf("native chooser frame absent")
			}
			frame, ok := fw.Content.(*ui.WindowFrameWidget)
			if !ok || frame.Body() != s.screen || v.focusedPopup() != fw {
				return fmt.Errorf("chooser not mounted/focused through shared frame")
			}
			for _, child := range s.screen.Children() {
				if area, ok := child.(interface{ Value() string }); ok {
					docs = area.Value()
				}
			}
			status := "Documentation: initial"
			if m.resolveSupported {
				status = "Documentation: resolved"
			}
			if !strings.Contains(docs, marker) || strings.Contains(docs, absent) ||
				!strings.Contains(docs, status) || !strings.Contains(paint, marker) ||
				strings.Contains(paint, absent) || !strings.Contains(paint, "Completions") {
				return fmt.Errorf("selected native documentation leaked/missing: docs=%q\n%s", docs, paint)
			}
			if phase == 4 && row.ID != m.alpha.ID {
				return fmt.Errorf("Up did not restore exact Alpha item identity")
			}
		}
		source = current
		return nil
	}); err != nil {
		return err
	}
	snapshot, err := m.retained.LiveDocuments.Snapshot(source.DocumentID)
	if err != nil || snapshot.Revision != source.Revision || string(snapshot.Text) != want {
		return fmt.Errorf("actual retained canonical source differs: %+v err=%v", snapshot, err)
	}
	switch phase {
	case 2:
		m.firstSet, m.alpha = set, row
		m.alpha.Insertion = types.CloneCompletionItem(row.Insertion)
	case 3:
		m.beta = row
		m.beta.Insertion = types.CloneCompletionItem(row.Insertion)
	}
	if err := m.verifyCompletionTrace(phase); err != nil {
		return err
	}
	m.last = source
	raw, err := json.Marshal(set)
	if err != nil {
		return err
	}
	return m.recordCompletion(fmt.Sprintf("F4 VERIFIED phase=%d source=%+v canonicalSHA256=%x selectedOriginalID=%s index=%d label=%q draftUnchanged=true\nSET %s\nDOCS %s\nFRAME\n%s",
		phase, source, sha256.Sum256([]byte(want)), row.ID, row.Index, row.Insertion.Label, raw, docs, paint))
}

func (m *completionVisualModel) verifyCompletionTrace(phase int) error {
	f, err := os.Open(m.tracePath)
	if err != nil {
		return err
	}
	data, readErr := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	closeErr := f.Close()
	if readErr != nil || closeErr != nil || len(data) > 1<<20 {
		return fmt.Errorf("bounded 1 MiB original trace read failed: read=%v close=%v size=%d", readErr, closeErr, len(data))
	}
	trace := string(data)
	events := func(marker, field string) ([]automaticSignatureTraceEvent, error) {
		return automaticSignatureTraceEvents(trace, marker, field)
	}
	init, err := events("request 'initialize -", "Params: ")
	if err != nil || len(init) != 1 {
		return fmt.Errorf("original initialize evidence missing: %v", err)
	}
	var initialization struct {
		Capabilities struct {
			TextDocument struct {
				Completion struct {
					DynamicRegistration bool `json:"dynamicRegistration"`
					CompletionItem      struct {
						SnippetSupport bool                          `json:"snippetSupport"`
						ResolveSupport struct{ Properties []string } `json:"resolveSupport"`
					} `json:"completionItem"`
				} `json:"completion"`
			} `json:"textDocument"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(init[0].raw, &initialization); err != nil {
		return err
	}
	caps := initialization.Capabilities.TextDocument.Completion
	if caps.DynamicRegistration || !caps.CompletionItem.SnippetSupport ||
		!reflect.DeepEqual(caps.CompletionItem.ResolveSupport.Properties, []string{"documentation", "detail"}) {
		return fmt.Errorf("actual initialization not static doc/detail-only completion: %s", init[0].raw)
	}
	serverInit, err := events("response 'initialize -", "Result: ")
	if err != nil || len(serverInit) != 1 {
		return fmt.Errorf("original server initialization evidence missing: %v", err)
	}
	var server struct {
		Capabilities struct {
			CompletionProvider struct {
				ResolveProvider bool `json:"resolveProvider"`
			} `json:"completionProvider"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(serverInit[0].raw, &server); err != nil {
		return err
	}
	m.resolveSupported = server.Capabilities.CompletionProvider.ResolveProvider
	opens, err := events("notification 'textDocument/didOpen", "Params: ")
	if err != nil {
		return err
	}
	wireVersion := 0
	for _, open := range opens {
		var params struct {
			TextDocument struct {
				URI, Text string
				Version   int
			} `json:"textDocument"`
		}
		if err := json.Unmarshal(open.raw, &params); err != nil {
			return err
		}
		if params.TextDocument.URI == fileuri.FromPath(m.path) && params.TextDocument.Text == completionVisualSource {
			wireVersion = params.TextDocument.Version
		}
	}
	if wireVersion <= 0 || m.firstSet != nil && m.firstSet.WireVersion != wireVersion {
		return fmt.Errorf("original didOpen literal source/wire version differs: wire=%d", wireVersion)
	}
	requests, err := events("request 'textDocument/completion -", "Params: ")
	if err != nil {
		return err
	}
	wantCount := 1
	if phase == 1 {
		wantCount = 0
	} else if phase >= 6 {
		wantCount = 2
	}
	if len(requests) != wantCount {
		return fmt.Errorf("ordinary explicit completion requests=%d want=%d", len(requests), wantCount)
	}
	for _, request := range requests {
		var params struct {
			TextDocument struct{ URI string }          `json:"textDocument"`
			Position     struct{ Line, Character int } `json:"position"`
			Context      struct{ TriggerKind int }     `json:"context"`
		}
		if err := json.Unmarshal(request.raw, &params); err != nil {
			return err
		}
		if params.TextDocument.URI != fileuri.FromPath(m.path) || params.Position.Line != 2 ||
			params.Position.Character != 11 || params.Context.TriggerKind != 1 {
			return fmt.Errorf("original completion not explicit at literal Doc caret: %s", request.raw)
		}
	}
	if phase == 1 {
		return m.recordCompletion(fmt.Sprintf("GOPLS_INITIALIZE %s\nGOPLS_SERVER_INITIALIZE resolveSupported=%t %s",
			init[0].raw, m.resolveSupported, serverInit[0].raw))
	}
	responses, err := events("response 'textDocument/completion -", "Result: ")
	if err != nil || len(responses) != wantCount {
		return fmt.Errorf("original completion response count=%d want=%d err=%v", len(responses), wantCount, err)
	}
	resolves, err := events("request 'completionItem/resolve -", "Params: ")
	if err != nil {
		return err
	}
	resolved, err := events("response 'completionItem/resolve -", "Result: ")
	if err != nil {
		return err
	}
	if !m.resolveSupported && (len(resolves) != 0 || len(resolved) != 0) {
		return fmt.Errorf("unadvertised resolve was probed: requests=%d responses=%d", len(resolves), len(resolved))
	}
	if m.resolveSupported && phase <= 5 {
		want := 2
		if phase == 2 {
			want = 1
		}
		if len(resolves) != want || len(resolved) != want {
			return fmt.Errorf("selected resolve/cache RPC count=%d/%d want=%d", len(resolves), len(resolved), want)
		}
	}
	for i, row := range []types.LSPCompletionEntry{m.alpha, m.beta} {
		if row.ID == "" {
			continue
		}
		var list struct {
			Items []json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(responses[0].raw, &list); err != nil {
			return err
		}
		if row.Index < 0 || row.Index >= len(list.Items) {
			return fmt.Errorf("original row/index/resolve evidence absent: %+v", row)
		}
		if !m.resolveSupported {
			var original struct {
				Label, Documentation string
			}
			if err := json.Unmarshal(list.Items[row.Index], &original); err != nil {
				return err
			}
			marker := []string{"COMPLETION_ALPHA_NATIVE_DOC", "COMPLETION_BETA_NATIVE_DOC"}[i]
			if original.Label != row.Insertion.Label || !strings.Contains(original.Documentation, marker) {
				return fmt.Errorf("initial original documentation differs: %s", list.Items[row.Index])
			}
			if err := m.recordCompletion(fmt.Sprintf("GOPLS_ORIGINAL_INITIAL_DOCUMENTATION phase=%d item=%s index=%d resolveSupported=false noResolveRPC=true\nITEM %s",
				phase, row.ID, row.Index, list.Items[row.Index])); err != nil {
				return err
			}
			continue
		}
		if i >= len(resolves) || i >= len(resolved) {
			return fmt.Errorf("original selected-row resolve evidence absent: %+v", row)
		}
		var original, request, response map[string]any
		for _, pair := range []struct {
			raw json.RawMessage
			out *map[string]any
		}{{list.Items[row.Index], &original}, {resolves[i].raw, &request}, {resolved[i].raw, &response}} {
			if err := json.Unmarshal(pair.raw, pair.out); err != nil {
				return err
			}
		}
		marker := []string{"COMPLETION_ALPHA_NATIVE_DOC", "COMPLETION_BETA_NATIVE_DOC"}[i]
		if original["label"] != row.Insertion.Label || original["data"] == nil || !reflect.DeepEqual(original, request) ||
			response["label"] != original["label"] || !strings.Contains(string(resolved[i].raw), marker) {
			return fmt.Errorf("resolve lost exact original data/label or literal documentation:\nORIGINAL %s\nREQUEST %s\nRESPONSE %s",
				list.Items[row.Index], resolves[i].raw, resolved[i].raw)
		}
		if err := m.recordCompletion(fmt.Sprintf("GOPLS_ORIGINAL phase=%d item=%s index=%d\nITEM %s\nRESOLVE_PARAMS %s\nRESOLVE_RESPONSE %s",
			phase, row.ID, row.Index, list.Items[row.Index], resolves[i].raw, resolved[i].raw)); err != nil {
			return err
		}
	}
	return nil
}
