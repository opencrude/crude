package viewmodel

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/document"
	"crude/shellport"
	"crude/snippet"
	"crude/tui/screens/completion"
	"crude/tui/screens/shellchat"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
)

// Only the completion reader is fake. The mounted editor, registry routing,
// native chooser/documentation controls and asynchronous result handlers are real.
// This fixture does not claim canonical service admission or real-provider proof;
// those belong to workspace and connected gopls regressions.
type completionTestService struct {
	shellport.LSPService
	context           uint64
	owner             string
	feature           uint64
	enabled           bool
	settings          *types.LSPSettings
	aggregateNotReady bool
	assistance        *types.LSPAssistanceCapabilities
}

func (s *completionTestService) ServerReady() bool                { return !s.aggregateNotReady }
func (s *completionTestService) ContextCursor() (string, uint64)  { return s.owner, s.context }
func (s *completionTestService) CompletionsState() (bool, uint64) { return s.enabled, s.feature }
func (s *completionTestService) Settings() types.LSPSettings {
	if s.settings != nil {
		return types.CloneLSPSettings(*s.settings)
	}
	settings := types.DefaultLSPSettings()
	settings.AutoComplete = true
	return settings
}

type completionTestRequest struct {
	trigger   types.LSPCompletionContext
	source    types.InsightSource
	view      string
	sequence  uint64
	line, col int
}
type completionTestInspection struct {
	id, digest, item string
	resolve          bool
}
type completionTestRelease struct {
	view     string
	sequence uint64
}
type completionTestPort struct {
	shellport.Workspace
	service                *completionTestService
	set                    types.LSPCompletionSet
	docs                   map[string]types.LSPCompletionDocumentation
	requestErr, inspectErr error
	mu                     sync.Mutex
	requests               []completionTestRequest
	inspections            []completionTestInspection
	released               chan completionTestRelease
}

func (p *completionTestPort) LSPMgr() shellport.LSPService { return p.service }
func (p *completionTestPort) RequestLSPCompletions(_ context.Context, source types.InsightSource, view string, sequence uint64, line, col int, trigger types.LSPCompletionContext) (types.LSPCompletionSet, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, completionTestRequest{trigger, source, view, sequence, line, col})
	if p.requestErr != nil {
		return types.LSPCompletionSet{}, p.requestErr
	}
	set := p.set
	// The invocation sequence is assigned by the requesting view, not a provider.
	// Source, context, wire version and returned row identities are independent
	// literal fixture evidence and deliberately are NOT echoed from the request.
	set.Sequence = sequence
	return set, nil
}
func (p *completionTestPort) InspectLSPCompletion(_ context.Context, id, digest, item string, resolve bool) (types.LSPCompletionInspection, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.inspections = append(p.inspections, completionTestInspection{id, digest, item, resolve})
	return types.LSPCompletionInspection{
		SetID: "original-set", Digest: strings.Repeat("d", 64), ItemID: item,
		Boundary: p.set.Boundary, WireVersion: 9, FeatureGeneration: 3,
		Documentation: p.docs[item], ResolveState: "resolved",
	}, p.inspectErr
}
func (p *completionTestPort) ReleaseLSPCompletions(_ context.Context, view string, sequence uint64) error {
	p.released <- completionTestRelease{view, sequence}
	return nil
}
func (p *completionTestPort) reads() []completionTestInspection {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]completionTestInspection(nil), p.inspections...)
}

