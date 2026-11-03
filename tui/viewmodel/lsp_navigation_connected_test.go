package viewmodel

import (
	"context"
	"crude/internal/fileuri"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/clientstate"
	"crude/core"
	"crude/coreprotocol"
	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/screens/navigation"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
)

// These literal provider answers are deliberately independent of normalization
// and editor coordinate helpers. Only temporary file URIs are substituted.
const navigationConnectedPoint = `[{"uri":$TARGET,"range":{"start":{"line":0,"character":3},"end":{"line":0,"character":3}}}]`
const navigationConnectedRange = `[{"targetUri":$TARGET,"targetRange":{"start":{"line":0,"character":0},"end":{"line":1,"character":11}},"targetSelectionRange":{"start":{"line":0,"character":6},"end":{"line":1,"character":9}},"originSelectionRange":{"start":{"line":2,"character":4},"end":{"line":2,"character":7}}}]`
const navigationConnectedMultiple = `[{"uri":$MAIN,"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":7}}},{"uri":$TARGET,"range":{"start":{"line":0,"character":3},"end":{"line":0,"character":3}}}]`

type navigationConnectedCall struct {
	path, kind         string
	line, col, version int
	includeDeclaration bool
	text               string
}

type navigationConnectedRuntime struct {
	renameConnectedRuntime
	target, external string
	answer           string
	unsupported      bool
	err              error
	navigationCalls  []navigationConnectedCall
	watched          chan struct{}
	targetStart      <-chan struct{}
}

type navigationConnectedClient struct {
	*renameConnectedClient
	runtime     *navigationConnectedRuntime
	startTarget bool
}

func (r *navigationConnectedRuntime) NewClient(spec types.LSPServerSpec, root string, admit func() error) workspace.LSPClient {
	base := r.renameConnectedRuntime.NewClient(spec, root, admit).(*renameConnectedClient)
	return &navigationConnectedClient{renameConnectedClient: base, runtime: r, startTarget: spec.LanguageID == "rust" && r.targetStart != nil}
}

func (c *navigationConnectedClient) Start(ctx context.Context) error {
	if c.startTarget {
		select {
		case <-c.runtime.targetStart:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return c.renameConnectedClient.Start(ctx)
}

func (c *navigationConnectedClient) RequestNavigation(_ context.Context, path, kind string, line, col int, includeDeclaration bool) (types.LSPNavigationResponse, error) {
	r := c.runtime
	r.mu.Lock()
	defer r.mu.Unlock()
	d := c.docs[path]
	r.navigationCalls = append(r.navigationCalls, navigationConnectedCall{
		path: path, kind: kind, line: line, col: col, version: d.version,
		includeDeclaration: includeDeclaration, text: d.text,
	})
	uri := func(path string) string {
		return strconv.Quote(fileuri.FromPath(path))
	}
	raw := strings.NewReplacer("$MAIN", uri(r.main), "$TARGET", uri(r.target), "$EXTERNAL", uri(r.external)).Replace(r.answer)
	return types.LSPNavigationResponse{Kind: kind, WireVersion: d.version, JSON: []byte(raw), Unsupported: r.unsupported}, r.err
}

func (c *navigationConnectedClient) NotifyWatchedFilesChanged(context.Context, string, int) {
	c.runtime.mu.Lock()
	done := c.runtime.watched
	c.runtime.mu.Unlock()
	if done != nil {
		select {
		case done <- struct{}{}:
		default:
		}
	}
}

type navigationConnectedFixture struct {
	runtime *core.Runtime
	shells  []remoteJourneyShell
	ws      *workspace.Workspace
	engine  *navigationConnectedRuntime
	main    *editor.Editor
	peer    *editor.Editor
}

func newNavigationConnectedFixture(t *testing.T, answer, targetText string) navigationConnectedFixture {
	return newNavigationConnectedFixtureTarget(t, answer, targetText, "target.go", nil)
}

func newNavigationConnectedFixtureTarget(t *testing.T, answer, targetText, targetName string, targetStart <-chan struct{}) navigationConnectedFixture {
	t.Helper()
	root := t.TempDir()
	engine := &navigationConnectedRuntime{
		renameConnectedRuntime: renameConnectedRuntime{main: filepath.Join(root, "main.go")},
		target:                 filepath.Join(root, targetName), external: filepath.Join(t.TempDir(), "external.go"), answer: answer,
		targetStart: targetStart,
	}
	for path, text := range map[string]string{
		engine.main: renameConnectedMain, engine.target: targetText,
		filepath.Join(root, "go.mod"): "module journey\n\ngo 1.22\n",
	} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	var owned *workspace.Workspace
	runtime := func() *core.Runtime {
		if targetStart != nil {
			// The core sizes process admission at construction from GOMAXPROCS.
			// This journey needs two simultaneous producers, even on a one-slot
			// host. Restore the actual scheduler before attaching either shell;
			// only the fixture's fixed process capacity needs two slots.
			previous := goruntime.GOMAXPROCS(max(8, goruntime.GOMAXPROCS(0)))
			defer goruntime.GOMAXPROCS(previous)
		}
		return remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
			owned = w
			w.LSPMgr.SetRuntime(engine)
			settings := w.AppDefaults()
			settings.LSP = types.DefaultLSPSettings()
			settings.LSP.AutoDetect, settings.LSP.AutoComplete = false, false
			settings.LSP.Languages = map[string]types.LSPLanguageSettings{"go": {
				Enabled: true, Command: executable, RootMarkers: []string{"go.mod"},
			}}
			if targetStart != nil {
				// LanguageID alone is not a process key: distinct arguments model
				// the real Rust server rather than sharing the Go fixture process.
				settings.LSP.Languages["rust"] = types.LSPLanguageSettings{Enabled: true, Command: executable, Args: []string{"--fixture-rust"}, RootMarkers: []string{"go.mod"}}
			}
			w.SetAppDefaults(settings)
			grantSemanticFixture(t, w)
		})
	}()
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	a, b := liveTestOpen(t, one.app, engine.main), liveTestOpen(t, two.app, engine.main)
	f := navigationConnectedFixture{runtime, []remoteJourneyShell{one, two}, owned, engine, a, b}
	f.synchronize(t, engine.main)
	remoteJourneyUntil(t, f.shells, func() bool {
		_, aReady := a.CurrentInsightSource()
		_, bReady := b.CurrentInsightSource()
		return aReady && bReady && one.app.ws.LSPMgr().ServerReady()
	})
	a.SetCursorPosition(2, 4)
	b.SetCursorPosition(0, 7)
	one.app.Input.SetInput("private navigation first draft")
	two.app.Input.SetInput("private navigation second draft")
	one.app.setFocusEditor()
	two.app.setFocusEditor()
	return f
}

