package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"crude/indentation"
	"crude/lspedit"
	"crude/tui/editor"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

type formatCall struct {
	Text    string
	Span    types.LSPFormattingSpan
	Options types.LSPFormattingOptions
}
type selectionFormattingRuntime struct {
	base        renameConnectedRuntime
	calls       []formatCall
	raw         string
	unsupported bool
	entered     chan struct{}
	release     <-chan struct{}
}
type selectionFormattingClient struct {
	*renameConnectedClient
	owner *selectionFormattingRuntime
}

func (r *selectionFormattingRuntime) NewClient(spec types.LSPServerSpec, root string, admit func() error) workspace.LSPClient {
	return &selectionFormattingClient{r.base.NewClient(spec, root, admit).(*renameConnectedClient), r}
}
func (c *selectionFormattingClient) RequestRangeFormatting(_ context.Context, path string, span types.LSPFormattingSpan, options types.LSPFormattingOptions) (types.LSPFormattingResponse, error) {
	r := c.owner
	r.base.mu.Lock()
	d := c.docs[path]
	r.calls = append(r.calls, formatCall{d.text, span, options})
	raw, unsupported, entered, release := r.raw, r.unsupported, r.entered, r.release
	r.base.mu.Unlock()
	if entered != nil {
		entered <- struct{}{}
	}
	if release != nil {
		<-release
	}
	if raw == "" {
		sl, sc, err := lspedit.Position(d.text, span.Start)
		if err != nil {
			return types.LSPFormattingResponse{}, err
		}
		el, ec, err := lspedit.Position(d.text, span.End)
		if err != nil {
			return types.LSPFormattingResponse{}, err
		}
		text := strings.ReplaceAll(d.text[span.Start:span.End], "=", " = ")
		body, _ := json.Marshal([]any{map[string]any{"range": map[string]any{"start": map[string]int{"line": sl, "character": sc}, "end": map[string]int{"line": el, "character": ec}}, "newText": text}})
		raw = string(body)
	}
	return types.LSPFormattingResponse{WireVersion: d.version, JSON: []byte(raw), Unsupported: unsupported}, nil
}
func newSelectionFormattingFixture(t *testing.T, text string) ([]remoteJourneyShell, []*editor.Editor, *selectionFormattingRuntime, string) {
	t.Helper()
	root := t.TempDir()
	path := writePaneFile(t, root, "format.go", text)
	writePaneFile(t, root, "go.mod", "module fixture\n\ngo 1.22\n")
	executable, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	engine := &selectionFormattingRuntime{}
	var owned *workspace.Workspace
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		owned = w
		w.LSPMgr.SetRuntime(engine)
		s := w.AppDefaults()
		s.Editor.LineNumbers = true
		s.LSP = types.DefaultLSPSettings()
		s.LSP.AutoComplete = false
		s.LSP.AutoDetect = false
		s.LSP.Languages = map[string]types.LSPLanguageSettings{"go": {Enabled: true, Command: executable, RootMarkers: []string{"go.mod"}}}
		w.SetAppDefaults(s)
		grantSemanticFixture(t, w)
	})
	shells := []remoteJourneyShell{newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)}
	eds := []*editor.Editor{liveTestOpen(t, shells[0].app, path), liveTestOpen(t, shells[1].app, path)}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, _, err := owned.LSPMgr.ProbeHover(ctx, path, types.LSPSourcePosition{Line: 1, Column: 1}); err != nil {
		t.Fatal(err)
	}
	remoteJourneyUntil(t, shells, func() bool {
		_, ready := eds[0].CurrentInsightSource()
		return ready && shells[0].app.ws.LSPMgr().ServerReady()
	})
	for _, shell := range shells {
		t.Cleanup(installDetachedMessageSinkForTest(shell.app, func(msg tea.Msg) { shell.fifo <- msg }))
	}
	shells[0].app.setFocusEditor()
	shells[0].app.Input.SetInput("private format draft")
	return shells, eds, engine, path
}
func TestSelectionFormattingConnectedKeyMenuControlAndUndo(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			const source = "alpha=1;\r\nbeta=2;\r\nomega=3;\r\n"
			shells, eds, engine, path := newSelectionFormattingFixture(t, source)
			app, ed := shells[0].app, eds[0]
			t.Cleanup(func() {
				if t.Failed() {
					engine.base.mu.Lock()
					defer engine.base.mu.Unlock()
					t.Logf("request=%+v calls=%+v source=%+v notice=%+v", app.selectionFormatting, engine.calls, ed.DocumentState(), app.ws.Status().Notifications(true))
				}
			})
			app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			if err := ed.SetIndentationChoices(indentation.Choices{Style: indentation.Spaces, Size: 2, TabWidth: 8}); err != nil {
				t.Fatal(err)
			}
			ed.SetCursorPosition(1, 7)
			ed.SetSelection(1, 0, 1, 7)
			renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: tea.KeyF8, Mod: tea.ModAlt})
			want := "alpha=1;\r\nbeta = 2;\r\nomega=3;\r\n"
			remoteJourneyUntil(t, shells, func() bool { return ed.Value() == want && eds[1].Value() == want })
			if ed.SelectedText() != "beta = 2;" {
				t.Fatal("formatted source selection lost", ed.SelectedText())
			}
			engine.base.mu.Lock()
			calls := append([]formatCall(nil), engine.calls...)
			engine.base.mu.Unlock()
			if len(calls) != 1 || calls[0].Text != source || calls[0].Span != (types.LSPFormattingSpan{Start: 10, End: 17}) || calls[0].Options != (types.LSPFormattingOptions{TabSize: 2, InsertSpaces: true}) {
				t.Fatal("wrong source/range/indent options", calls)
			}
			renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			remoteJourneyUntil(t, shells, func() bool { return ed.Value() == source && eds[1].Value() == source })
			menu := app.buildEditorContextMenu(tea.Mouse{})
			item, ok := contextMenuItem(menu, "Format selection")
			if !ok || item.Disabled {
				t.Fatal("menu cannot format selection")
			}
			item.Action()
			remoteJourneyUntil(t, shells, func() bool { return ed.Value() == want && eds[1].Value() == want })
			renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			remoteJourneyUntil(t, shells, func() bool { return ed.Value() == source && eds[1].Value() == source })
			token := ed.ActionToken()
			result := productRequest(t, app, "editor.action", ed.DocumentState().DocumentID, "", map[string]any{"action_id": "edit.formatSelection", "view_token": token}, nil)
			if result.State != control.Admitted {
				t.Fatal(result)
			}
			remoteJourneyUntil(t, shells, func() bool { return ed.Value() == want && eds[1].Value() == want })
			if disk, err := os.ReadFile(path); err != nil || string(disk) != source || app.Input.Value() != "private format draft" {
				t.Fatal("selection formatting saved source or consumed draft", err)
			}
		})
	}
}
func TestSelectionFormattingStaleSelectionAndCancellationNeverApply(t *testing.T) {
	for _, cancelKind := range []string{"selection", "source", "cancel"} {
		t.Run(cancelKind, func(t *testing.T) {
			source := "x=1;\ny=2;"
			shells, eds, engine, _ := newSelectionFormattingFixture(t, source)
			app, ed := shells[0].app, eds[0]
			ed.SetSelection(0, 0, 0, 4)
			release := make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			engine.base.mu.Lock()
			engine.entered = make(chan struct{}, 1)
			engine.release = release
			entered := engine.entered
			engine.base.mu.Unlock()
			cmd := app.requestSelectionFormat(ed)
			done := make(chan tea.Msg, 1)
			go func() { done <- cmd() }()
			select {
			case <-entered:
			case <-time.After(5 * time.Second):
				t.Fatal("formatter not called")
			}
			switch cancelKind {
			case "selection":
				ed.SetSelection(1, 0, 1, 4)
			case "source":
				renameConnectedKey(t, shells[1], tea.KeyPressMsg{Code: 'X', Text: "X"})
				remoteJourneyUntil(t, shells, func() bool { return ed.Value() != source })
			case "cancel":
				app.cancelSelectionFormatting()
			}
			app.syncSelectionFormatting()
			close(release)
			result := <-done
			if result != nil {
				runAppCommand(t, app, func() tea.Msg { return result })
			}
			if strings.Contains(ed.Value(), " = ") {
				t.Fatal("stale formatter changed source", ed.Value())
			}
			if app.selectionFormatting != nil {
				t.Fatal("cancelled request retained its slot")
			}
		})
	}
}
func TestSelectionFormattingMalformedUnsupportedAndEmpty(t *testing.T) {
	for _, raw := range []string{"[]", "null", "unsupported", `[{"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":4}},"newText":"outside"}]`, `[{}]`} {
		t.Run(raw, func(t *testing.T) {
			source := "x=1;\ny=2;"
			shells, eds, engine, _ := newSelectionFormattingFixture(t, source)
			app, ed := shells[0].app, eds[0]
			ed.SetSelection(0, 0, 0, 4)
			engine.base.mu.Lock()
			engine.raw = raw
			engine.unsupported = raw == "unsupported"
			engine.base.mu.Unlock()
			runAppCommand(t, app, app.requestSelectionFormat(ed))
			if ed.Value() != source || app.selectionFormatting != nil {
				t.Fatal("invalid/empty result mutated or stuck", ed.Value())
			}
			if raw == "[]" || raw == "null" {
				found := false
				for _, notice := range app.ws.Status().Notifications(true) {
					found = found || notice.Summary == "No formatting changes were needed."
				}
				if !found {
					t.Fatal("no-change result incorrectly reported as an error")
				}
			}
		})
	}
}