func completionTestApp(t *testing.T, width, height int, text string) (*App, *completionTestPort) {
	t.Helper()
	app, ws := newRealShellApp(t)
	path := filepath.Join(ws.RootPath, "completion.go")
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := app.openEditorTab(path); err != nil {
		t.Fatal(err)
	}
	snapshot := document.Snapshot{ID: "completion-document", Path: path, Revision: 41,
		Text: []byte(text), Saved: []byte(text), DiskExists: true, DiskHash: sha256.Sum256([]byte(text)),
		AnalysisGeneration: "completion-owner", AnalysisLifetime: "completion-open"}
	ed := app.EditorState.Editor
	if err := ed.InstallDocument(snapshot); err != nil {
		t.Fatal(err)
	}
	ed.SetCursorPosition(0, len(text))
	source := types.InsightSource{WorkspaceGeneration: "completion-owner", DocumentID: "completion-document",
		Path: path, Lifetime: "completion-open", Revision: 41, Kind: "canonical"}
	if actual, current := ed.CurrentInsightSource(); !current || actual != source {
		t.Fatalf("fixture did not install its acknowledged immutable canonical source: %+v current=%t", actual, current)
	}
	original := app.ws // retain cleanup authority before wrapping the attachment
	port := &completionTestPort{Workspace: original,
		released: make(chan completionTestRelease, 32),
		service:  &completionTestService{LSPService: original.LSPMgr(), owner: "completion-owner", context: 7, feature: 3, enabled: true},
		set: types.LSPCompletionSet{ID: "original-set", Digest: strings.Repeat("d", 64), ViewID: "editor-completion",
			Boundary: types.InsightBoundary{Source: source,
				Context:  types.InsightContext{ID: "original-context", Generation: 7, UnknownInputs: []string{"build flags unknown"}},
				Producer: types.InsightProducer{InstanceID: "original-instance", Provider: "literal-provider"}},
			WireVersion: 9, FeatureGeneration: 3, Line: 0, Col: len(text),
			Answer: types.LSPCompletionItems, ExpiresAt: time.Now().Add(time.Minute),
			Entries: []types.LSPCompletionEntry{
				{ID: "original:0", Index: 0, Insertion: types.CompletionItem{Label: "duplicate", InsertText: "first"},
					Documentation: types.LSPCompletionDocumentation{Present: true, Form: "string", Value: "FIRST_SELECTED_DOC"}},
				{ID: "original:1", Index: 1, Insertion: types.CompletionItem{Label: "duplicate", InsertText: "second"},
					Documentation: types.LSPCompletionDocumentation{Present: true, Form: "markup", Kind: "markdown", Value: "**SECOND_SELECTED_DOC**"}},
			}},
		docs: map[string]types.LSPCompletionDocumentation{
			"original:0": {HasDetail: true, Detail: "resolved first detail", Present: true, Form: "string", Value: "FIRST_RESOLVED_DOC"},
			"original:1": {HasDetail: true, Detail: "resolved second detail", Present: true, Form: "markup", Kind: "markdown", Value: "**SECOND_RESOLVED_DOC**"},
		}}
	app.ws = port
	t.Cleanup(func() { app.closeCompletionPopup(); app.ws = original })
	app.Update(tea.WindowSizeMsg{Width: width, Height: height})
	app.Input.SetInput("keep completion private draft")
	app.setFocusEditor()
	app.paintShellFrame()
	return app, port
}

func completionTestKey(t *testing.T, app *App, key tea.KeyPressMsg) {
	t.Helper()
	_, cmd := app.Update(key)
	runAppCommand(t, app, cmd)
	app.View()
}

func completionPaintedText(app *App) string {
	app.advanceReveals(time.Now().Add(2 * revealDuration))
	app.View()
	fw := app.overlayHost.Window(ui.OverlayCompletion)
	if fw == nil {
		return ""
	}
	w, h := fw.Size()
	surf := surface.NewSurface("completion-test", w, h, app.mainSurf.Buffer().Get(0, 0))
	fw.Content.Render(surf, app.renderCtx)
	var text strings.Builder
	for y := range h {
		text.WriteString(shellchat.PlainRow(surf.Buffer(), y, w))
		text.WriteByte('\n')
	}
	return text.String()
}

func completionRequireText(t *testing.T, app *App, present, absent []string) {
	t.Helper()
	frame := completionPaintedText(app)
	for _, want := range present {
		if !strings.Contains(frame, want) {
			t.Fatalf("missing %q in mounted completion frame:\n%s", want, frame)
		}
	}
	for _, unwanted := range absent {
		if strings.Contains(frame, unwanted) {
			t.Fatalf("nonselected/stale %q painted in completion frame:\n%s", unwanted, frame)
		}
	}
}