func (f navigationConnectedFixture) synchronize(t *testing.T, path string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, view, err := f.ws.LSPMgr.ProbeHover(ctx, path, types.LSPSourcePosition{Line: 1, Column: 1}); err != nil || view.Source.Kind != "canonical" {
		t.Fatal("navigation fixture canonical synchronization failed", view, err)
	}
}

func navigationConnectedPaint(app *App) string {
	app.advanceReveals(time.Now().Add(2 * revealDuration))
	return ansi.Strip(app.View().RenderContent())
}

func (f navigationConnectedFixture) key(t *testing.T, key tea.KeyPressMsg) {
	t.Helper()
	renameConnectedKey(t, f.shells[0], key)
}

func (f navigationConnectedFixture) open(t *testing.T, key tea.KeyPressMsg) *navigationState {
	t.Helper()
	f.key(t, key)
	one := f.shells[0]
	remoteJourneyUntil(t, f.shells, func() bool {
		return one.app.navigation != nil && one.app.navigation.shown
	})
	return one.app.navigation
}

func (f navigationConnectedFixture) inspect(t *testing.T) {
	t.Helper()
	f.key(t, tea.KeyPressMsg{Code: tea.KeyEnter})
	paint := navigationConnectedPaint(f.shells[0].app)
	for _, text := range []string{"URI (literal, complete)", "targetSelectionRange", "Back", "Close"} {
		if !strings.Contains(paint, text) {
			t.Fatalf("Enter did not paint literal inspect-only metadata %q:\n%s", text, paint)
		}
	}
}

func (f navigationConnectedFixture) explicitOpen(t *testing.T) {
	t.Helper()
	// Detail's initial action is Close. Open is two left-arrow moves away.
	f.key(t, tea.KeyPressMsg{Code: tea.KeyLeft})
	f.key(t, tea.KeyPressMsg{Code: tea.KeyLeft})
	f.key(t, tea.KeyPressMsg{Code: tea.KeyEnter})
	remoteJourneyUntil(t, f.shells, func() bool { return f.shells[0].app.navigation == nil })
}

func TestConnectedNavigationFourKeysAndCodeContextActions(t *testing.T) {
	f := newNavigationConnectedFixture(t, navigationConnectedMultiple, "\t😀name\r\n")
	one := f.shells[0]
	for _, entry := range []struct {
		kind, label string
		code        rune
	}{
		{types.LSPNavigationDefinition, "Go to definition", '.'},
		{types.LSPNavigationReferences, "Find references", ';'},
		{types.LSPNavigationTypeDefinition, "Go to type definition", 't'},
		{types.LSPNavigationImplementation, "Go to implementation", 'm'},
	} {
		t.Run(entry.kind, func(t *testing.T) {
			for _, contextAction := range []bool{false, true} {
				if contextAction {
					menu := one.app.buildEditorContextMenu(tea.Mouse{X: -1, Y: -1})
					found := false
					for _, section := range menu.Sections {
						if section.SectionLabel != "Code" {
							continue
						}
						for _, item := range section.Items {
							if item.Label == entry.label && !item.Disabled && item.Action != nil {
								found = true
								item.Action() // The real callback must actually schedule the command.
							}
						}
					}
					if !found {
						t.Fatalf("Code context menu omitted %q", entry.label)
					}
					remoteJourneyUntil(t, f.shells, func() bool { return one.app.navigation != nil && one.app.navigation.shown })
				} else {
					f.open(t, tea.KeyPressMsg{Code: entry.code, Mod: tea.ModAlt})
				}
				paint := navigationConnectedPaint(one.app)
				if !strings.Contains(paint, entry.label) || !strings.Contains(paint, "Inspect") {
					t.Fatalf("entry did not paint its actual chooser:\n%s", paint)
				}
				if one.app.EditorState.Editor != f.main || f.main.CursorLine() != 2 || f.main.CursorCol() != 4 ||
					one.app.EditorState.Tabs[f.engine.target] != nil {
					t.Fatal("multiple targets silently selected the first destination")
				}
				f.engine.mu.Lock()
				call := f.engine.navigationCalls[len(f.engine.navigationCalls)-1]
				f.engine.mu.Unlock()
				if call.kind != entry.kind || call.path != f.engine.main || call.line != 2 || call.col != 4 ||
					call.version <= 0 || call.text != renameConnectedMain || call.includeDeclaration != (entry.kind == types.LSPNavigationReferences) {
					t.Fatalf("entry lost its exact source/provider invocation: %+v", call)
				}
				f.key(t, tea.KeyPressMsg{Code: tea.KeyEsc})
			}
		})
	}
}

