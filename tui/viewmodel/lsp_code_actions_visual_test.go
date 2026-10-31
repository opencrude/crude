package viewmodel

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/localhistory"
	"crude/shellport"
	"crude/types"
	"crude/workspace"
)

// Run sequentially through scripts/tuishot.sh, never alongside another capture:
// CRUDE_VISUAL_CODE_ACTION=1 CRUDE_ARGS=-test.run=^TestCodeActionVisualAcceptance$
// CRUDE_CODE_ACTION_VISUAL_RESULT receives each observer's rendered evidence.
//
// F3 opens/synchronizes the disposable fixture. Each F4 observes one boundary:
// list, detail, default Close, list, detail, preview, default Cancel, list,
// detail, fresh preview, committed. Between observers the capture driver supplies
// actual Alt+Enter, Down x printed zero-based row index, Enter to inspect, and
// Left/Left/Enter to Preview changes. Review Enter cancels; Left/Enter applies.
// F9 exits after all observations. Observers never invoke a scored action.
func TestCodeActionVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_CODE_ACTION") != "1" {
		t.Skip("dedicated terminal real-provider code-action acceptance only")
	}
	runCodeActionVisual(t, false, 0, 0)
}

func TestCodeActionVisualHeadless(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		if !t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			runCodeActionVisual(t, true, size[0], size[1])
		}) {
			// Preserve the first real-provider blocker, not repeated launches
			// against an unchanged unsupported protocol contract.
			return
		}
	}
}

const codeActionVisualBefore = "package journey\n\nimport \"fmt\"\n\nvar actionValue = 1\n"
const codeActionVisualAfter = "package journey\n\nvar actionValue = 1\n"

type codeActionVisualModel struct {
	*featureVisualModel
	retained     *workspace.Workspace
	phase        int
	row          workspace.CodeActionSummary
	firstSet     string
	cancelled    workspace.ReviewedEditPreview
	plan         workspace.ReviewedEditPreview
	lastResult   workspace.ReviewedEditResult
	executable   string
	pids         []int
	transcript   strings.Builder
	listRowIndex chan int
}

type codeActionVisualDone struct {
	phase int
	err   error
}

func runCodeActionVisual(t *testing.T, headless bool, width, height int) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("exact fixture provider process oracle uses Linux /proc")
	}
	gopls := os.Getenv("CRUDE_LSP_VISUAL_GOPLS")
	if gopls == "" {
		gopls, _ = exec.LookPath("gopls")
	}
	if gopls == "" {
		t.Fatal("installed gopls required; missing provider is not code-action acceptance")
	}
	executable, err := filepath.EvalSymlinks(gopls)
	if err != nil {
		t.Fatal(err)
	}
	version, err := exec.Command(executable, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("identify real provider: %s: %v", version, err)
	}
	goroot, err := exec.Command("go", "env", "GOROOT").Output()
	if err != nil {
		t.Fatal(err)
	}
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CRUDE_HOME", home)
	path := filepath.Join(root, "action.go")
	tracePath := filepath.Join(home, "gopls-rpc.log")
	for name, text := range map[string]string{
		"go.mod": "module journey\n\ngo 1.22\n", "action.go": codeActionVisualBefore,
		"unrelated.txt": "Must not become selected recovery content.\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
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
	m := &codeActionVisualModel{
		featureVisualModel: &featureVisualModel{
			controlVisualModel: &controlVisualModel{App: shell.app, runtime: coreRuntime, root: root, path: path,
				ctx: t.Context(), completed: map[int]bool{}},
			settled:    make(chan struct{}, 1),
			resultPath: os.Getenv("CRUDE_CODE_ACTION_VISUAL_RESULT"),
		},
		retained: retained, executable: executable, listRowIndex: make(chan int, 1),
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
			t.Errorf("code-action provider ownership unproven: started=%v remaining=%v err=%v", m.pids, remaining, processErr)
		}
		if err := m.recordCodeAction(fmt.Sprintf("CODE_ACTION_PROVIDER_JOIN started=%v remaining=%v joined=%t err=%v",
			m.pids, remaining, joined, processErr)); err != nil {
			t.Error(err)
		}
		if t.Failed() {
			if err := m.recordCodeAction("CODE_ACTION_RPC_EVIDENCE\n" + codeActionVisualTrace(tracePath)); err != nil {
				t.Error(err)
			}
		} else if err := m.recordCodeAction("CODE_ACTION_VISUAL_SUCCESS actual keys, exact canonical bytes, original Inspect receipt, selected safety, joined local framed core"); err != nil {
			t.Error(err)
		}
		t.Log(m.transcript.String())
	}()
	if err := m.recordCodeAction(fmt.Sprintf("CODE_ACTION_PROVIDER %s executable=%s viewport=%dx%d", strings.TrimSpace(string(version)), executable, width, height)); err != nil {
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
			m.driveCodeActionVisual(pumpCtx)
		}()
	}
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if m.failure != nil {
		if err := m.recordCodeAction("CODE_ACTION_VISUAL_BLOCKED " + m.failure.Error()); err != nil {
			t.Error(err)
		}
		t.Fatal(m.failure)
	}
	if m.phase != 12 {
		t.Fatalf("code-action observation boundaries incomplete: %d/12", m.phase)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != codeActionVisualAfter {
		t.Fatalf("actual code-action final disk bytes=%q want=%q err=%v", got, codeActionVisualAfter, err)
	}
}