func TestCompletionNativeSelectionPaintsOnlyOriginalSelectedDocumentation(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			app, port := completionTestApp(t, size[0], size[1], "pr")
			completionTestKey(t, app, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
			if app.completion == nil || app.completion.screen == nil || app.completion.screen.SelectedID() != "original:0" {
				t.Fatal("registry Ctrl+Space did not mount the native first row")
			}
			completionRequireText(t, app, []string{"FIRST_SELECTED_DOC"}, []string{"SECOND_SELECTED_DOC", "Completions ·"})
			frame := app.overlayHost.Window(ui.OverlayCompletion).Content.(*ui.WindowFrameWidget)
			if rect, ok := frame.Host().ScreenRect(completion.ListID); !ok || rect.Empty() {
				t.Fatal("the native chooser List is not mounted")
			}
			completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyDown})
			if app.completion.screen.SelectedID() != "original:1" {
				t.Fatal("duplicate labels hid the actual native identity change")
			}
			completionRequireText(t, app, []string{"**SECOND_SELECTED_DOC**"}, []string{"FIRST_SELECTED_DOC"})
			completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyUp})
			completionRequireText(t, app, []string{"FIRST_SELECTED_DOC"}, []string{"SECOND_SELECTED_DOC"})
			if len(port.reads()) != 0 {
				t.Fatal("provider without resolve support received a documentation resolve")
			}
			port.mu.Lock()
			requests := append([]completionTestRequest(nil), port.requests...)
			port.mu.Unlock()
			if len(requests) != 1 || requests[0].source != port.set.Boundary.Source ||
				requests[0].view != "editor-completion" || requests[0].sequence != 1 || requests[0].line != 0 || requests[0].col != 2 {
				t.Fatalf("registry did not preserve exact invocation operands: %+v", requests)
			}
			completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyEsc})
			if app.completion != nil || app.overlayHost.IsOpen(ui.OverlayCompletion) || !app.EditorState.Editor.IsOpen() ||
				app.EditorState.Editor.Value() != "pr" || app.Input.Value() != "keep completion private draft" {
				t.Fatal("completion-only navigation/close changed source, editor lifetime or composer")
			}
			select {
			case released := <-port.released:
				if released != (completionTestRelease{"editor-completion", 1}) {
					t.Fatalf("close released a different invocation: %+v", released)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("close did not release its canonical invocation")
			}
			disk, err := os.ReadFile(port.set.Boundary.Source.Path)
			if err != nil || string(disk) != "pr" {
				t.Fatalf("passive docs mutated project text: %q %v", disk, err)
			}
		})
	}
}

func TestCompletionDocumentationShowsAvailableTextOrEmptyState(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		for _, tc := range []struct {
			name            string
			doc             types.LSPCompletionDocumentation
			present, absent []string
		}{
			// A row with nothing to read gets no empty documentation pane.
			{"absent", types.LSPCompletionDocumentation{}, []string{"duplicate"}, []string{"MARKDOWN_LITERAL", "No documentation available."}},
			{"empty", types.LSPCompletionDocumentation{Present: true, Form: "string"}, []string{"duplicate"}, []string{"MARKDOWN_LITERAL", "No documentation available."}},
			{"markdown", types.LSPCompletionDocumentation{Present: true, Form: "markup", Kind: "markdown", Value: "**MARKDOWN_LITERAL**"}, []string{"**MARKDOWN_LITERAL**"}, []string{"No documentation available."}},
		} {
			t.Run(fmt.Sprintf("%dx%d/%s", size[0], size[1], tc.name), func(t *testing.T) {
				app, port := completionTestApp(t, size[0], size[1], "pr")
				port.set.Entries[0].Documentation = tc.doc
				completionTestKey(t, app, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
				completionRequireText(t, app, tc.present, tc.absent)
				completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyEsc})
			})
		}
	}
}

