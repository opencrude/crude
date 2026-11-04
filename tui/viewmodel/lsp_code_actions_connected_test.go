package viewmodel

import (
	"context"
	"crude/internal/fileuri"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/shellport"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
)

const actionConnectedRaw = `[
{"title":"Replace old with new","kind":"quickfix","isPreferred":true,"edit":{"changes":{$MAIN:[{"range":{"start":{"line":2,"character":4},"end":{"line":2,"character":7}},"newText":"new"}]}}},
{"title":"Unsafe command row","command":{"title":"Do not run","command":"forbidden.command"}},
{"title":"Provider disabled row","disabled":{"reason":"Literal provider reason"},"edit":{"changes":{$MAIN:[{"range":{"start":{"line":2,"character":4},"end":{"line":2,"character":7}},"newText":"unapproved"}]}}}
]`

type actionConnectedRuntime struct {
	renameConnectedRuntime
	actions string
	span    [4]int
}

type actionConnectedClient struct {
	*renameConnectedClient
	runtime *actionConnectedRuntime
}

func (r *actionConnectedRuntime) NewClient(spec types.LSPServerSpec, root string, admit func() error) workspace.LSPClient {
	base := r.renameConnectedRuntime.NewClient(spec, root, admit).(*renameConnectedClient)
	return &actionConnectedClient{renameConnectedClient: base, runtime: r}
}

func (c *actionConnectedClient) RequestCodeActions(_ context.Context, path string, line, col, endLine, endCol int) (types.LSPCodeActionResponse, error) {
	r := c.runtime
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.span = [4]int{line, col, endLine, endCol}
	d := c.docs[path]
	if path != r.main || d.text != renameConnectedMain {
		return types.LSPCodeActionResponse{}, fmt.Errorf("unexpected action source %q", path)
	}
	uri := strconv.Quote(fileuri.FromPath(path))
	return types.LSPCodeActionResponse{JSON: []byte(strings.ReplaceAll(r.actions, "$MAIN", uri)),
		WireVersion: d.version, DiagnosticsDetail: "fixture has no diagnostic adjunct"}, nil
}

func newActionConnectedFixture(t *testing.T, raw string) (renameConnectedFixture, *actionConnectedRuntime) {
	t.Helper()
	root := t.TempDir()
	engine := &actionConnectedRuntime{renameConnectedRuntime: renameConnectedRuntime{
		main: filepath.Join(root, "main.go"), peer: filepath.Join(root, "peer.go")}, actions: raw}
	for path, text := range map[string]string{
		engine.main: renameConnectedMain, engine.peer: renameConnectedPeer,
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
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		owned = w
		w.LSPMgr.SetRuntime(engine)
		settings := w.AppDefaults()
		settings.LSP = types.DefaultLSPSettings()
		settings.LSP.AutoDetect, settings.LSP.AutoComplete = false, false
		settings.LSP.Languages = map[string]types.LSPLanguageSettings{"go": {
			Enabled: true, Command: executable, RootMarkers: []string{"go.mod"},
		}}
		w.SetAppDefaults(settings)
		grantSemanticFixture(t, w)
	})
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	a, b := liveTestOpen(t, one.app, engine.main), liveTestOpen(t, two.app, engine.main)
	f := renameConnectedFixture{runtime: runtime, shells: []remoteJourneyShell{one, two}, ws: owned,
		engine: &engine.renameConnectedRuntime, main: a, peer: b}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, view, err := owned.LSPMgr.ProbeHover(ctx, engine.main, types.LSPSourcePosition{Line: 3, Column: 1}); err != nil || view.Source.Kind != "canonical" {
		t.Fatal("canonical action source not synchronized", view, err)
	}
	remoteJourneyUntil(t, f.shells, func() bool {
		_, aReady := a.CurrentInsightSource()
		_, bReady := b.CurrentInsightSource()
		return aReady && bReady && one.app.ws.LSPMgr().ServerReady()
	})
	a.SetCursorPosition(2, 4)
	b.SetCursorPosition(0, 7)
	one.app.Input.SetInput("private action first draft")
	two.app.Input.SetInput("private action second draft")
	one.app.setFocusEditor()
	two.app.setFocusEditor()
	return f, engine
}

func actionConnectedOpen(t *testing.T, f renameConnectedFixture) *codeActionState {
	t.Helper()
	one := f.shells[0]
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModAlt})
	remoteJourneyUntil(t, f.shells, func() bool {
		return one.app.codeActions != nil && one.app.codeActions.set.ID != ""
	})
	s := one.app.codeActions
	if paint := ansi.Strip(one.app.View().RenderContent()); !strings.Contains(paint, "Code actions") {
		t.Fatalf("Alt+Enter did not paint the actual chooser:\n%s", paint)
	}
	return s
}

