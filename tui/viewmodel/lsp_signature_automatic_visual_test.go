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
// CRUDE_VISUAL_AUTOMATIC_SIGNATURE=1,
// CRUDE_ARGS=-test.run=^TestAutomaticSignatureVisualAcceptance$, and optionally
// CRUDE_AUTOMATIC_SIGNATURE_VISUAL_RESULT=/absolute/evidence.txt.
// Use COLS=160 ROWS=40, then (after that window closes) COLS=80 ROWS=24.
//
// External driver (wait for F3 SETUP):
// key:F3 sleep:3
// key:ctrl+End key:Up key:End key:F4
// 'type:(' key:F4
// type:x key:F4
// key:comma key:F4
// key:Escape key:F4
// key:alt+i key:F4
// key:shift+Right key:F4
// key:Escape key:Escape key:F4 quit:F9
//
// F3 sets up. F4 only observes; no requests, edits, caret setters or focus repair.
func TestAutomaticSignatureVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_AUTOMATIC_SIGNATURE") != "1" {
		t.Skip("dedicated-terminal installed-gopls automatic parameter-help acceptance")
	}
	runAutomaticSignatureVisual(t, false, 0, 0)
}

func TestAutomaticSignatureVisualHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		if !t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			runAutomaticSignatureVisual(t, true, size[0], size[1])
		}) {
			return
		}
	}
}

const automaticSignatureVisualSource = "package journey\n\nvar _ = join\n"
const automaticSignatureVisualOpen = "package journey\n\nvar _ = join()\n"
const automaticSignatureVisualArg = "package journey\n\nvar _ = join(x)\n"
const automaticSignatureVisualComma = "package journey\n\nvar _ = join(x,)\n"
const automaticSignatureVisualLib = "package journey\n\n// join returns the AUTOMATIC_SIGNATURE_NATIVE_SENTINEL first value.\nfunc join(first string, count int) string { return first }\n\nvar x = \"literal native argument\"\n"
const automaticSignatureVisualMod = "module journey\n\ngo 1.22\n"
const automaticSignatureVisualDraft = "Private automatic parameter draft stays unsent."
const automaticSignatureVisualLabel = "join(first string, count int) string"

type automaticSignatureVisualModel struct {
	*featureVisualModel
	retained   *workspace.Workspace
	phase      int
	executable string
	tracePath  string
	pids       []int
	transcript strings.Builder
	initial    types.InsightSource
	last       types.InsightSource
	settings   types.LSPSettings
	trust      types.WorkspaceAnalysisSettings
	reader     *ui.TextArea
}

type automaticSignatureVisualDone struct {
	phase int
	err   error
}