func TestCompletionResolveUsesOriginalIdentityAndAcceptsOnlyInitialSnippet(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		for _, key := range []rune{tea.KeyEnter, tea.KeyTab} {
			t.Run(fmt.Sprintf("%dx%d/%d", size[0], size[1], key), func(t *testing.T) {
				app, port := completionTestApp(t, size[0], size[1], "pr")
				text, plan, err := snippet.Parse("print(${1:value}, $1)$0")
				if err != nil {
					t.Fatal(err)
				}
				port.set.ResolveSupported = true
				port.set.Entries[0].Insertion = types.CompletionItem{Label: "duplicate", InsertText: text, Snippet: plan}
				completionTestKey(t, app, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
				completionRequireText(t, app, []string{"FIRST_RESOLVED_DOC", "resolved first detail"}, []string{"SECOND_RESOLVED_DOC"})
				completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyDown})
				completionRequireText(t, app, []string{"SECOND_RESOLVED_DOC", "resolved second detail"}, []string{"FIRST_RESOLVED_DOC"})
				completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyUp})
				completionTestKey(t, app, tea.KeyPressMsg{Code: key})
				ed := app.EditorState.Editor
				if ed.Value() != "print(value, value)" || !ed.SnippetActive() || ed.SelectedText() != "value" ||
					app.completion != nil || app.editorSnippet == nil {
					t.Fatalf("acceptance did not retain initial insertion/first snippet field: %q selected=%q", ed.Value(), ed.SelectedText())
				}
				reads := port.reads()
				if len(reads) != 3 {
					t.Fatalf("selection/acceptance made duplicate or missing reads: %+v", reads)
				}
				for i, want := range []completionTestInspection{
					{"original-set", strings.Repeat("d", 64), "original:0", true},
					{"original-set", strings.Repeat("d", 64), "original:1", true},
					{"original-set", strings.Repeat("d", 64), "original:0", false},
				} {
					if reads[i] != want {
						t.Fatalf("read %d not original exact object: got %+v want %+v", i, reads[i], want)
					}
				}
				if app.Input.Value() != "keep completion private draft" {
					t.Fatal("acceptance consumed composer")
				}
			})
		}
	}
}

func TestCompletionMountedNativeSemanticSelectionAndActivationUseOriginalRow(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			app, port := completionTestApp(t, size[0], size[1], "pr")
			completionTestKey(t, app, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
			s := app.completion
			if s == nil || s.screen == nil {
				t.Fatal("chooser not mounted")
			}
			host := app.overlayHost.Window(ui.OverlayCompletion).Content.(*ui.WindowFrameWidget).Host()
			operate := func(verb, item string) {
				t.Helper()
				node, err := host.InspectControl(completion.ListID, control.Target{})
				if err != nil {
					t.Fatal(err)
				}
				result, err := host.Control(completion.ListID, node.OpenEpoch, &node.Revision, ui.ControlOperation{Verb: verb, ItemID: item})
				if err != nil {
					t.Fatal(err)
				}
				if result.Action == nil {
					t.Fatal("native semantic operation lost parent action")
				}
				cmd, handled := app.handleCompletionAction(result.Action)
				if !handled {
					t.Fatal("native semantic operation bypassed completion owner")
				}
				runAppCommand(t, app, tea.Batch(result.Cmd, cmd))
			}
			operate("select", "original:1")
			if s.screen.SelectedID() != "original:1" {
				t.Fatal("semantic select used duplicate label instead of original identity")
			}
			completionRequireText(t, app, []string{"SECOND_SELECTED_DOC"}, []string{"FIRST_SELECTED_DOC"})
			operate("activate", "original:1")
			if app.completion != nil || app.EditorState.Editor.Value() != "second" ||
				app.Input.Value() != "keep completion private draft" {
				t.Fatal("semantic activation did not use the selected original insertion")
			}
			reads := port.reads()
			if len(reads) != 1 || reads[0] != (completionTestInspection{"original-set", strings.Repeat("d", 64), "original:1", false}) {
				t.Fatalf("semantic activation did not inspect its exact original insertion once: %+v", reads)
			}
		})
	}
}