func actionConnectedPreview(t *testing.T, f renameConnectedFixture) workspace.ReviewedEditPreview {
	t.Helper()
	actionConnectedOpen(t, f)
	one := f.shells[0]
	// Choosing the action enters its real review, without a metadata page.
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
	remoteJourneyUntil(t, f.shells, func() bool { return one.app.LSPPopup.EditPreview != nil })
	p := one.app.LSPPopup.EditPreview
	if paint := ansi.Strip(one.app.View().RenderContent()); !strings.Contains(paint, "Review code action") ||
		!strings.Contains(paint, "Apply changes") || !strings.Contains(paint, "- var old = 1") ||
		!strings.Contains(paint, "+ var new = 1") {
		t.Fatalf("action did not enter the shared literal review:\n%s", paint)
	} else {
		for _, banned := range []string{"SHA-256", "canonical", "UTF-16", "literal", "Set digest:"} {
			if strings.Contains(paint, banned) {
				t.Fatalf("the change review shows internal evidence %q:\n%s", banned, paint)
			}
		}
	}
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
	if paint := ansi.Strip(one.app.View().RenderContent()); !strings.Contains(paint, "- var old = 1") || !strings.Contains(paint, "+ var new = 1") {
		t.Fatalf("complete changed spans are not reachable through the native reader:\n%s", paint)
	}
	return p.request.preview
}

func actionConnectedBytes(t *testing.T, f renameConnectedFixture, text string) {
	t.Helper()
	disk, err := os.ReadFile(f.engine.main)
	if err != nil || string(disk) != text {
		t.Fatalf("literal disk result=%q want=%q err=%v", disk, text, err)
	}
	if f.main.Value() != text || f.peer.Value() != text {
		t.Fatal("both rendered editor owners did not receive the same canonical source")
	}
}

func TestConnectedCodeActionKeysDefaultCancelAndExactSharedApplyReceipt(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		t.Run(fmt.Sprintf("%dx%d", size[0], size[1]), func(t *testing.T) {
			f, _ := newActionConnectedFixture(t, actionConnectedRaw)
			one, two := f.shells[0], f.shells[1]
			for _, shell := range f.shells {
				shell.app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			}
			cancelled := actionConnectedPreview(t, f)
			actionConnectedBytes(t, f, renameConnectedMain)
			renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter}) // Shared review defaults to Cancel.
			remoteJourneyUntil(t, f.shells, func() bool { return one.app.LSPPopup.EditPreview == nil })
			actionConnectedBytes(t, f, renameConnectedMain)
			if out, err := one.app.ws.(shellport.ReviewedEdits).InspectReviewedEdit(t.Context(), cancelled.ID, cancelled.Digest); err != nil || out.State != "pending" || out.SafetySnapshotID != "" {
				t.Fatal("cancelled preview invented an admitted mutation or retained safety", out, err)
			}
			plan := actionConnectedPreview(t, f)
			if plan.ID == cancelled.ID || len(plan.Files) != 1 {
				t.Fatal("fresh action resurrected the cancelled permit")
			}
			foreign, err := two.app.ws.(shellport.ReviewedEdits).ApplyReviewedEdit(t.Context(), plan.ID, plan.Digest)
			if err == nil && foreign.State != "refused" {
				t.Fatal("another shell acquired private action approval", foreign)
			}
			result := journeySettingsControl(t, f.runtime, f.shells, one, "reviewed-edit/actions/Apply changes", ui.ControlOperation{Verb: "activate"})
			if result.State != control.Committed || len(result.ResourceIDs) != 2 || result.ResourceIDs[0] != plan.ID {
				t.Fatal("Apply acknowledged before exact mutation and safety receipt", result)
			}
			remoteJourneyUntil(t, f.shells, func() bool {
				return f.main.Value() == "package journey\n\nvar new = 1\n" && f.peer.Value() == f.main.Value()
			})
			actionConnectedBytes(t, f, "package journey\n\nvar new = 1\n")
			for _, shell := range f.shells {
				if !strings.Contains(ansi.Strip(shell.app.View().RenderContent()), "var new = 1") {
					t.Fatal("canonical edit was not painted in both shells")
				}
			}
			if f.main.CursorLine() != 2 || f.main.CursorCol() != 4 || f.peer.CursorLine() != 0 || f.peer.CursorCol() != 7 {
				t.Fatal("shared action reset private cursors")
			}
			if one.app.Input.Value() != "private action first draft" || two.app.Input.Value() != "private action second draft" {
				t.Fatal("code actions consumed private composer drafts")
			}
			retained, err := f.ws.LocalHistory.LoadRetainedSnapshot(t.Context(), plan.ID)
			if err != nil || retained.Entries["main.go"].Hash != plan.Files[0].BeforeSHA256 || len(retained.Entries) != 1 {
				t.Fatal("exact selected recovery preimage was not retained", retained, err)
			}
		})
	}
}