func runAutomaticSignatureVisual(t *testing.T, headless bool, width, height int) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("exact provider process ownership oracle requires Linux /proc")
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
		t.Fatalf("installed real gopls version: %s %v", version, err)
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
		"go.mod": automaticSignatureVisualMod, "source.go": automaticSignatureVisualSource,
		"lib.go": automaticSignatureVisualLib,
	}
	for name, text := range sources {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	tracePath := filepath.Join(home, "gopls-automatic-signature-rpc.log")
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
				"HOME": home, "GOROOT": strings.TrimSpace(string(goroot)), "GOPATH": filepath.Join(home, "go"),
				"GOCACHE": filepath.Join(home, "cache"), "GOENV": "off", "GOPROXY": "off",
				"GOSUMDB": "off", "GOTOOLCHAIN": "local",
			},
		}}
		w.SetAppDefaults(settings)
		grantSemanticFixture(t, w)
	})
	shell := newRemoteJourneyShell(t, coreRuntime, root)
	remoteJourneyCreate(t, shell)
	m := &automaticSignatureVisualModel{
		featureVisualModel: &featureVisualModel{
			controlVisualModel: &controlVisualModel{
				App: shell.app, runtime: coreRuntime, root: root, path: filepath.Join(root, "source.go"),
				ctx: t.Context(), completed: map[int]bool{},
			},
			settled: make(chan struct{}, 1), resultPath: os.Getenv("CRUDE_AUTOMATIC_SIGNATURE_VISUAL_RESULT"),
		},
		retained: retained, executable: executable, tracePath: tracePath,
	}
	defer func() {
		if len(m.pids) == 0 {
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
		joined := processErr == nil && len(m.pids) > 0 && len(remaining) == 0
		if !joined {
			t.Errorf("automatic parameter provider join unproven: started=%v remaining=%v err=%v", m.pids, remaining, processErr)
		}
		if err := m.recordAutomaticSignature(fmt.Sprintf("AUTOMATIC_SIGNATURE_PROVIDER_JOIN started=%v remaining=%v joined=%t err=%v",
			m.pids, remaining, joined, processErr)); err != nil {
			t.Error(err)
		}
		for name, want := range sources {
			got, err := os.ReadFile(filepath.Join(root, name))
			if err != nil || string(got) != want {
				t.Errorf("native unsaved journey changed disk %s: %q err=%v", name, got, err)
			}
		}
		if t.Failed() {
			if data, err := os.ReadFile(tracePath); err == nil {
				if len(data) > 16<<10 {
					data = data[len(data)-(16<<10):]
				}
				t.Logf("private installed-gopls RPC tail:\n%s", data)
			}
		} else if err := m.recordAutomaticSignature("AUTOMATIC_SIGNATURE_VISUAL_SUCCESS native delimiters; first/second parameters; passive caret card; editor x; Escape; selectable Alt+I reader; exact unsaved edits; unchanged disk/draft; joined local framed core"); err != nil {
			t.Error(err)
		}
		t.Log(m.transcript.String())
	}()
	if err := m.recordAutomaticSignature(fmt.Sprintf("AUTOMATIC_SIGNATURE_PROVIDER %s executable=%s viewport=%dx%d fixture=%s path=%s diskSHA256=%x trace=%s",
		strings.TrimSpace(string(version)), executable, width, height, root, m.path, sha256.Sum256([]byte(automaticSignatureVisualSource)), tracePath)); err != nil {
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
			m.driveAutomaticSignatureVisual(pumpCtx)
		}()
	}
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if m.failure != nil {
		if err := m.recordAutomaticSignature("AUTOMATIC_SIGNATURE_VISUAL_BLOCKED " + m.failure.Error()); err != nil {
			t.Error(err)
		}
		t.Fatal(m.failure)
	}
	if m.phase != 9 {
		t.Fatalf("automatic parameter observations incomplete: %d/9", m.phase)
	}
	if m.rootHost.HasPointerCapture() {
		t.Error("automatic parameter journey left pointer capture armed")
	}
}