func TestCompletionPassiveDocsScrollSelectAndForwardTypingExactlyOnce(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			app, port := completionTestApp(t, size[0], size[1], "pr")
			port.set.Entries[0].Documentation.Value = "SCROLL_TOP\n" + strings.Repeat("Independent documentation line\n", 60) + "SCROLL_BOTTOM"
			completionTestKey(t, app, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
			completionRequireText(t, app, []string{"SCROLL_TOP"}, []string{"SCROLL_BOTTOM", "SECOND_SELECTED_DOC"})
			s := app.completion
			completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyPgDown, Mod: tea.ModCtrl})
			if app.completion != s || s.screen.SelectedID() != "original:0" {
				t.Fatal("documentation paging changed completion selection")
			}
			completionRequireText(t, app, []string{"Independent documentation"}, []string{"SCROLL_TOP", "SECOND_SELECTED_DOC"})
			frame := app.overlayHost.Window(ui.OverlayCompletion).Content.(*ui.WindowFrameWidget)
			rect, ok := frame.Host().ScreenRect(completion.TextID)
			if !ok || rect.Empty() {
				t.Fatal("read-only native documentation control is not mounted")
			}
			// The list does not take focus while typing; a click into the
			// documentation focuses it for selection and copy.
			fw := app.overlayHost.Window(ui.OverlayCompletion)
			wx, wy := fw.Position()
			for _, msg := range []tea.Msg{
				tea.MouseClickMsg{X: wx + rect.X + 1, Y: wy + rect.Y, Button: tea.MouseLeft},
				tea.MouseReleaseMsg{X: wx + rect.X + 1, Y: wy + rect.Y, Button: tea.MouseLeft},
			} {
				_, cmd := app.Update(msg)
				runAppCommand(t, app, cmd)
			}
			if app.focusedPopup() != fw || !frame.Host().Focus(completion.TextID) {
				t.Fatal("a click did not focus the mounted documentation")
			}
			completionTestKey(t, app, tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
			selected := false
			for _, child := range s.screen.Children() {
				if child.ID() == completion.TextID {
					if reader, ok := child.(interface{ HasActiveSelection() bool }); ok {
						selected = reader.HasActiveSelection()
					}
				}
			}
			if !selected {
				t.Fatal("mounted read-only documentation did not support native selection")
			}
			if app.EditorState.Editor.Value() != "pr" || app.EditorState.Editor.Modified() || app.Input.Value() != "keep completion private draft" {
				t.Fatal("selecting read-only documentation changed source/composer")
			}
			completionTestKey(t, app, tea.KeyPressMsg{Code: 'x', Text: "x"})
			if app.completion != nil || app.EditorState.Editor.Value() != "prx" || app.Input.Value() != "keep completion private draft" {
				t.Fatalf("passive documentation did not close and forward typing exactly once: %q", app.EditorState.Editor.Value())
			}
		})
	}
}

func completionInvalidate(t *testing.T, app *App, port *completionTestPort, change string) {
	t.Helper()
	ed := app.EditorState.Editor
	switch change {
	case "source":
		ed.InsertRune('x')
	case "cursor":
		ed.SetCursorPosition(0, 0)
	case "focus":
		app.setFocusTree()
	case "tab":
		path := filepath.Join(app.ws.RootPath(), "another.go")
		if err := os.WriteFile(path, []byte("other"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := app.openEditorTab(path); err != nil {
			t.Fatal(err)
		}
	case "workspace":
		app.workspaceSwitchGen.Add(1)
	case "feature":
		port.service.feature++
	case "off-on":
		port.service.enabled = false
		app.syncCompletion()
		port.service.enabled = true
		port.service.feature += 2
	case "context":
		port.service.context++
	case "context-owner":
		port.service.owner = "another-workspace-owner"
	case "escape":
		completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyEsc})
	default:
		t.Fatal("unknown invalidation", change)
	}
}