func TestConnectedCodeActionAfterRestartLanguageServers(t *testing.T) {
	f, _ := newActionConnectedFixture(t, actionConnectedRaw)
	one := f.shells[0]
	before := one.app.ws.LSPMgr().ServerHealthSnapshots()[0].InstanceID
	runAppCommand(t, one.app, one.app.executePaletteCommand("lsp.restart"))
	remoteJourneyUntil(t, f.shells, func() bool {
		health := one.app.ws.LSPMgr().ServerHealthSnapshots()
		return len(health) != 0 && health[0].Connected && health[0].InstanceID != before
	})
	actionConnectedPreview(t, f)
	actionConnectedBytes(t, f, renameConnectedMain)
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEsc})
	remoteJourneyUntil(t, f.shells, func() bool { return one.app.LSPPopup.EditPreview == nil })
}

func TestConnectedCodeActionsUnsupportedEmptyAndSelectionUseActualChooser(t *testing.T) {
	f, engine := newActionConnectedFixture(t, actionConnectedRaw)
	one := f.shells[0]
	// Scored selection uses the actual editor's keyboard path.
	for range 3 {
		renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModShift})
	}
	s := actionConnectedOpen(t, f)
	engine.mu.Lock()
	span := engine.span
	engine.mu.Unlock()
	if span != [4]int{2, 4, 2, 7} || s.set.Start != 21 || s.set.End != 24 {
		t.Fatal("actual selection was not mapped to source bytes and LSP UTF16", span, s.set.Start, s.set.End)
	}
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyDown})
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
	if paint := ansi.Strip(one.app.View().RenderContent()); !strings.Contains(paint, "containing commands") || strings.Contains(paint, "Preview changes") {
		t.Fatalf("unsupported command became actionable or invisible:\n%s", paint)
	}
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEsc}) // Back to original list.
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEsc}) // Close list.
	actionConnectedBytes(t, f, renameConnectedMain)
	engine.mu.Lock()
	engine.actions = "null"
	engine.mu.Unlock()
	empty := actionConnectedOpen(t, f)
	if len(empty.set.Items) != 0 || !strings.Contains(ansi.Strip(one.app.View().RenderContent()), "No code actions available here") {
		t.Fatal("supported null became failure or stale actionable rows")
	}
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEsc})
	actionConnectedBytes(t, f, renameConnectedMain)
}

func TestConnectedCodeActionPrepareControlWaitsForReviewAndCloseRevokesSet(t *testing.T) {
	f, _ := newActionConnectedFixture(t, actionConnectedRaw)
	one := f.shells[0]
	s := actionConnectedOpen(t, f)
	result := journeySettingsControl(t, f.runtime, f.shells, one, "code-actions/list", ui.ControlOperation{Verb: "activate", ItemID: "0"})
	if result.State != control.Applied || len(result.ResourceIDs) != 1 || one.app.LSPPopup.EditPreview == nil {
		t.Fatal("semantic Preview did not await exact mounted review", result)
	}
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEsc})
	remoteJourneyUntil(t, f.shells, func() bool { return one.app.LSPPopup.EditPreview == nil })
	if preview, err := one.app.ws.(shellport.CodeActions).PrepareLSPCodeAction(t.Context(), s.set.ID, s.set.Digest, 0); err == nil || preview.ID != "" {
		t.Fatal("closing the shared review failed to retire its original action set")
	}
	actionConnectedOpen(t, f)
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEsc})
	if one.app.codeActions != nil {
		t.Fatal("Escape did not retire the action list")
	}
	actionConnectedBytes(t, f, renameConnectedMain)
}

type lostActionReviewReply struct {
	shellport.ReviewedEdits
	applies atomic.Int32
}

func (p *lostActionReviewReply) ApplyReviewedEdit(ctx context.Context, id, digest string) (workspace.ReviewedEditResult, error) {
	p.applies.Add(1)
	out, err := p.ReviewedEdits.ApplyReviewedEdit(ctx, id, digest)
	if err != nil || out.State != "committed" {
		return out, err
	}
	// Fault only the result carrier after the real canonical RPC completes.
	// This never patches canonical workspace state or invents a disk effect.
	return workspace.ReviewedEditResult{}, errors.New("injected lost final action reply")
}