func TestConnectedReferencesInspectCloseBackAndExplicitSecondTarget(t *testing.T) {
	f := newNavigationConnectedFixture(t, navigationConnectedMultiple, "\t😀name\r\n")
	one, two := f.shells[0], f.shells[1]
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		one.app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		f.open(t, tea.KeyPressMsg{Code: ';', Mod: tea.ModAlt})
		f.key(t, tea.KeyPressMsg{Code: tea.KeyDown})
		f.inspect(t)
		f.key(t, tea.KeyPressMsg{Code: tea.KeyEnter}) // Default Close, never Open.
		if one.app.navigation != nil || one.app.EditorState.Tabs[f.engine.target] != nil || f.main.CursorCol() != 4 {
			t.Fatal("detail's default Enter opened a target")
		}
	}
	f.open(t, tea.KeyPressMsg{Code: ';', Mod: tea.ModAlt})
	f.key(t, tea.KeyPressMsg{Code: tea.KeyDown})
	f.inspect(t)
	f.key(t, tea.KeyPressMsg{Code: tea.KeyEsc}) // Explicit Back to original list.
	if one.app.navigation == nil || !one.app.navigation.screen.InList() || !strings.Contains(navigationConnectedPaint(one.app), "Inspect") {
		t.Fatal("Back lost the original chooser")
	}
	f.inspect(t) // Retains selected second row, rather than resetting to first.
	f.explicitOpen(t)
	dest := one.app.EditorState.Editor
	if dest.FilePath() != f.engine.target || dest.CursorLine() != 0 || dest.CursorCol() != 6 || dest.HasSelection() ||
		dest.Value() != "\t😀name\r\n" {
		t.Fatalf("explicit second row did not land once in UTF16 source: %s %d:%d %q", dest.FilePath(), dest.CursorLine(), dest.CursorCol(), dest.Value())
	}
	if two.app.EditorState.Editor != f.peer || f.peer.CursorLine() != 0 || f.peer.CursorCol() != 7 ||
		two.app.focusTarget() != FocusEditor || two.app.navigation != nil || f.main.CursorLine() != 2 || f.main.CursorCol() != 4 {
		t.Fatal("navigation changed the other shell's private focus/cursor or the original source caret")
	}
	if one.app.Input.Value() != "private navigation first draft" || two.app.Input.Value() != "private navigation second draft" {
		t.Fatal("navigation consumed a private composer draft")
	}
	if paint := navigationConnectedPaint(one.app); !strings.Contains(paint, "name") {
		t.Fatalf("opened destination was not actually painted:\n%s", paint)
	}
}