func TestCompletionLateInitialResolveAndAcceptanceRepliesCannotReviveInvalidatedUI(t *testing.T) {
	for _, stage := range []string{"initial", "resolve", "accept"} {
		for _, change := range []string{"source", "cursor", "focus", "tab", "workspace", "feature", "off-on", "context", "context-owner", "escape"} {
			t.Run(stage+"/"+change, func(t *testing.T) {
				app, port := completionTestApp(t, 160, 40, "pr")
				originalEditor := app.EditorState.Editor
				var late tea.Msg
				if stage == "initial" {
					_, cmd := app.dispatchEditorKey(tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
					if cmd == nil || app.completion == nil {
						t.Fatal("initial invocation not admitted")
					}
					late = cmd()
				} else {
					port.set.ResolveSupported = stage == "resolve"
					_, cmd := app.dispatchEditorKey(tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
					if cmd == nil {
						t.Fatal("initial invocation not admitted")
					}
					msg, ok := cmd().(completionResultMsg)
					if !ok {
						t.Fatal("unexpected initial result")
					}
					app.handleCompletionResult(msg)
					read := app.syncCompletion()
					app.View()
					if stage == "accept" {
						_, read = app.Update(tea.KeyPressMsg{Code: tea.KeyTab})
					}
					if read == nil || app.completion == nil || app.completion.pending == nil {
						t.Fatal("read was not admitted")
					}
					// Read directly through the authoritative current read token, not
					// a BatchMsg envelope returned by App.Update.
					pending := app.completion.pending
					result, err := port.InspectLSPCompletion(context.Background(), "original-set", strings.Repeat("d", 64), "original:0", stage == "resolve")
					late = completionReadMsg{read: pending, result: result, err: err}
				}
				completionInvalidate(t, app, port, change)
				app.syncCompletion()
				_, cmd := app.Update(late)
				runAppCommand(t, app, cmd)
				if app.completion != nil || app.overlayHost.IsOpen(ui.OverlayCompletion) || completionPaintedText(app) != "" {
					t.Fatal("late result painted or reopened invalidated completion")
				}
				want := "pr"
				if change == "source" {
					want = "prx"
				}
				if originalEditor.Value() != want || app.Input.Value() != "keep completion private draft" {
					t.Fatal("late result mutated original source/private draft")
				}
				if change == "escape" && !originalEditor.IsOpen() {
					t.Fatal("pending Escape closed editor")
				}
			})
		}
	}
}

func TestCompletionAllAnswersUseOriginalContextBarrierAndQuietAutomaticResults(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		for _, answer := range []string{"items", "empty", "null", "unsupported", "error"} {
			t.Run(fmt.Sprintf("automatic=%t/%s", automatic, answer), func(t *testing.T) {
				app, port := completionTestApp(t, 160, 40, "pr")
				if answer == "error" {
					port.requestErr = errors.New("independent request failure")
					// A transport error has no semantic set. Its admission-time
					// context clock is nevertheless an owner-qualified observation.
					port.service.context = 7
				} else {
					port.set.Boundary.Context.Generation = 8
					port.set.Answer = answer
					if answer != "items" {
						port.set.Entries = nil
					}
				}
				for i := range port.set.Entries {
					port.set.Entries[i].FilterText = "print" // continues the typed "pr"
				}
				cmd := app.requestCompletion(automatic, types.LSPCompletionContext{})
				if cmd == nil {
					t.Fatal("request not admitted")
				}
				if answer == "error" {
					port.service.context = 0
				}
				_, next := app.Update(cmd())
				runAppCommand(t, app, next)
				if app.completion == nil || app.completion.screen != nil || app.overlayHost.IsOpen(ui.OverlayCompletion) {
					t.Fatal("answer escaped its original context publication barrier")
				}
				for _, summary := range []string{"Completion: independent request failure", "No suggestions at this position.", "This language server does not support completion."} {
					if signatureHasNotice(app, summary) {
						t.Fatal("unpublished answer already reported", summary)
					}
				}
				if answer == "error" {
					port.service.context = 7
				} else {
					port.service.context = 8
				}
				runAppCommand(t, app, app.syncCompletion())
				if answer == "items" && automatic {
					// Automatic suggestions choose no row, so no documentation shows.
					completionRequireText(t, app, []string{"duplicate"}, []string{"FIRST_SELECTED_DOC", "SECOND_SELECTED_DOC"})
				} else if answer == "items" {
					completionRequireText(t, app, []string{"FIRST_SELECTED_DOC"}, []string{"SECOND_SELECTED_DOC"})
				} else {
					if app.completion != nil || app.overlayHost.IsOpen(ui.OverlayCompletion) {
						t.Fatal("negative answer did not settle after exact publication")
					}
					notice := map[string]string{"empty": "No suggestions at this position.", "null": "No suggestions at this position.",
						"unsupported": "This language server does not support completion.", "error": "Completion: independent request failure"}[answer]
					if signatureHasNotice(app, notice) == automatic {
						t.Fatalf("explicit/automatic feedback distinction lost: %q automatic=%t", notice, automatic)
					}
				}
				if app.EditorState.Editor.Value() != "pr" || app.EditorState.Editor.Modified() || app.Input.Value() != "keep completion private draft" {
					t.Fatal("context barrier changed source/private draft")
				}
			})
		}
	}
}

func TestCompletionOldSelectedResolveCannotReplaceCurrentDuplicateLabelDocs(t *testing.T) {
	app, port := completionTestApp(t, 160, 40, "pr")
	port.set.ResolveSupported = true
	_, request := app.dispatchEditorKey(tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
	if request == nil {
		t.Fatal("request not admitted")
	}
	msg, ok := request().(completionResultMsg)
	if !ok {
		t.Fatal("unexpected request result")
	}
	app.handleCompletionResult(msg)
	firstResolve := app.syncCompletion()
	if firstResolve == nil || app.completion == nil || app.completion.pending == nil {
		t.Fatal("initial resolve was not admitted")
	}
	old := app.completion.pending
	late := firstResolve()
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyDown})
	if old.owner.screen.SelectedID() != "original:1" {
		t.Fatal("native selection did not leave first row")
	}
	_, cmd := app.Update(late)
	runAppCommand(t, app, cmd)
	completionRequireText(t, app, []string{"SECOND_RESOLVED_DOC"}, []string{"FIRST_RESOLVED_DOC", "FIRST_SELECTED_DOC"})
	if app.completion != old.owner || app.completion.screen.SelectedID() != "original:1" {
		t.Fatal("late old-row read dismissed or replaced the current invocation")
	}
}