func (m *automaticSignatureVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case automaticSignatureVisualDone:
		m.busy = false
		if v.err != nil {
			m.failure = fmt.Errorf("automatic signature boundary %d: %w", v.phase, v.err)
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
			return m, func() tea.Msg {
				return automaticSignatureVisualDone{phase, m.observeAutomaticSignature(phase)}
			}
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}

func (m *automaticSignatureVisualModel) driveAutomaticSignatureVisual(ctx context.Context) {
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
	for _, r := range "(x," {
		m.program.Send(tea.KeyPressMsg{Code: r, Text: string(r)})
		if !observe(tea.KeyF4) {
			return
		}
	}
	send(tea.KeyEscape, 0)
	if !observe(tea.KeyF4) {
		return
	}
	send('i', tea.ModAlt)
	if !observe(tea.KeyF4) {
		return
	}
	send(tea.KeyRight, tea.ModShift)
	if !observe(tea.KeyF4) {
		return
	}
	send(tea.KeyEscape, 0) // Native text selection, then the reader itself.
	send(tea.KeyEscape, 0)
	if !observe(tea.KeyF4) {
		return
	}
	send(tea.KeyF9, 0)
}

func (m *automaticSignatureVisualModel) recordAutomaticSignature(line string) error {
	fmt.Fprintln(&m.transcript, line)
	return m.evidence(line)
}

// Only F3 may dispatch setup actions.
func (m *automaticSignatureVisualModel) setupAutomaticSignature() error {
	if err := m.connect(); err != nil {
		return err
	}
	m.program.Send(tea.KeyPressMsg{Code: 'P', Text: automaticSignatureVisualDraft})
	if err := m.await(func(v *controlVisualModel) error {
		if v.Input.Value() != automaticSignatureVisualDraft {
			return fmt.Errorf("ordinary fixture draft input not retained: %q", v.Input.Value())
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
	} {
		if _, err := m.execute(setup.id, setup.in, nil); err != nil {
			return err
		}
	}
	if err := m.await(func(v *controlVisualModel) error {
		ed := v.EditorState.Editor
		source, ready := ed.CurrentInsightSource()
		if !ready || !v.ws.LSPMgr().ServerReady() || v.focusTarget() != FocusEditor ||
			ed.Value() != automaticSignatureVisualSource || ed.Modified() || ed.HasSelection() ||
			ed.DocumentState().PendingEdits != 0 || source.Kind != "canonical" || source.Path != m.path ||
			source.DocumentID == "" || source.Lifetime == "" || source.WorkspaceGeneration == "" {
			return fmt.Errorf("fixture lacks focused, acknowledged literal source and ready installed provider")
		}
		if v.signatureHelp != nil || v.overlayHost.IsOpen(signatureHelpID) {
			return fmt.Errorf("opening a source file fabricated parameter help")
		}
		m.initial, m.last = source, source
		m.settings = types.CloneLSPSettings(v.ws.LSPMgr().Settings())
		m.trust = m.retained.LoadAppSettings().Analysis
		return featureVisualPaint(v, "var _ = join")
	}); err != nil {
		return err
	}
	pids, err := lspVisualProcesses(m.root, m.executable)
	if err != nil || len(pids) == 0 {
		return fmt.Errorf("installed provider ownership missing: pids=%v err=%v", pids, err)
	}
	m.pids = pids
	snapshot, err := m.retained.LiveDocuments.Snapshot(m.initial.DocumentID)
	if err != nil || snapshot.Revision != m.initial.Revision || string(snapshot.Text) != automaticSignatureVisualSource {
		return fmt.Errorf("initial canonical source mismatch: %+v err=%v", snapshot, err)
	}
	return m.recordAutomaticSignature(fmt.Sprintf("F3 SETUP pids=%v source=%+v draftSHA256=%x; next native Ctrl+End Up End; F4 is read-only",
		pids, m.initial, sha256.Sum256([]byte(automaticSignatureVisualDraft))))
}

func (m *automaticSignatureVisualModel) observeAutomaticSignature(phase int) error {
	if phase == 0 {
		return m.setupAutomaticSignature()
	}
	wantText, wantCol, requests := automaticSignatureVisualComma, 15, 2
	switch phase {
	case 1:
		wantText, wantCol, requests = automaticSignatureVisualSource, 12, 0
	case 2:
		wantText, wantCol, requests = automaticSignatureVisualOpen, 13, 1
	case 3:
		wantText, wantCol, requests = automaticSignatureVisualArg, 14, 1
	case 6, 7, 8:
		requests = 3
	}
	var observed types.InsightSource
	var editorRevision uint64
	var help types.LSPSignatureHelp
	var paint string
	var caretX, caretY, cardX, cardY, cardW, cardH int
	if err := m.await(func(v *controlVisualModel) error {
		ed := v.EditorState.Editor
		source, ready := ed.CurrentInsightSource()
		line, col := ed.LSPCursorPosition()
		if !ready || source.Kind != "canonical" || source.Path != m.path ||
			source.DocumentID != m.initial.DocumentID || source.Lifetime != m.initial.Lifetime ||
			source.WorkspaceGeneration != m.initial.WorkspaceGeneration ||
			ed.Value() != wantText || ed.Modified() != (phase >= 2) ||
			ed.DocumentState().PendingEdits != 0 || ed.HasSelection() || line != 2 || col != wantCol ||
			v.Input.Value() != automaticSignatureVisualDraft || len(v.EditorState.Tabs) != 1 || !ed.IsOpen() {
			return fmt.Errorf("native source/caret/draft/tab mismatch: ready=%t source=%+v revision=%d text=%q modified=%t pending=%d caret=%d:%d draft=%q tabs=%d",
				ready, source, ed.Revision(), ed.Value(), ed.Modified(), ed.DocumentState().PendingEdits,
				line, col, v.Input.Value(), len(v.EditorState.Tabs))
		}
		mutated := phase == 2 || phase == 3 || phase == 4
		if mutated && source.Revision <= m.last.Revision || !mutated && source != m.last {
			return fmt.Errorf("native source revision failed to advance only for literal edits: previous=%+v now=%+v", m.last, source)
		}
		if !reflect.DeepEqual(v.ws.LSPMgr().Settings(), m.settings) ||
			!reflect.DeepEqual(m.retained.LoadAppSettings().Analysis, m.trust) {
			return fmt.Errorf("native feature journey changed trust/master/provider settings")
		}
		paint = ansi.Strip(v.View().RenderContent())
		shown := phase == 2 || phase == 4 || phase == 6 || phase == 7
		state := v.signatureHelp
		if !shown {
			if state != nil || v.signatureHelpIntent != nil || v.overlayHost.IsOpen(signatureHelpID) ||
				v.focusedPopup() != nil || v.focusTarget() != FocusEditor ||
				strings.Contains(paint, "Parameter information") || strings.Contains(paint, "Alt+I details") {
				return fmt.Errorf("ordinary input/Escape retained help or stole editor focus:\n%s", paint)
			}
			if !strings.Contains(paint, strings.TrimSpace(strings.TrimPrefix(wantText, "package journey\n\n"))) {
				return fmt.Errorf("native edited source not painted:\n%s", paint)
			}
		} else {
			automatic := phase == 2 || phase == 4
			if state == nil || !state.shown || state.result == nil || state.result.err != nil ||
				state.automatic != automatic || state.source != source || state.editor != ed ||
				state.path != m.path || state.revision != ed.Revision() || state.line != 2 || state.col != wantCol ||
				!v.overlayHost.IsOpen(signatureHelpID) {
				return fmt.Errorf("native delimiter/Alt+I has no exact-source installed reader")
			}
			help = state.result.help
			b := help.Boundary
			owner, generation := v.ws.LSPMgr().ContextCursor()
			if b.Source != source || b.Source.WorkspaceGeneration != owner ||
				b.Context.Generation != generation || b.Context.ID == "" || b.Context.Root != m.root ||
				b.Producer.Provider != m.executable || b.Producer.InstanceID == "" ||
				b.Producer.ToolIdentity != "launch-config:"+b.Context.ID || len(b.Context.UnknownInputs) == 0 {
				return fmt.Errorf("answer lost source/provider/context qualification: %+v cursor=%s/%d", b, owner, generation)
			}
			active := 1
			if phase == 2 {
				active = 0
			}
			if len(help.Signatures) != 1 || help.ActiveSignature != 0 {
				return fmt.Errorf("real provider signature count/selection: %+v", help)
			}
			sig := help.Signatures[0]
			if sig.Label != automaticSignatureVisualLabel || sig.ActiveParameter != active ||
				len(sig.Parameters) != 2 || sig.Parameters[0].Label != "first string" || sig.Parameters[1].Label != "count int" {
				return fmt.Errorf("literal real-gopls signature/first/second parameter mismatch: %+v", sig)
			}
			fw := v.overlayHost.Window(signatureHelpID)
			frame, ok := fw.Content.(*ui.WindowFrameWidget)
			if !ok {
				return fmt.Errorf("parameter reader is not a shared WindowFrame")
			}
			reader, ok := frame.Body().(*ui.TextReader)
			if !ok {
				return fmt.Errorf("parameter reader has no native text body")
			}
			area := reader.TextArea()
			if !strings.Contains(area.Value(), automaticSignatureVisualLabel) ||
				!strings.Contains(area.Value(), fmt.Sprintf("Active parameter %d of 2: %s", active+1, []string{"first string", "count int"}[active])) {
				return fmt.Errorf("actual mounted native TextArea lost literal provider parameter")
			}
			for _, marker := range []string{automaticSignatureVisualLabel, "Parameter information",
				fmt.Sprintf("Active parameter %d of 2:", active+1)} {
				if !strings.Contains(paint, marker) {
					return fmt.Errorf("native frame lacks %q:\n%s", marker, paint)
				}
			}
			rect, mounted := v.rootHost.ScreenRect(v.editorScreen.ID())
			if !mounted || rect.Empty() {
				return fmt.Errorf("reader source editor is not mounted")
			}
			caretX, caretY = ed.CursorScreenPosition(rect.X, rect.Y)
			cardX, cardY = fw.Position()
			cardW, cardH = fw.Size()
			if cardX > caretX || cardX+cardW <= caretX ||
				(cardY != caretY+1 && cardY+cardH != caretY && cardY+cardH+1 != caretY) ||
				cardX < 0 || cardY < 0 || cardX+cardW > v.width || cardY+cardH > v.height {
				return fmt.Errorf("reader detached from painted source caret: caret=%d,%d card=%d,%d %dx%d shell=%dx%d",
					caretX, caretY, cardX, cardY, cardW, cardH, v.width, v.height)
			}
			if automatic {
				if v.focusedPopup() != nil || v.focusTarget() != FocusEditor || area.HasActiveSelection() {
					return fmt.Errorf("automatic card captured input instead of remaining passive")
				}
			} else {
				if v.focusedPopup() != fw || !strings.Contains(area.Value(), "AUTOMATIC_SIGNATURE_NATIVE_SENTINEL") {
					return fmt.Errorf("Alt+I did not focus full qualified provider reader")
				}
				if phase == 6 {
					m.reader = area
				} else if area != m.reader || !area.HasActiveSelection() {
					return fmt.Errorf("native shifted arrow did not select the explicit reader")
				}
			}
		}
		observed, editorRevision = source, ed.Revision()
		return nil
	}); err != nil {
		return err
	}
	snapshot, err := m.retained.LiveDocuments.Snapshot(observed.DocumentID)
	if err != nil || snapshot.Revision != observed.Revision || string(snapshot.Text) != wantText {
		return fmt.Errorf("canonical unsaved source differs from native literal edit: source=%+v snapshot=%+v err=%v", observed, snapshot, err)
	}
	if err := m.verifyAutomaticSignatureTrace(requests); err != nil {
		return err
	}
	raw, err := json.Marshal(help)
	if err != nil {
		return err
	}
	m.last = observed
	return m.recordAutomaticSignature(fmt.Sprintf("F4 VERIFIED phase=%d source=%+v editorRevision=%d canonicalRevision=%d sourceUTF16=2:%d sourceByte=%d canonicalSHA256=%x requests=%d caret=%d,%d card=%d,%d %dx%d draftUnchanged=true\nRESULT %s\nFRAME\n%s",
		phase, observed, editorRevision, snapshot.Revision, wantCol, len("package journey\n\n")+wantCol,
		sha256.Sum256([]byte(wantText)), requests, caretX, caretY, cardX, cardY, cardW, cardH, raw, paint))
}

type automaticSignatureTraceEvent struct {
	offset int
	raw    json.RawMessage
}

func automaticSignatureTraceEvents(trace, marker, field string) ([]automaticSignatureTraceEvent, error) {
	var events []automaticSignatureTraceEvent
	for offset := 0; ; {
		index := strings.Index(trace[offset:], marker)
		if index < 0 {
			return events, nil
		}
		index += offset
		value := strings.Index(trace[index:], field)
		if value < 0 {
			return nil, fmt.Errorf("installed provider trace lacks %s after %s", field, marker)
		}
		var raw json.RawMessage
		if err := json.NewDecoder(strings.NewReader(trace[index+value+len(field):])).Decode(&raw); err != nil {
			return nil, fmt.Errorf("original provider trace %s: %w", marker, err)
		}
		events = append(events, automaticSignatureTraceEvent{index, raw})
		offset = index + len(marker)
	}
}

func (m *automaticSignatureVisualModel) verifyAutomaticSignatureTrace(count int) error {
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
		return fmt.Errorf("private signature RPC trace exceeded 1 MiB observation bound")
	}
	trace := string(data)
	requests, err := automaticSignatureTraceEvents(trace, "request 'textDocument/signatureHelp -", "Params: ")
	if err != nil {
		return err
	}
	responses, err := automaticSignatureTraceEvents(trace, "response 'textDocument/signatureHelp -", "Result: ")
	if err != nil {
		return err
	}
	if len(requests) != count || len(responses) != count {
		return fmt.Errorf("ordinary native input signature request/response count=%d/%d want=%d (observer must not request help)",
			len(requests), len(responses), count)
	}
	changes, err := automaticSignatureTraceEvents(trace, "notification 'textDocument/didChange", "Params: ")
	if err != nil {
		return err
	}
	previousVersion := 0
	for i, request := range requests {
		wantCol, wantActive, wantText := 15, 1, automaticSignatureVisualComma
		if i == 0 {
			wantCol, wantActive, wantText = 13, 0, automaticSignatureVisualOpen
		}
		var params struct {
			TextDocument struct{ URI string } `json:"textDocument"`
			Position     struct {
				Line, Character int
			} `json:"position"`
		}
		if err := json.Unmarshal(request.raw, &params); err != nil {
			return err
		}
		if params.TextDocument.URI != fileuri.FromPath(m.path) || params.Position.Line != 2 || params.Position.Character != wantCol {
			return fmt.Errorf("original request not at literal typed call: %s", request.raw)
		}
		// Last actual whole-document notification BEFORE this request.
		wireVersion, wireText := 0, ""
		for _, event := range changes {
			if event.offset >= request.offset {
				break
			}
			var change struct {
				TextDocument struct {
					URI     string `json:"uri"`
					Version int    `json:"version"`
				} `json:"textDocument"`
				ContentChanges []struct {
					Text  string          `json:"text"`
					Range json.RawMessage `json:"range"`
				} `json:"contentChanges"`
			}
			if err := json.Unmarshal(event.raw, &change); err != nil {
				return err
			}
			if change.TextDocument.URI != params.TextDocument.URI {
				continue
			}
			if len(change.ContentChanges) != 1 || len(change.ContentChanges[0].Range) != 0 {
				return fmt.Errorf("fixture expected one original whole-document update: %s", event.raw)
			}
			wireVersion, wireText = change.TextDocument.Version, change.ContentChanges[0].Text
		}
		if wireVersion <= 0 || wireText != wantText || i == 1 && wireVersion <= previousVersion ||
			i == 2 && wireVersion != previousVersion {
			return fmt.Errorf("original provider source before request %d: version=%d previous=%d text=%q want=%q",
				i, wireVersion, previousVersion, wireText, wantText)
		}
		previousVersion = wireVersion
		var response struct {
			Signatures []struct {
				Label           string                            `json:"label"`
				Parameters      []struct{ Label json.RawMessage } `json:"parameters"`
				ActiveParameter *int                              `json:"activeParameter"`
			} `json:"signatures"`
			ActiveSignature int `json:"activeSignature"`
			ActiveParameter int `json:"activeParameter"`
		}
		if err := json.Unmarshal(responses[i].raw, &response); err != nil {
			return err
		}
		if len(response.Signatures) != 1 || response.ActiveSignature != 0 ||
			response.Signatures[0].Label != automaticSignatureVisualLabel || len(response.Signatures[0].Parameters) != 2 {
			return fmt.Errorf("independent original gopls signature differs: %s", responses[i].raw)
		}
		active := response.ActiveParameter
		if override := response.Signatures[0].ActiveParameter; override != nil {
			active = *override
		}
		if active != wantActive {
			return fmt.Errorf("original provider active parameter=%d want=%d: %s", active, wantActive, responses[i].raw)
		}
		for p, parameter := range response.Signatures[0].Parameters {
			// Literal ASCII fixture UTF-16 spans.
			wantLabel := []string{"first string", "count int"}[p]
			wantSpan := [][2]int{{5, 17}, {19, 28}}[p]
			var label string
			if json.Unmarshal(parameter.Label, &label) == nil {
				if label != wantLabel {
					return fmt.Errorf("original parameter label=%q want=%q", label, wantLabel)
				}
			} else {
				var span [2]int
				if err := json.Unmarshal(parameter.Label, &span); err != nil || span != wantSpan {
					return fmt.Errorf("original parameter span=%s want=%v err=%v", parameter.Label, wantSpan, err)
				}
			}
		}
		if err := m.recordAutomaticSignature(fmt.Sprintf("GOPLS_ORIGINAL request=%d sourceWireVersion=%d sourceUTF16=2:%d standardResponseVersion=unreported\nPARAMS %s\nRESPONSE %s",
			i+1, wireVersion, wantCol, request.raw, responses[i].raw)); err != nil {
			return err
		}
	}
	return nil
}