func (m *codeActionVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case reviewedEditAppliedMsg:
		m.lastResult = v.result
	case controlVisualCheck:
		v.reply <- v.check(m.controlVisualModel)
		return m, nil
	case featureVisualAction:
		return m, v.run(m.App)
	case codeActionVisualDone:
		m.busy = false
		if v.err != nil {
			m.failure = fmt.Errorf("code-action boundary %d: %w", v.phase, v.err)
			return m, tea.Quit
		}
		m.phase = v.phase + 1
		select {
		case m.settled <- struct{}{}:
		default: // Native capture observes via its evidence file, not this channel.
		}
		return m, nil
	case tea.KeyPressMsg:
		if v.Code == tea.KeyF9 && !m.busy {
			return m, tea.Quit
		}
		if v.Code == tea.KeyF3 && m.phase == 0 || v.Code == tea.KeyF4 && m.phase > 0 && m.phase < 12 {
			if m.busy {
				return m, nil
			}
			m.busy = true
			phase := m.phase
			return m, func() tea.Msg { return codeActionVisualDone{phase, m.observeCodeAction(phase)} }
		}
	}
	_, cmd := m.App.Update(msg)
	return m, cmd
}

func (m *codeActionVisualModel) driveCodeActionVisual(ctx context.Context) {
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
	for invocation := 0; invocation < 3; invocation++ {
		m.program.Send(tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
		if !observe(tea.KeyF4) {
			return
		}
		var index int
		select {
		case index = <-m.listRowIndex:
		case <-ctx.Done():
			return
		}
		for range index {
			key(tea.KeyDown)
		}
		key(tea.KeyEnter) // Inspect only; Close must remain the default.
		if !observe(tea.KeyF4) {
			return
		}
		if invocation == 0 {
			key(tea.KeyEnter) // Actual safe-default Close, not preview.
		} else {
			key(tea.KeyLeft)
			key(tea.KeyLeft)
			key(tea.KeyEnter) // Explicit Preview changes.
			if !observe(tea.KeyF4) {
				return
			}
			if invocation == 2 {
				key(tea.KeyLeft) // Only this invocation explicitly chooses Apply.
			}
			key(tea.KeyEnter) // First review defaults to Cancel.
		}
		if !observe(tea.KeyF4) {
			return
		}
	}
	key(tea.KeyF9)
}

func (m *codeActionVisualModel) recordCodeAction(line string) error {
	fmt.Fprintln(&m.transcript, line)
	return m.evidence(line)
}

func (m *codeActionVisualModel) unchangedCodeAction() error {
	if err := m.check(func(v *controlVisualModel) error {
		if v.EditorState.Editor.Value() != codeActionVisualBefore || v.EditorState.Editor.Modified() {
			return fmt.Errorf("non-applying action changed the canonical editor")
		}
		return nil
	}); err != nil {
		return err
	}
	got, err := os.ReadFile(m.path)
	if err != nil || string(got) != codeActionVisualBefore {
		return fmt.Errorf("non-applying action changed disk: got=%q err=%v", got, err)
	}
	return nil
}

func (m *codeActionVisualModel) observeCodeAction(phase int) error {
	if phase == 0 {
		if err := m.connect(); err != nil {
			return err
		}
		if _, err := m.execute("file.open", map[string]any{"path": m.path}, nil); err != nil {
			return err
		}
		m.action(func(app *App) tea.Cmd {
			app.hideWorkspacePanel()
			app.setFocusEditor()
			return nil
		})
		// Fixture navigation is ordinary input, never cursor/state patching.
		for _, key := range []tea.KeyPressMsg{
			{Code: tea.KeyHome, Mod: tea.ModCtrl}, {Code: tea.KeyDown}, {Code: tea.KeyDown}, {Code: tea.KeyHome},
			{Code: tea.KeyRight}, {Code: tea.KeyRight}, {Code: tea.KeyRight}, {Code: tea.KeyRight},
			{Code: tea.KeyRight}, {Code: tea.KeyRight}, {Code: tea.KeyRight}, {Code: tea.KeyRight},
		} {
			m.program.Send(key)
		}
		if err := m.await(func(v *controlVisualModel) error {
			facts, ok := v.ws.LSPMgr().FileDiagnostics(m.path)
			source, current := v.EditorState.Editor.CurrentInsightSource()
			if !ok || !current || !facts.HasExactSource() || facts.Boundary.Source != source || len(facts.Diagnostics) == 0 {
				return fmt.Errorf("unused import diagnostic not synchronized to real canonical source")
			}
			return featureVisualPaint(v, `import "fmt"`)
		}); err != nil {
			return err
		}
		seen, err := lspVisualProcesses(m.root, m.executable)
		if err != nil || len(seen) == 0 {
			return fmt.Errorf("real code-action provider identity not observed: %v", err)
		}
		m.pids = seen
		return m.recordCodeAction(fmt.Sprintf("F3 VERIFIED unused-import canonical source synchronized; real gopls pids=%v", seen))
	}
	if phase == 1 || phase == 4 || phase == 8 {
		var set workspace.CodeActionSet
		if err := m.await(func(v *controlVisualModel) error {
			if v.codeActions == nil || v.codeActions.set.ID == "" {
				return fmt.Errorf("real code-action chooser not mounted:\n%s", ansi.Strip(v.View().RenderContent()))
			}
			set = v.codeActions.set
			return featureVisualPaint(v, "Code actions")
		}); err != nil {
			return err
		}
		if err := m.unchangedCodeAction(); err != nil {
			return err
		}
		if err := m.recordCodeAction(fmt.Sprintf("F4 LIST boundary=%d set=%s provider=%+v rows=%+v", phase, set.ID, set.Boundary.Producer, set.Items)); err != nil {
			return err
		}
		if phase == 1 {
			m.firstSet = set.ID
		} else if set.ID == m.firstSet {
			return fmt.Errorf("new invocation revived the original closed action set")
		}
		for _, row := range set.Items {
			if row.Title == "Organize Imports" && row.Kind == "source.organizeImports" {
				m.row = row
				if err := m.recordCodeAction(fmt.Sprintf("CODE_ACTION_ROW_INDEX=%d title=%q needsResolve=%t canReview=%t unsupported=%q",
					row.Index, row.Title, row.NeedsResolve, row.CanReview, row.UnsupportedReason)); err != nil {
					return err
				}
				select {
				case <-m.listRowIndex:
				default:
				}
				m.listRowIndex <- row.Index
				return nil
			}
		}
		return fmt.Errorf("real gopls did not offer the observed literal Organize Imports action; rows=%+v", set.Items)
	}
	if phase == 2 || phase == 5 || phase == 9 {
		var paint string
		if err := m.await(func(v *controlVisualModel) error {
			if v.codeActions == nil || v.codeActions.screen.InList() || v.LSPPopup.EditPreview != nil {
				return fmt.Errorf("row Enter did not inspect without preview")
			}
			paint = ansi.Strip(v.View().RenderContent())
			return featureVisualPaint(v, "Action title (literal, complete)", "Close")
		}); err != nil {
			return err
		}
		if err := m.recordCodeAction(fmt.Sprintf("F4 INSPECT boundary=%d original row=%+v\n%s", phase, m.row, paint)); err != nil {
			return err
		}
		if err := m.unchangedCodeAction(); err != nil {
			return err
		}
		if !m.row.CanReview {
			return fmt.Errorf("real gopls unused-import action cannot reach review: title=%q kind=%q lazy=%t disabled=%q unsupported=%q",
				m.row.Title, m.row.Kind, m.row.NeedsResolve, m.row.DisabledReason, m.row.UnsupportedReason)
		}
		if !strings.Contains(paint, "Preview changes") {
			return fmt.Errorf("eligible inspected row did not paint Preview changes:\n%s", paint)
		}
		return nil
	}
	if phase == 3 || phase == 7 {
		if err := m.await(func(v *controlVisualModel) error {
			if v.codeActions != nil || v.LSPPopup.EditPreview != nil {
				return fmt.Errorf("default %s left an action overlay open", map[int]string{3: "Close", 7: "Cancel"}[phase])
			}
			return featureVisualPaint(v, `import "fmt"`)
		}); err != nil {
			return err
		}
		if err := m.unchangedCodeAction(); err != nil {
			return err
		}
		if phase == 7 {
			if out, err := m.ws.(shellport.ReviewedEdits).InspectReviewedEdit(m.ctx, m.cancelled.ID, m.cancelled.Digest); err != nil || out.State != "pending" || out.SafetySnapshotID != "" {
				return fmt.Errorf("cancelled preview invented an admitted mutation or safety: %+v %v", out, err)
			}
		}
		return m.recordCodeAction(fmt.Sprintf("F4 SAFE_DEFAULT boundary=%d actual Enter closed without source mutation", phase))
	}
	if phase == 6 || phase == 10 {
		if err := m.await(func(v *controlVisualModel) error {
			p := v.LSPPopup.EditPreview
			if p == nil || p.request.preview.ID == "" || len(p.request.preview.Files) != 1 {
				return fmt.Errorf("real action review not mounted:\n%s", ansi.Strip(v.View().RenderContent()))
			}
			file := p.request.preview.Files[0]
			if file.BeforeSHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(codeActionVisualBefore))) ||
				file.AfterSHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(codeActionVisualAfter))) {
				return fmt.Errorf("review differs from the independently expected literal import removal: %+v", file)
			}
			if phase == 6 {
				m.cancelled = p.request.preview
			} else {
				m.plan = p.request.preview
				if m.plan.ID == m.cancelled.ID {
					return fmt.Errorf("fresh invocation revived a cancelled Apply permit")
				}
			}
			return featureVisualPaint(v, "Review code action", "Apply changes", "Cancel")
		}); err != nil {
			return err
		}
		if err := m.unchangedCodeAction(); err != nil {
			return err
		}
		return m.recordCodeAction(fmt.Sprintf("F4 PREVIEW boundary=%d immutable Cancel-default review; no disk or canonical effect", phase))
	}
	if err := m.await(func(v *controlVisualModel) error {
		out := m.lastResult
		if out.ID != m.plan.ID || out.Digest != m.plan.Digest || out.State != "committed" ||
			out.SafetySnapshotID == "" || out.SafetyReference != out.ID || v.LSPPopup.EditPreview != nil ||
			v.EditorState.Editor.Value() != codeActionVisualAfter || v.EditorState.Editor.Modified() {
			return fmt.Errorf("real action effect/safety/publication not confirmed: %+v", out)
		}
		if strings.Contains(ansi.Strip(v.View().RenderContent()), `import "fmt"`) {
			return fmt.Errorf("removed import remained in the rendered canonical editor")
		}
		return featureVisualPaint(v, "var actionValue = 1")
	}); err != nil {
		return err
	}
	var out workspace.ReviewedEditResult
	if err := m.check(func(*controlVisualModel) error { out = m.lastResult; return nil }); err != nil {
		return err
	}
	observed, err := m.ws.(shellport.ReviewedEdits).InspectReviewedEdit(m.ctx, out.ID, out.Digest)
	if err != nil || observed != out {
		return fmt.Errorf("original code-action receipt differs: %+v %v", observed, err)
	}
	retained, err := m.retained.LocalHistory.LoadRetainedSnapshot(m.ctx, out.SafetyReference)
	if err != nil || len(retained.Entries) != 1 ||
		retained.Entries["action.go"].Hash != fmt.Sprintf("%x", sha256.Sum256([]byte(codeActionVisualBefore))) {
		return fmt.Errorf("retained code-action safety included other files or lost the exact preimage: %+v %v", retained, err)
	}
	plan, err := m.ws.LocalHistory().PlanRestore(m.ctx, out.SafetySnapshotID, localhistory.SnapshotOptions{})
	if err != nil || len(plan.Changes) != 1 || plan.Changes[0].Path != "action.go" ||
		plan.Changes[0].Target == nil || plan.Changes[0].Target.Hash != fmt.Sprintf("%x", sha256.Sum256([]byte(codeActionVisualBefore))) {
		return fmt.Errorf("code-action safety is not the exact selected preimage: %+v %v", plan, err)
	}
	return m.recordCodeAction(fmt.Sprintf("F4 COMMITTED original receipt=%+v; independently expected import removal and exact selected safety", out))
}

// Preserve bounded actual provider protocol evidence before t.TempDir cleanup.
// This reads only this disposable provider's trace, not a live user's core logs.
func codeActionVisualTrace(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return err.Error()
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, 1<<20))
	if err != nil {
		return err.Error()
	}
	lines := strings.Split(string(data), "\n")
	var out strings.Builder
	for i, line := range lines {
		if strings.Contains(line, "textDocument/codeAction") || strings.Contains(line, "codeAction/resolve") {
			for _, next := range lines[i:min(len(lines), i+4)] {
				fmt.Fprintln(&out, next)
			}
		}
	}
	evidence := out.String()
	if len(evidence) > 16<<10 {
		evidence = evidence[:16<<10] + "\n[protocol evidence clipped at 16 KiB]"
	}
	return evidence
}