func TestCompletionRejectsUnqualifiedInitialAndInspectionEvidence(t *testing.T) {
	for _, field := range []string{"set-id", "digest", "view", "sequence", "source", "feature", "position"} {
		t.Run("initial/"+field, func(t *testing.T) {
			app, _ := completionTestApp(t, 160, 40, "pr")
			cmd := app.requestCompletionCmd()
			if cmd == nil {
				t.Fatal("request not admitted")
			}
			msg, ok := cmd().(completionResultMsg)
			if !ok {
				t.Fatal("unexpected request result")
			}
			switch field {
			case "set-id":
				msg.set.ID = ""
			case "digest":
				msg.set.Digest = "not-an-original-digest"
			case "view":
				msg.set.ViewID = "another-view"
			case "sequence":
				msg.set.Sequence++
			case "source":
				msg.set.Boundary.Source.Lifetime = "another-open"
			case "feature":
				msg.set.FeatureGeneration++
			case "position":
				msg.set.Col++
			}
			_, next := app.Update(msg)
			runAppCommand(t, app, next)
			if app.completion != nil || app.overlayHost.IsOpen(ui.OverlayCompletion) ||
				app.EditorState.Editor.Value() != "pr" || app.Input.Value() != "keep completion private draft" {
				t.Fatal("unqualified initial result painted or mutated source/private draft")
			}
		})
	}
	for _, field := range []string{"set-id", "digest", "item", "source", "context", "producer", "wire", "feature"} {
		t.Run("accept/"+field, func(t *testing.T) {
			app, port := completionTestApp(t, 160, 40, "pr")
			completionTestKey(t, app, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
			_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
			if cmd == nil || app.completion == nil || app.completion.pending == nil {
				t.Fatal("acceptance read not admitted")
			}
			pending := app.completion.pending
			result, err := port.InspectLSPCompletion(context.Background(), "original-set", strings.Repeat("d", 64), "original:0", false)
			if err != nil {
				t.Fatal(err)
			}
			switch field {
			case "set-id":
				result.SetID = "another-set"
			case "digest":
				result.Digest = strings.Repeat("e", 64)
			case "item":
				result.ItemID = "original:1"
			case "source":
				result.Boundary.Source.Lifetime = "another-open"
			case "context":
				result.Boundary.Context.Generation++
			case "producer":
				result.Boundary.Producer.InstanceID = "another-instance"
			case "wire":
				result.WireVersion++
			case "feature":
				result.FeatureGeneration++
			}
			_, next := app.Update(completionReadMsg{read: pending, result: result})
			runAppCommand(t, app, next)
			if app.completion != nil || app.overlayHost.IsOpen(ui.OverlayCompletion) ||
				app.EditorState.Editor.Value() != "pr" || app.Input.Value() != "keep completion private draft" {
				t.Fatal("unqualified original-object inspection authorized an insertion")
			}
		})
	}
}

func TestCompletionInspectionErrorRemainsInertAndPendingEscapeDoesNotCloseEditor(t *testing.T) {
	for _, automatic := range []bool{false, true} {
		t.Run(fmt.Sprintf("pending-escape/automatic=%t", automatic), func(t *testing.T) {
			app, _ := completionTestApp(t, 80, 24, "pr")
			cmd := app.requestCompletion(automatic, types.LSPCompletionContext{})
			s := app.completion
			if cmd == nil || s == nil {
				t.Fatal("pending request not admitted")
			}
			completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyEsc})
			if app.completion != nil || s.ctx.Err() == nil || !s.released || !s.editor.IsOpen() || s.editor.Value() != "pr" {
				t.Fatal("pending Esc did not cancel only the completion invocation")
			}
			_, next := app.Update(cmd())
			runAppCommand(t, app, next)
			if app.completion != nil || !s.editor.IsOpen() || s.editor.Value() != "pr" {
				t.Fatal("cancelled request revived UI or closed editor")
			}
		})
	}
	for _, resolve := range []bool{false, true} {
		t.Run(fmt.Sprintf("inspection-error/resolve=%t", resolve), func(t *testing.T) {
			app, port := completionTestApp(t, 160, 40, "pr")
			port.set.ResolveSupported = resolve
			port.inspectErr = errors.New("independent inspection failure")
			completionTestKey(t, app, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
			if !resolve {
				completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyTab})
			}
			status := "Could not insert this suggestion."
			if resolve {
				status = "Could not load documentation."
			}
			completionRequireText(t, app, []string{status, "FIRST_SELECTED_DOC"}, []string{"FIRST_RESOLVED_DOC", "SECOND_SELECTED_DOC"})
			if app.EditorState.Editor.Value() != "pr" || app.EditorState.Editor.Modified() ||
				app.Input.Value() != "keep completion private draft" {
				t.Fatal("failed inspection inserted or consumed source/draft")
			}
		})
	}
}