func TestSelectionFormattingMultipleRangesOneUndo(t *testing.T) {
	source := "one=1;\r\none=1;\r\nlast=9;"
	shells, eds, engine, _ := newSelectionFormattingFixture(t, source)
	ed := eds[0]
	ed.SetCursorPosition(0, 6)
	ed.SetSelection(0, 0, 0, 6)
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'n', Mod: tea.ModAlt})
	if ed.CaretCount() != 2 {
		t.Fatal("two selected ranges missing")
	}
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: tea.KeyF8, Mod: tea.ModAlt})
	want := "one = 1;\r\none = 1;\r\nlast=9;"
	remoteJourneyUntil(t, shells, func() bool { return ed.Value() == want && eds[1].Value() == want })
	engine.base.mu.Lock()
	count := len(engine.calls)
	engine.base.mu.Unlock()
	if count != 2 || ed.SelectedText() != "one = 1;\r\none = 1;" {
		t.Fatal("formatting merged or lost disjoint selections", ed.SelectedText(), count)
	}
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, shells, func() bool { return ed.Value() == source && eds[1].Value() == source })
	if ed.CaretCount() != 2 || ed.SelectedText() != "one=1;\r\none=1;" {
		t.Fatal("one Undo lost the selection set")
	}
}

func TestSelectionFormattingStaleMenuDoesNotRetarget(t *testing.T) {
	shells, eds, engine, _ := newSelectionFormattingFixture(t, "x=1;\ny=2;")
	app, ed := shells[0].app, eds[0]
	ed.SetSelection(0, 0, 0, 4)
	menu := app.buildEditorContextMenu(tea.Mouse{})
	item, ok := contextMenuItem(menu, "Format selection")
	if !ok {
		t.Fatal("format selection missing")
	}
	ed.SetSelection(1, 0, 1, 4)
	item.Action()
	if app.selectionFormatting != nil {
		t.Fatal("stale menu changed target selection")
	}
	engine.base.mu.Lock()
	defer engine.base.mu.Unlock()
	if len(engine.calls) != 0 {
		t.Fatal("stale menu reached provider")
	}
}