func TestConnectedNavigationExternalListDoesNotReadAndRequiresExplicitOpen(t *testing.T) {
	raw := strings.ReplaceAll(navigationConnectedPoint, "$TARGET", "$EXTERNAL")
	f := newNavigationConnectedFixture(t, raw, "\t😀name\r\n")
	one := f.shells[0]
	s := f.open(t, tea.KeyPressMsg{Code: '.', Mod: tea.ModAlt})
	if len(s.result.Targets) != 1 || !s.result.Targets[0].External ||
		s.result.Targets[0].UnavailableReason != "" || one.app.EditorState.Tabs[f.engine.external] != nil {
		t.Fatal("nonexistent external destination was read, made unavailable, or auto-opened during listing")
	}
	f.inspect(t)
	if paint := navigationConnectedPaint(one.app); !strings.Contains(paint, "Open external file") || !strings.Contains(paint, "outside workspace") {
		t.Fatalf("external detail omitted the explicit decision:\n%s", paint)
	}
	f.key(t, tea.KeyPressMsg{Code: tea.KeyEnter})
	if one.app.navigation != nil || one.app.EditorState.Tabs[f.engine.external] != nil {
		t.Fatal("external detail's default action was not Close")
	}
	// No source existed when listed or inspected. Explicit Open must observe the
	// now-created bytes, not a prefetched or fabricated list-time source.
	if err := os.WriteFile(f.engine.external, []byte("\t😀external\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	f.open(t, tea.KeyPressMsg{Code: '.', Mod: tea.ModAlt})
	f.inspect(t)
	f.explicitOpen(t)
	ed := one.app.EditorState.Editor
	if ed.FilePath() != f.engine.external || ed.Value() != "\t😀external\r\n" || ed.CursorCol() != 6 {
		t.Fatal("explicit external Open did not observe and render the selected source", ed.FilePath(), ed.Value(), ed.CursorCol())
	}
}

func TestConnectedNavigationCanonicalUnsavedUTF16RangeWinsOverDisk(t *testing.T) {
	text := "\t界é😀abc\r\nab\t界é😀XYZ\r\n"
	// References use standard Location[], not LocationLink[]. The source range is
	// intentionally multiline, tabbed and Unicode, independent of any helper.
	const raw = `[{"uri":$TARGET,"range":{"start":{"line":0,"character":6},"end":{"line":1,"character":9}}}]`
	f := newNavigationConnectedFixture(t, raw, text)
	one, two := f.shells[0], f.shells[1]
	target := liveTestOpen(t, two.app, f.engine.target)
	target.SetCursorPosition(1, 12)
	renameConnectedKey(t, two, tea.KeyPressMsg{Code: '!', Text: "!"})
	remoteJourneyUntil(t, f.shells, func() bool {
		_, ready := target.CurrentInsightSource()
		return ready && target.Modified() && strings.Contains(target.Value(), "XYZ!")
	})
	f.synchronize(t, f.engine.main)
	f.open(t, tea.KeyPressMsg{Code: ';', Mod: tea.ModAlt}) // A single reference still requires a chooser.
	if one.app.EditorState.Editor != f.main || one.app.EditorState.Tabs[f.engine.target] != nil {
		t.Fatal("single reference auto-jumped")
	}
	f.inspect(t)
	f.explicitOpen(t)
	ed := one.app.EditorState.Editor
	if ed.FilePath() != f.engine.target || ed.Value() != "\t界é😀abc\r\nab\t界é😀XYZ!\r\n" ||
		ed.CursorLine() != 0 || ed.CursorCol() != 9 || ed.HasSelection() {
		t.Fatalf("canonical UTF16 landing/selection wrong: %s %d:%d selected=%q source=%q", ed.FilePath(), ed.CursorLine(), ed.CursorCol(), ed.SelectedText(), ed.Value())
	}
	disk, err := os.ReadFile(f.engine.target)
	if err != nil || string(disk) != text || !ed.Modified() || target.HasSelection() {
		t.Fatal("navigation saved/replaced canonical dirty bytes or shared a private selection", string(disk), err)
	}
	if paint := navigationConnectedPaint(one.app); !strings.Contains(paint, "XYZ!") {
		t.Fatalf("canonical unsaved target was not visibly rendered:\n%s", paint)
	}
}

func TestConnectedNavigationLocationLinkRetainsRangesAndOrigin(t *testing.T) {
	text := "\t界é😀abc\r\nab\t界é😀XYZ\r\n"
	// Type-definition accepts homogeneous links, unlike references. Two rows
	// force an explicit choice rather than the internal single-target shortcut.
	raw := strings.TrimSuffix(navigationConnectedRange, "]") + "," + strings.TrimPrefix(navigationConnectedRange, "[")
	f := newNavigationConnectedFixture(t, raw, text)
	s := f.open(t, tea.KeyPressMsg{Code: 't', Mod: tea.ModAlt})
	target := s.result.Targets[0]
	if !target.Link || target.Range.Start.Line != 0 || target.Range.End.Character != 11 ||
		target.Selection.Start.Character != 6 || target.Selection.End.Character != 9 ||
		target.Origin == nil || target.Origin.Start.Line != 2 || target.Origin.Start.Character != 4 {
		t.Fatalf("connected LocationLink lost literal source/target ranges: %+v", target)
	}
	f.inspect(t)
	f.explicitOpen(t)
	ed := f.shells[0].app.EditorState.Editor
	if ed.FilePath() != f.engine.target || ed.CursorCol() != 9 || ed.HasSelection() || ed.Value() != text {
		t.Fatal("LocationLink selection failed to land without creating an editable selection")
	}
}

func TestConnectedNavigationControlOpenAwaitsActualLanding(t *testing.T) {
	f := newNavigationConnectedFixture(t, navigationConnectedMultiple, "\t😀name\r\n")
	one := f.shells[0]
	f.open(t, tea.KeyPressMsg{Code: ';', Mod: tea.ModAlt})
	f.key(t, tea.KeyPressMsg{Code: tea.KeyDown})
	inspected := journeySettingsControl(t, f.runtime, f.shells, one, "navigation/list", ui.ControlOperation{Verb: "activate", ItemID: "1"})
	if inspected.State != control.Applied || one.app.navigation == nil || one.app.navigation.screen.InList() ||
		one.app.EditorState.Tabs[f.engine.target] != nil {
		t.Fatalf("metadata inspect read source or failed to mount its exact detail: %+v", inspected)
	}
	opened := journeySettingsControl(t, f.runtime, f.shells, one, "navigation/actions/open", ui.ControlOperation{Verb: "activate"})
	ed := one.app.EditorState.Editor
	if opened.State != control.Applied || len(opened.ResourceIDs) != 2 || one.app.navigation != nil ||
		ed.FilePath() != f.engine.target || ed.CursorCol() != 6 || ed.HasSelection() {
		t.Fatalf("Open acknowledged an action message rather than the actual selected document landing: %+v, %s %d", opened, ed.FilePath(), ed.CursorCol())
	}
	// A provider-invalid target must not turn its selected-target error into a
	// successful local control receipt or install a destination.
	f = newNavigationConnectedFixture(t, `[{"uri":$TARGET,"range":{"start":{"line":99,"character":0},"end":{"line":99,"character":0}}}]`, "\t😀name\r\n")
	one = f.shells[0]
	f.open(t, tea.KeyPressMsg{Code: ';', Mod: tea.ModAlt})
	f.inspect(t)
	refused := journeySettingsControl(t, f.runtime, f.shells, one, "navigation/actions/open", ui.ControlOperation{Verb: "activate"})
	if refused.State != control.Failed || one.app.EditorState.Tabs[f.engine.target] != nil ||
		one.app.EditorState.Editor != f.main || !strings.Contains(navigationConnectedPaint(one.app), "Navigation failed") {
		t.Fatalf("invalid target received successful Open: %+v", refused)
	}
}

type navigationControlOutcome struct {
	result control.Result
	err    error
}

// Start the real connected control request, but retain its prepared pane-open
// completion so another genuine LSP publication can arrive before Update.
func holdNavigationControlOpen(t *testing.T, f navigationConnectedFixture) (shellControlDoneMsg, <-chan navigationControlOutcome) {
	t.Helper()
	one := f.shells[0]
	one.app.View()
	cursor, componentID := "", ""
	for {
		result := requestShellForTest(t, f.runtime, f.shells, one, "shell.controls", control.PageRequest{Limit: 128, Cursor: cursor}, nil)
		if result.State != control.Applied {
			t.Fatalf("navigation control inventory: %+v", result)
		}
		var page control.Page
		if err := json.Unmarshal(result.Output, &page); err != nil {
			t.Fatal(err)
		}
		for _, node := range page.Nodes {
			if strings.HasSuffix(node.ID, "::navigation/actions/open") {
				componentID = node.ID
			}
		}
		if page.NextCursor == "" {
			break
		}
		cursor = page.NextCursor
	}
	if componentID == "" {
		t.Fatal("navigation Open control is not mounted")
	}
	intent := shellControlIntent{ComponentID: componentID, Operation: ui.ControlOperation{Verb: "activate"}}
	described := requestShellForTest(t, f.runtime, f.shells, one, "shell.describe_control", intent, nil)
	if described.State != control.Applied {
		t.Fatalf("navigation Open description: %+v", described)
	}
	var details struct {
		Descriptor control.Descriptor `json:"descriptor"`
		Node       control.Node       `json:"node"`
	}
	if err := json.Unmarshal(described.Output, &details); err != nil {
		t.Fatal(err)
	}
	intent.ExpectedAction = details.Descriptor.ID
	raw, err := json.Marshal(intent)
	if err != nil {
		t.Fatal(err)
	}
	port := one.app.ws.(shellport.ShellControl)
	identity := port.ControlIdentity()
	identity.ResourceID = componentID
	id := fmt.Sprintf("navigation-held-%d", shellControlTestSequence.Add(1))
	envelope := coreprotocol.ControlEnvelope{Descriptor: details.Descriptor, Request: control.Request{
		ID: id, CommandID: "shell.control", Target: identity,
		Caller: control.Caller{Kind: control.ExternalCaller, ClientID: "test-controller"},
		Input:  raw, Preconditions: control.Preconditions{Revision: &details.Node.Revision, OpenEpoch: &details.Node.OpenEpoch},
	}}
	envelope.Descriptor.ID = "shell.control"
	ctx, cancel := context.WithTimeout(t.Context(), 60*time.Second)
	t.Cleanup(cancel)
	outcome := make(chan navigationControlOutcome, 1)
	go func() {
		result, err := f.runtime.RequestShellControl(ctx, identity.WorkspaceID, *identity.Shell, envelope)
		outcome <- navigationControlOutcome{result, err}
	}()

	type commandResult struct {
		app *App
		msg tea.Msg
	}
	results := make(chan commandResult, 64)
	pending, launched := 0, 0
	launch := func(app *App, cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		launched++
		if launched > cap(results) {
			t.Fatal("held control journey did not settle")
		}
		pending++
		go func() { results <- commandResult{app, cmd()} }()
	}
	var held shellControlDoneMsg
	found := false
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for !found || pending != 0 {
		var app *App
		var msg tea.Msg
		select {
		case msg = <-f.shells[0].fifo:
			app = f.shells[0].app
		case msg = <-f.shells[1].fifo:
			app = f.shells[1].app
		case done := <-results:
			pending--
			app, msg = done.app, done.msg
		case premature := <-outcome:
			t.Fatalf("control settled before its prepared pane open: %+v %v", premature.result, premature.err)
		case <-deadline.C:
			t.Fatal("control did not prepare its selected target")
		case <-ctx.Done():
			t.Fatal("control preparation cancelled:", ctx.Err())
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, cmd := range batch {
				launch(app, cmd)
			}
			continue
		}
		if completion, ok := msg.(shellControlDoneMsg); ok && completion.request.Envelope.Request.ID == id {
			switch value := completion.completion.(type) {
			case navigation.OpenMsg:
			case navigationTargetMsg:
				if value.err != nil {
					t.Fatal(value.err)
				}
			case paneFilesOpenedMsg:
				if len(value.files) != 1 || value.files[0].err != nil || value.files[0].snapshot == nil {
					t.Fatalf("target preparation failed: %+v", value.files)
				}
				held, found = completion, true
				continue
			default:
				t.Fatalf("unexpected control preparation stage %T", value)
			}
		}
		if msg != nil {
			_, cmd := app.Update(msg)
			launch(app, cmd)
		}
	}
	return held, outcome
}

func TestConnectedNavigationLandingTracksSourceProducerNotDestinationReadiness(t *testing.T) {
	for _, stopSource := range []bool{false, true} {
		name := "destination-starting"
		if stopSource {
			name = "source-stopped"
		}
		t.Run(name, func(t *testing.T) {
			gate := make(chan struct{})
			defer close(gate)
			scheduler := goruntime.GOMAXPROCS(0)
			f := newNavigationConnectedFixtureTarget(t, navigationConnectedPoint, "\t😀name\r\n", "target.rs", gate)
			if got := goruntime.GOMAXPROCS(0); got != scheduler {
				t.Fatalf("fixture changed navigation scheduling: GOMAXPROCS=%d, want %d", got, scheduler)
			}
			one := f.shells[0]
			s := f.open(t, tea.KeyPressMsg{Code: ';', Mod: tea.ModAlt})
			f.inspect(t)
			instance := s.result.Boundary.Producer.InstanceID
			sourceID := ""
			for _, health := range one.app.ws.LSPMgr().ServerHealthSnapshots() {
				if health.InstanceID == instance && health.Connected {
					sourceID = health.ID
				}
			}
			if sourceID == "" {
				t.Fatal("original source producer is not connected")
			}
			held, outcome := holdNavigationControlOpen(t, f)
			// Wait in product order: target start is admitted by Open, then
			// its active-file readiness is published to the invoking shell.
			remoteJourneyUntil(t, f.shells, func() bool {
				for _, health := range one.app.ws.LSPMgr().ServerHealthSnapshots() {
					if health.Status == "Failed" || health.Status == "Waiting for slot" {
						t.Fatalf("server failed before held navigation landing: %+v", health)
					}
					if health.Starting && health.InstanceID != "" && health.InstanceID != instance {
						return true
					}
				}
				return false
			})
			remoteJourneyUntil(t, f.shells, func() bool { return !one.app.ws.LSPMgr().ServerReady() })
			if stopSource {
				if !one.app.ws.LSPMgr().StopServer(sourceID) {
					t.Fatal("source server Stop was not admitted")
				}
				remoteJourneyUntil(t, f.shells, func() bool {
					for _, health := range one.app.ws.LSPMgr().ServerHealthSnapshots() {
						if health.ID == sourceID {
							return !health.Connected && health.InstanceID == ""
						}
					}
					return false
				})
			}
			_, cmd := one.app.Update(held)
			runAppCommand(t, one.app, cmd)
			var result navigationControlOutcome
			select {
			case result = <-outcome:
			case <-time.After(15 * time.Second):
				t.Fatal("held navigation control did not produce its original receipt")
			case <-t.Context().Done():
				t.Fatal(t.Context().Err())
			}
			if result.err != nil {
				t.Fatal(result.err)
			}
			paint := navigationConnectedPaint(one.app)
			if stopSource {
				if result.result.State != control.Failed || result.result.Fault == nil || result.result.Fault.Code != control.Stale ||
					one.app.navigation != nil || one.app.EditorState.Tabs[f.engine.target] != nil ||
					one.app.EditorState.Editor != f.main || f.main.CursorLine() != 2 || f.main.CursorCol() != 4 ||
					!strings.Contains(paint, "var old = 1") {
					t.Fatalf("stopped source producer installed a destination or disturbed the original view: %+v", result.result)
				}
			} else {
				ed := one.app.EditorState.Editor
				if result.result.State != control.Applied || len(result.result.ResourceIDs) != 2 ||
					one.app.navigation != nil || ed == f.main || ed.FilePath() != f.engine.target ||
					ed.Value() != "\t😀name\r\n" || ed.CursorCol() != 6 || ed.HasSelection() ||
					!strings.Contains(paint, "name") {
					t.Fatalf("starting destination server prevented the verified target from landing: %+v", result.result)
				}
			}
			if one.app.Input.Value() != "private navigation first draft" {
				t.Fatal("navigation consumed the private composer draft")
			}
		})
	}
}

func TestConnectedNavigationNullEmptyUnsupportedAndErrorRemainDistinct(t *testing.T) {
	f := newNavigationConnectedFixture(t, "null", "\t😀name\r\n")
	one := f.shells[0]
	for _, tc := range []struct {
		name, raw, marker string
		unsupported       bool
		err               error
	}{
		{"null", "null", "Provider returned null.", false, nil},
		{"empty", "[]", "Provider returned an empty array [].", false, nil},
		{"unsupported", "", "Navigation is unsupported", true, nil},
		{"error", "", "Literal connected navigation failure", false, errors.New("Literal connected navigation failure")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.engine.mu.Lock()
			f.engine.answer, f.engine.unsupported, f.engine.err = tc.raw, tc.unsupported, tc.err
			f.engine.mu.Unlock()
			f.key(t, tea.KeyPressMsg{Code: ';', Mod: tea.ModAlt})
			remoteJourneyUntil(t, f.shells, func() bool {
				return strings.Contains(navigationConnectedPaint(one.app), tc.marker)
			})
			paint := navigationConnectedPaint(one.app)
			if one.app.EditorState.Editor != f.main || one.app.EditorState.Tabs[f.engine.target] != nil ||
				strings.Contains(paint, "Open external file") || strings.Contains(paint, "[ Open ]") {
				t.Fatalf("negative result retained actionable stale destinations:\n%s", paint)
			}
			if tc.err == nil && !strings.Contains(paint, "not proof") {
				t.Fatalf("negative provider result became a cleanliness claim:\n%s", paint)
			}
			f.key(t, tea.KeyPressMsg{Code: tea.KeyEnter}) // Close is safe for each negative result.
			if one.app.navigation != nil {
				t.Fatal("negative-result Close did not retire its exact invocation")
			}
		})
	}
}

// Capture a genuine remote/core result without delivering it to Update. This is
// carrier fault injection, not a synthesized boundary or fake canonical source.
func (f navigationConnectedFixture) captureResult(t *testing.T, holdReplica ...*[]tea.Msg) navigationResultMsg {
	t.Helper()
	_, cmd := f.shells[0].app.Update(tea.KeyPressMsg{Code: ';', Mod: tea.ModAlt})
	if cmd == nil || f.shells[0].app.navigation == nil {
		t.Fatal("actual reference key did not launch a qualified command")
	}
	msg := captureNavigationCommand[navigationResultMsg](t, f.shells[0], cmd, holdReplica...)
	if msg.err != nil || msg.result.Boundary.Source.DocumentID == "" {
		t.Fatalf("could not capture real qualified navigation result: %T %+v", msg, msg)
	}
	return msg
}

// Like runAppCommand, this drives real Update/publication acknowledgements while
// commands execute off Update. It withholds only the selected result carrier.
func captureNavigationCommand[T any](t *testing.T, shell remoteJourneyShell, command tea.Cmd, holdReplica ...*[]tea.Msg) T {
	t.Helper()
	results := make(chan tea.Msg, 64)
	pending, launched := 0, 0
	var captured T
	found := false
	launch := func(cmd tea.Cmd) {
		if cmd != nil {
			launched++
			if launched > cap(results) {
				t.Fatal("qualified navigation capture did not settle")
			}
			pending++
			go func() { results <- cmd() }()
		}
	}
	launch(command)
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	for pending > 0 {
		var msg tea.Msg
		select {
		case msg = <-results:
			pending--
		case msg = <-shell.fifo:
		case <-deadline.C:
			t.Fatal("qualified navigation command did not produce its expected carrier")
		}
		if result, ok := msg.(T); ok {
			captured, found = result, true
			continue
		}
		if _, ok := msg.(shellport.ReplicaChangesMsg); ok && len(holdReplica) > 0 {
			*holdReplica[0] = append(*holdReplica[0], msg)
			continue
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			for _, cmd := range batch {
				launch(cmd)
			}
			continue
		}
		if msg != nil {
			_, next := shell.app.Update(msg)
			launch(next)
		}
	}
	if found {
		return captured
	}
	t.Fatal("qualified navigation command settled without its expected carrier")
	var zero T
	return zero
}

func (f navigationConnectedFixture) retire(t *testing.T, reason string) {
	t.Helper()
	one, two := f.shells[0], f.shells[1]
	switch reason {
	case "caret":
		f.main.SetCursorPosition(2, 5)
	case "peer-edit", "equal-text-undo":
		renameConnectedKey(t, two, tea.KeyPressMsg{Code: 'X', Text: "X"})
		remoteJourneyUntil(t, f.shells, func() bool { return f.main.Value() == "packageX journey\n\nvar old = 1\n" })
		if reason == "equal-text-undo" {
			renameConnectedKey(t, two, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			remoteJourneyUntil(t, f.shells, func() bool { return f.main.Value() == renameConnectedMain })
		}
	case "focus":
		one.app.setFocusInput()
	case "dock":
		one.app.termDock.ShowPanel(termdock.PanelNotices)
		one.app.setTermDockFocused(true)
	case "close-reopen":
		one.app.closeEditorTab()
		runAppCommand(t, one.app, one.app.drainLiveEditorCommands())
		liveTestOpen(t, one.app, f.engine.main)
		one.app.setFocusEditor()
	case "workspace":
		one.app.workspaceSwitchGen.Add(1)
		one.app.resetLSPRequestContext()
	default:
		t.Fatal("unknown retirement reason", reason)
	}
}

func TestConnectedNavigationLateResultAndPaneOpenCannotStealRetiredSource(t *testing.T) {
	for _, stage := range []string{"result", "pane-open"} {
		for _, reason := range []string{"caret", "peer-edit", "equal-text-undo", "focus", "dock", "close-reopen", "workspace"} {
			t.Run(fmt.Sprintf("%s/%s", stage, reason), func(t *testing.T) {
				f := newNavigationConnectedFixture(t, navigationConnectedPoint, "\t😀name\r\n")
				one := f.shells[0]
				result := f.captureResult(t)
				var opened paneFilesOpenedMsg
				if stage == "pane-open" {
					_, cmd := one.app.Update(result)
					runAppCommand(t, one.app, cmd)
					// Enter has only inspected metadata. Capture the exact explicit
					// Open command's observation and completed asynchronous pane read.
					f.inspect(t)
					f.key(t, tea.KeyPressMsg{Code: tea.KeyLeft})
					f.key(t, tea.KeyPressMsg{Code: tea.KeyLeft})
					_, inspect := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
					target := captureNavigationCommand[navigationTargetMsg](t, one, inspect)
					if target.err != nil {
						t.Fatal(target.err)
					}
					_, open := one.app.Update(target)
					opened = captureNavigationCommand[paneFilesOpenedMsg](t, one, open)
				}
				f.retire(t, reason)
				current := one.app.EditorState.Editor
				beforeLine, beforeCol, beforeText := current.CursorLine(), current.CursorCol(), current.Value()
				if stage == "result" {
					_, cmd := one.app.Update(result)
					runAppCommand(t, one.app, cmd)
				} else {
					_, cmd := one.app.Update(opened)
					runAppCommand(t, one.app, cmd)
				}
				if one.app.EditorState.Editor != current || current.CursorLine() != beforeLine || current.CursorCol() != beforeCol ||
					current.Value() != beforeText || one.app.EditorState.Tabs[f.engine.target] != nil || one.app.navigation != nil {
					t.Fatal("late carrier opened a tab, moved source, or revived retired navigation")
				}
				if reason == "focus" && one.app.focusTarget() == FocusEditor || reason == "dock" && !one.app.termDockOwnsKeys() {
					t.Fatal("late carrier stole private destination focus")
				}
			})
		}
	}
}

func TestConnectedNavigationFreshInvocationRejectsOldIdenticalSourceReply(t *testing.T) {
	f := newNavigationConnectedFixture(t, navigationConnectedPoint, "\t😀name\r\n")
	old := f.captureResult(t)
	f.key(t, tea.KeyPressMsg{Code: tea.KeyEsc})
	current := f.open(t, tea.KeyPressMsg{Code: ';', Mod: tea.ModAlt})
	one := f.shells[0]
	_, cmd := one.app.Update(old)
	runAppCommand(t, one.app, cmd)
	if one.app.navigation != current || current == old.owner || !current.screen.InList() ||
		one.app.EditorState.Editor != f.main || one.app.EditorState.Tabs[f.engine.target] != nil {
		t.Fatal("a late identical-source result acquired a fresh private invocation")
	}
}

func TestConnectedNavigationWaitsForPendingSourceButPermitsAcknowledgedDirtySource(t *testing.T) {
	f := newNavigationConnectedFixture(t, navigationConnectedPoint, "\t😀name\r\n")
	one := f.shells[0]
	f.main.InsertRune('X') // Deliberately not submitted: no canonical source authority.
	f.key(t, tea.KeyPressMsg{Code: ';', Mod: tea.ModAlt})
	f.engine.mu.Lock()
	calls := len(f.engine.navigationCalls)
	f.engine.mu.Unlock()
	if calls != 0 || one.app.navigation != nil {
		t.Fatal("pending optimistic text reached the provider")
	}
	runAppCommand(t, one.app, one.app.drainLiveEditorCommands())
	remoteJourneyUntil(t, f.shells, func() bool {
		_, ready := f.main.CurrentInsightSource()
		return ready && f.main.Value() == "package journey\n\nvar Xold = 1\n"
	})
	f.synchronize(t, f.engine.main)
	f.open(t, tea.KeyPressMsg{Code: ';', Mod: tea.ModAlt})
	f.engine.mu.Lock()
	call := f.engine.navigationCalls[len(f.engine.navigationCalls)-1]
	f.engine.mu.Unlock()
	if call.text != "package journey\n\nvar Xold = 1\n" || call.line != 2 || call.col != 5 || !f.main.Modified() {
		t.Fatal("acknowledged dirty source was refused or replaced by disk source", call)
	}
	disk, err := os.ReadFile(f.engine.main)
	if err != nil || string(disk) != renameConnectedMain {
		t.Fatal("navigation silently saved acknowledged dirty source", string(disk), err)
	}
}

func TestConnectedNavigationInvalidSurrogateTargetNeverOpensOrClamps(t *testing.T) {
	const raw = `[{"uri":$TARGET,"range":{"start":{"line":0,"character":2},"end":{"line":0,"character":2}}}]`
	f := newNavigationConnectedFixture(t, raw, "\t😀name\r\n")
	one := f.shells[0]
	f.open(t, tea.KeyPressMsg{Code: ';', Mod: tea.ModAlt})
	f.inspect(t)
	f.key(t, tea.KeyPressMsg{Code: tea.KeyLeft})
	f.key(t, tea.KeyPressMsg{Code: tea.KeyLeft})
	f.key(t, tea.KeyPressMsg{Code: tea.KeyEnter})
	remoteJourneyUntil(t, f.shells, func() bool {
		return strings.Contains(navigationConnectedPaint(one.app), "Navigation failed")
	})
	if one.app.EditorState.Tabs[f.engine.target] != nil || one.app.EditorState.Editor != f.main ||
		f.main.CursorLine() != 2 || f.main.CursorCol() != 4 {
		t.Fatal("invalid surrogate-half target was clamped or opened before strict source validation")
	}
}

func TestConnectedNavigationPrivateTargetRecoveryDoesNotInstallOrMove(t *testing.T) {
	f := newNavigationConnectedFixture(t, navigationConnectedPoint, "\t😀name\r\n")
	one, two := f.shells[0], f.shells[1]
	store, err := clientstate.Open(filepath.Join(t.TempDir(), "navigation-private.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	attachment, err := store.Acquire(t.Context(), "navigation-recovery", clientstate.AcquireOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := one.app.ConfigureComposerDrafts(store, attachment, "navigation-workspace"); err != nil {
		t.Fatal(err)
	}
	// Persist a genuine unresolved private target draft. The target is not open
	// in the invoking shell, so ordinary pane-open would restore it on install.
	peer := liveTestOpen(t, two.app, f.engine.target)
	draft := editor.New(one.app.Theme)
	t.Cleanup(draft.Close)
	if err := draft.InstallDocument(peer.DocumentSnapshot()); err != nil {
		t.Fatal(err)
	}
	draft.InsertRune('X')
	recovery, unresolved := draft.ExportDocumentRecovery()
	if !unresolved {
		t.Fatal("fixture did not create an unresolved private draft")
	}
	payload, err := json.Marshal(recovery)
	if err != nil {
		t.Fatal(err)
	}
	key := clientstate.EditorRecoveryKey{ClientID: attachment.ClientID, WorkspaceID: "navigation-workspace", Path: f.engine.target}
	record, err := store.SaveEditorRecovery(t.Context(), attachment, key, 0, payload)
	if err != nil {
		t.Fatal(err)
	}
	f.synchronize(t, f.engine.main)
	_, contextGeneration := f.ws.LSPMgr.ContextCursor()
	remoteJourneyUntil(t, f.shells, func() bool {
		_, generation := one.app.ws.LSPMgr().ContextCursor()
		_, current := f.main.CurrentInsightSource()
		return generation == contextGeneration && current && one.app.ws.LSPMgr().ServerReady()
	})
	f.open(t, tea.KeyPressMsg{Code: ';', Mod: tea.ModAlt})
	f.inspect(t)
	f.explicitOpen(t)
	if one.app.EditorState.Tabs[f.engine.target] != nil || one.app.EditorState.Editor != f.main ||
		f.main.CursorLine() != 2 || f.main.CursorCol() != 4 {
		t.Fatal("navigation installed a target with unresolved private recovery before validating the landing")
	}
	current, err := store.LoadEditorRecovery(t.Context(), attachment, key)
	if err != nil || current.Revision != record.Revision || string(current.Payload) != string(payload) {
		t.Fatal("navigation consumed or changed the private target recovery", err)
	}
}

func TestConnectedNavigationOriginalContextPublicationBarrierIncludesNull(t *testing.T) {
	f := newNavigationConnectedFixture(t, "null", "\t😀name\r\n")
	one := f.shells[0]
	_, oldContext := one.app.ws.LSPMgr().ContextCursor()
	observed := make(chan struct{}, 1)
	f.engine.mu.Lock()
	f.engine.watched = observed
	f.engine.mu.Unlock()
	// A real connected mutation advances the core context, but do not install
	// its publication yet. The provider callback is an explicit completion
	// signal, not a guessed debounce or a mutation of the owned workspace.
	one.app.ws.LSPMgr().NotifyWatchedFilesChanged(t.Context(), filepath.Join(one.app.ws.RootPath(), "go.mod"), 2)
	select {
	case <-observed:
	case <-time.After(5 * time.Second):
		t.Fatal("core did not deliver the watched-input notification to its provider")
	}
	f.synchronize(t, f.engine.main)
	var held []tea.Msg
	result := f.captureResult(t, &held)
	if result.result.Boundary.Context.Generation <= oldContext {
		t.Fatal("fixture failed to create a genuinely newer qualified context")
	}
	_, installed := one.app.ws.LSPMgr().ContextCursor()
	if installed != oldContext {
		t.Fatal("fixture installed the publication before the result")
	}
	_, deferredCmd := one.app.Update(result)
	// Do not pump queued publications until after asserting the rendered result.
	paint := navigationConnectedPaint(one.app)
	if one.app.navigation == nil || one.app.navigation.shown || strings.Contains(paint, "Provider returned null.") ||
		!strings.Contains(paint, "Requesting navigation") {
		t.Fatalf("null evidence became visible before its original context publication:\n%s", paint)
	}
	for _, msg := range held {
		_, cmd := one.app.Update(msg)
		runAppCommand(t, one.app, cmd)
	}
	runAppCommand(t, one.app, deferredCmd)
	remoteJourneyUntil(t, f.shells, func() bool {
		return one.app.navigation != nil && one.app.navigation.shown &&
			strings.Contains(navigationConnectedPaint(one.app), "Provider returned null.")
	})
	one.app.ws.LSPMgr().NotifyWatchedFilesChanged(t.Context(), filepath.Join(one.app.ws.RootPath(), "go.mod"), 2)
	remoteJourneyUntil(t, f.shells, func() bool { return one.app.navigation == nil })
	if strings.Contains(navigationConnectedPaint(one.app), "Provider returned null.") {
		t.Fatal("negative evidence outlived the exact context in which it was observed")
	}
}