func (s *completionTestService) AssistanceCapabilities(string) types.LSPAssistanceCapabilities {
	if s.assistance != nil {
		return types.CloneLSPAssistance(*s.assistance)
	}
	return types.LSPAssistanceCapabilities{Completion: true, CompletionTriggers: []string{"."}, SignatureHelp: true, SignatureTriggers: []string{"(", ","}}
}

func TestCompletionRejectedInsertionExplainsNextStep(t *testing.T) {
	app, port := completionTestApp(t, 80, 24, "pr")
	port.set.Entries[0].Insertion = types.CompletionItem{Label: "bad", InsertText: "x", AdjustIndentation: true, SnippetSource: "unexpected"}
	completionTestKey(t, app, tea.KeyPressMsg{Code: ' ', Mod: tea.ModCtrl})
	completionTestKey(t, app, tea.KeyPressMsg{Code: tea.KeyTab})
	if app.EditorState.Editor.Value() != "pr" || app.EditorState.Editor.Modified() {
		t.Fatal("rejected completion changed text")
	}
	found := false
	for _, notice := range app.ws.Status().Notifications(true) {
		if notice.Summary == "Could not insert this suggestion. Try another suggestion." && strings.Contains(notice.Detail, "unexpected snippet source") {
			found = true
		}
	}
	if !found {
		t.Fatal("rejected completion did not give an actionable summary and technical detail")
	}
}