func TestSelectionFormattingFlushesTypedSourceBeforeRequest(t *testing.T) {
	shells, eds, engine, path := newSelectionFormattingFixture(t, "x=1;\ny=2;")
	app, ed := shells[0].app, eds[0]
	ed.SetCursorPosition(0, 3)
	_, typing := app.Update(tea.KeyPressMsg{Code: '0', Text: "0"})
	// Keep the edit receipt outstanding as the next ordinary formatting key arrives.
	ed.SetSelection(0, 0, 0, 5)
	_, format := app.Update(tea.KeyPressMsg{Code: tea.KeyF8, Mod: tea.ModAlt})
	runAppCommand(t, app, tea.Batch(typing, format))
	remoteJourneyUntil(t, shells, func() bool { return ed.Value() == "x = 10;\ny=2;" && eds[1].Value() == ed.Value() })
	engine.base.mu.Lock()
	calls := append([]formatCall(nil), engine.calls...)
	engine.base.mu.Unlock()
	if len(calls) != 1 || calls[0].Text != "x=10;\ny=2;" {
		t.Fatal("formatter saw saved or pre-typing source", calls)
	}
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, shells, func() bool { return ed.Value() == "x=10;\ny=2;" && eds[1].Value() == ed.Value() })
	if !ed.Modified() {
		t.Fatal("format Undo discarded the preceding unsaved edit")
	}
	if disk, err := os.ReadFile(path); err != nil || string(disk) != "x=1;\ny=2;" {
		t.Fatal("formatting flushed bytes to disk", err)
	}
}