func TestConnectedCodeActionLostReplyChecksOriginalWithoutReapplying(t *testing.T) {
	f, _ := newActionConnectedFixture(t, actionConnectedRaw)
	one := f.shells[0]
	plan := actionConnectedPreview(t, f)
	p := one.app.LSPPopup.EditPreview
	carrier := &lostActionReviewReply{ReviewedEdits: p.request.port}
	p.request.port = carrier
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyLeft})
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
	if carrier.applies.Load() != 1 || one.app.LSPPopup.EditPreview != p || p.busy {
		t.Fatal("unknown reply became success, replay, or lost its original reader")
	}
	if paint := ansi.Strip(one.app.View().RenderContent()); !strings.Contains(paint, "Check result") || strings.Contains(paint, "[ Apply changes ]") {
		t.Fatalf("unknown outcome offered another Apply or hid inspection:\n%s", paint)
	}
	remoteJourneyUntil(t, f.shells, func() bool {
		return f.main.Value() == "package journey\n\nvar new = 1\n" && f.peer.Value() == f.main.Value()
	})
	checked := journeySettingsControl(t, f.runtime, f.shells, one, "reviewed-edit/actions/Check result", ui.ControlOperation{Verb: "activate"})
	if checked.State != control.Committed || carrier.applies.Load() != 1 || one.app.LSPPopup.EditPreview != nil {
		t.Fatal("inspection replayed the edit or failed to confirm original commitment", checked)
	}
	if len(checked.ResourceIDs) != 2 || checked.ResourceIDs[0] != plan.ID {
		t.Fatal("original inspection lost review and retained-safety identities", checked)
	}
	actionConnectedBytes(t, f, "package journey\n\nvar new = 1\n")
}

func TestConnectedCodeActionLateListAndPeerEditCannotReviveChooser(t *testing.T) {
	f, _ := newActionConnectedFixture(t, actionConnectedRaw)
	one, two := f.shells[0], f.shells[1]
	old := actionConnectedOpen(t, f)
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEsc})
	current := actionConnectedOpen(t, f)
	_, cmd := one.app.Update(codeActionListResultMsg{owner: old, set: old.set})
	runAppCommand(t, one.app, cmd)
	if one.app.codeActions != current || current.set.ID == old.set.ID {
		t.Fatal("late old list replaced a fresh identical-source invocation")
	}
	renameConnectedKey(t, two, tea.KeyPressMsg{Code: 'X', Text: "X"})
	remoteJourneyUntil(t, f.shells, func() bool {
		return one.app.codeActions == nil && f.main.Value() == "packageX journey\n\nvar old = 1\n"
	})
	if paint := ansi.Strip(one.app.View().RenderContent()); strings.Contains(paint, "Preview changes") || strings.Contains(paint, "Replace old with new") {
		t.Fatal("peer canonical edit retained actionable old evidence")
	}
	disk, err := os.ReadFile(f.engine.main)
	if err != nil || string(disk) != renameConnectedMain {
		t.Fatal("retiring a list saved or lost the peer's unsaved source", string(disk), err)
	}
}

func TestConnectedCodeActionCtrlEnterUsesTheDisplayedTerminalChord(t *testing.T) {
	f, _ := newActionConnectedFixture(t, actionConnectedRaw)
	one := f.shells[0]
	profile := ui.CurrentKeyboardProfile()
	ui.SetKeyboardProfile(ui.KeyboardProfile{ModifiedEnter: true, AltEnterReserved: true})
	t.Cleanup(func() { ui.SetKeyboardProfile(profile) })
	menu := one.app.buildEditorContextMenu(tea.Mouse{})
	found := false
	for _, section := range menu.Sections {
		for _, item := range section.Items {
			if item.Label == "Code actions" {
				found = true
				if item.Detail != "Ctrl+Enter" {
					t.Fatalf("context menu advertised a terminal-reserved chord: %q", item.Detail)
				}
			}
		}
	}
	if !found {
		t.Fatal("connected editor omitted its Code actions entry point")
	}
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModCtrl})
	remoteJourneyUntil(t, f.shells, func() bool {
		return one.app.codeActions != nil && one.app.codeActions.set.ID != ""
	})
	if paint := ansi.Strip(one.app.View().RenderContent()); !strings.Contains(paint, "Replace old with new") {
		t.Fatalf("displayed Ctrl+Enter did not open the same native chooser:\n%s", paint)
	}
	actionConnectedBytes(t, f, renameConnectedMain)
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEsc})
}
