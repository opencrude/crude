package viewmodel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/core"
	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/screens/termdock"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
	"crude/workspace"
)

const renameConnectedMain = "package journey\n\nvar old = 1\n"
const renameConnectedPeer = "package journey\n\nvar use = old\n"

// These are original provider JSON, not protocol.WorkspaceEdit values. Only the
// temporary URIs and the provider's actual wire clocks are substituted.
const renameConnectedRaw = `{"documentChanges":[
{"textDocument":{"uri":$MAIN,"version":$MAIN_VERSION},"edits":[{"range":{"start":{"line":2,"character":4},"end":{"line":2,"character":7}},"newText":"new"}]},
{"textDocument":{"uri":$PEER,"version":$PEER_VERSION},"edits":[{"range":{"start":{"line":2,"character":10},"end":{"line":2,"character":13}},"newText":"new"}]}]}`

type renameConnectedDocument struct {
	version  int
	text     string
	boundary types.InsightBoundary
}

type renameConnectedRuntime struct {
	mu      sync.Mutex
	clients []*renameConnectedClient
	raw     string
	main    string
	peer    string
	calls   int
}

type renameConnectedClient struct {
	noopLSPClient
	owner *renameConnectedRuntime
	docs  map[string]renameConnectedDocument // guarded by owner.mu, per client
}

func (r *renameConnectedRuntime) NewClient(_ types.LSPServerSpec, _ string, admit func() error) workspace.LSPClient {
	r.mu.Lock()
	defer r.mu.Unlock()
	c := &renameConnectedClient{noopLSPClient: noopLSPClient{admit: admit}, owner: r, docs: make(map[string]renameConnectedDocument)}
	r.clients = append(r.clients, c)
	return c
}

func (c *renameConnectedClient) sync(path, text string, boundary types.InsightBoundary) (int, error) {
	c.owner.mu.Lock()
	defer c.owner.mu.Unlock()
	d := c.docs[path]
	d.version++
	d.text, d.boundary = text, boundary
	c.docs[path] = d
	return d.version, nil
}

func (c *renameConnectedClient) OpenFile(_ context.Context, path, _, text string, boundary types.InsightBoundary) (int, error) {
	return c.sync(path, text, boundary)
}

func (c *renameConnectedClient) UpdateFile(_ context.Context, path, text string, boundary types.InsightBoundary) (int, error) {
	return c.sync(path, text, boundary)
}

func (c *renameConnectedClient) RequestRename(_ context.Context, path string, line, col int, name string) (types.LSPRenameEdit, error) {
	r := c.owner
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	main, peer := c.docs[r.main], c.docs[r.peer]
	if path != r.main || line != 2 || col != 4 || name != "new" || main.text != renameConnectedMain || peer.text != renameConnectedPeer {
		return types.LSPRenameEdit{}, fmt.Errorf("unexpected provider input: %s %d:%d %q; source=%q peer=%q", path, line, col, name, main.text, peer.text)
	}
	uri := func(path string) string {
		return strconv.Quote((&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String())
	}
	raw := strings.NewReplacer("$MAIN_VERSION", strconv.Itoa(main.version), "$PEER_VERSION", strconv.Itoa(peer.version),
		"$MAIN", uri(r.main), "$PEER", uri(r.peer)).Replace(r.raw)
	return types.LSPRenameEdit{JSON: []byte(raw), WireVersion: main.version}, nil
}

type renameConnectedFixture struct {
	runtime *core.Runtime
	shells  []remoteJourneyShell
	ws      *workspace.Workspace // passive owned reads only after factory setup
	engine  *renameConnectedRuntime
	main    *editor.Editor
	peer    *editor.Editor
}

func newRenameConnectedFixture(t *testing.T, raw string, missingHistory bool) renameConnectedFixture {
	t.Helper()
	root := t.TempDir()
	engine := &renameConnectedRuntime{raw: raw, main: filepath.Join(root, "main.go"), peer: filepath.Join(root, "peer.go")}
	for path, text := range map[string]string{engine.main: renameConnectedMain, engine.peer: renameConnectedPeer, filepath.Join(root, "go.mod"): "module journey\n\ngo 1.22\n"} {
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
		w.LSPMgr.SetRuntime(engine) // generic language-engine seam; no subprocess
		settings := w.AppDefaults()
		settings.LSP = types.DefaultLSPSettings()
		settings.LSP.AutoDetect, settings.LSP.AutoComplete = false, false
		settings.LSP.Languages = map[string]types.LSPLanguageSettings{"go": {
			Enabled: true, Command: executable, RootMarkers: []string{"go.mod"},
		}}
		w.SetAppDefaults(settings)
		grantSemanticFixture(t, w)
		if missingHistory {
			// Inject the absent dependency before the actor/attachments exist;
			// never mutate the live workspace to manufacture an Apply failure.
			store := w.LocalHistory
			w.LocalHistory = nil
			t.Cleanup(func() {
				if store != nil {
					if err := store.Close(); err != nil {
						t.Error(err)
					}
				}
			})
		}
	})
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	a, b := liveTestOpen(t, one.app, engine.main), liveTestOpen(t, two.app, engine.peer)
	f := renameConnectedFixture{runtime, []remoteJourneyShell{one, two}, owned, engine, a, b}
	// The owned probe waits for completed canonical synchronization, not just
	// entry into OpenFile or a sleep guessed to cover the debounce.
	for _, path := range []string{engine.peer, engine.main} {
		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		_, view, err := owned.LSPMgr.ProbeHover(ctx, path, types.LSPSourcePosition{Line: 3, Column: 1})
		cancel()
		if err != nil || view.Source.Kind != "canonical" || view.SourceText == "" {
			t.Fatalf("canonical LSP synchronization: %+v %v", view, err)
		}
	}
	remoteJourneyUntil(t, f.shells, func() bool {
		connected, state, _ := one.app.ws.LSPMgr().Status()
		_, aReady := a.CurrentInsightSource()
		_, bReady := b.CurrentInsightSource()
		return connected && state == editor.LSPConnected && aReady && bReady
	})
	a.SetCursorPosition(2, 4)
	b.SetCursorPosition(0, 7)
	one.app.Input.SetInput("private first shell draft")
	two.app.Input.SetInput("private second shell draft")
	one.app.setFocusEditor()
	two.app.setFocusEditor()
	f.assertBytes(t, renameConnectedMain, renameConnectedPeer)
	f.assertPaint(t, "var old = 1", "var use = old")
	return f
}

func TestConnectedRenameSemanticExactReviewAndCommittedReceipt(t *testing.T) {
	f := newRenameConnectedFixture(t, renameConnectedRaw, false)
	one, two := f.shells[0], f.shells[1]
	_ = f.preview(t)
	page, err := one.app.inspectShellControls(one.app.ws.(shellport.ShellControl).ControlIdentity(), control.PageRequest{Limit: 128})
	if err != nil {
		t.Fatal(err)
	}
	oldID := ""
	for _, node := range page.Nodes {
		if strings.HasSuffix(node.ID, "::reviewed-edit/actions/Apply rename") {
			oldID = node.ID
		}
	}
	if oldID == "" {
		t.Fatal("reviewed Apply button is not described by its mounted owner")
	}
	cancelled := journeySettingsControl(t, f.runtime, f.shells, one, "reviewed-edit/actions/Cancel", ui.ControlOperation{Verb: "activate"})
	if cancelled.State != control.Applied {
		t.Fatal("semantic Cancel did not settle without mutation", cancelled)
	}
	plan := f.preview(t)
	current := one.app.LSPPopup.EditPreview
	stale := requestShellForTest(t, f.runtime, f.shells, one, "shell.describe_control",
		shellControlIntent{ComponentID: oldID, Operation: ui.ControlOperation{Verb: "activate"}}, nil)
	if stale.State == control.Applied || one.app.LSPPopup.EditPreview != current {
		t.Fatal("retired review control acquired a new invocation")
	}
	foreign := requestShellForTest(t, f.runtime, f.shells, two, "shell.describe_control",
		shellControlIntent{ComponentID: oldID, Operation: ui.ControlOperation{Verb: "activate"}}, nil)
	if foreign.State == control.Applied {
		t.Fatal("another shell acquired private review activation")
	}
	result := journeySettingsControl(t, f.runtime, f.shells, one, "reviewed-edit/actions/Apply rename", ui.ControlOperation{Verb: "activate"})
	if result.State != control.Committed || len(result.ResourceIDs) != 2 || result.ResourceIDs[0] != plan.ID || result.ResourceIDs[1] == "" {
		t.Fatalf("semantic action did not await the exact disk/safety receipt: %+v", result)
	}
	liveTestPump(t, []*App{one.app, two.app}, nil)
	f.assertBytes(t, "package journey\n\nvar new = 1\n", "package journey\n\nvar use = new\n")
	f.assertPaint(t, "var new = 1", "var use = new")
	if one.app.LSPPopup.EditPreview != nil {
		t.Fatal("known committed receipt did not settle the actual review")
	}
}

func renameConnectedKey(t *testing.T, shell remoteJourneyShell, key tea.KeyPressMsg) {
	t.Helper()
	_, cmd := shell.app.Update(key)
	runAppCommand(t, shell.app, cmd)
}

func (f renameConnectedFixture) prompt(t *testing.T) {
	t.Helper()
	one := f.shells[0]
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyF2})
	if paint := ansi.Strip(one.app.View().RenderContent()); !strings.Contains(paint, "Rename symbol") {
		t.Fatalf("F2 did not paint the rename name prompt:\n%s", paint)
	}
	for _, r := range "new" {
		renameConnectedKey(t, one, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if paint := ansi.Strip(one.app.View().RenderContent()); !strings.Contains(paint, "new") {
		t.Fatal("typed name is not visible")
	}
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
}

func (f renameConnectedFixture) preview(t *testing.T) workspace.ReviewedEditPreview {
	t.Helper()
	f.prompt(t)
	one := f.shells[0]
	remoteJourneyUntil(t, f.shells, func() bool {
		return one.app.LSPPopup.EditPreview != nil || f.hasNotice("Rename failed")
	})
	if one.app.LSPPopup.EditPreview == nil {
		t.Fatalf("rename unexpectedly failed: %+v", one.app.ws.Status().Notifications(true))
	}
	p := one.app.LSPPopup.EditPreview.request.preview
	paint := ansi.Strip(one.app.View().RenderContent())
	for _, want := range []string{"Review rename", "Apply rename", "Cancel", `- "old"`, `+ "new"`, "Local History"} {
		if !strings.Contains(paint, want) {
			t.Fatalf("immutable review does not paint %q:\n%s", want, paint)
		}
	}
	if len(p.Files) != 2 || p.ID == "" || p.Digest == "" {
		t.Fatalf("incomplete review: %+v", p)
	}
	f.assertBytes(t, renameConnectedMain, renameConnectedPeer)
	if f.main.Value() != renameConnectedMain || f.peer.Value() != renameConnectedPeer {
		t.Fatal("preview mutated an editor")
	}
	return p
}

func (f renameConnectedFixture) assertBytes(t *testing.T, main, peer string) {
	t.Helper()
	for path, expected := range map[string]string{f.engine.main: main, f.engine.peer: peer} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != expected {
			t.Fatalf("disk %s=%q, want %q (%v)", filepath.Base(path), data, expected, err)
		}
	}
}

func (f renameConnectedFixture) assertPaint(t *testing.T, main, peer string) {
	t.Helper()
	for i, expected := range []string{main, peer} {
		paint := ansi.Strip(f.shells[i].app.View().RenderContent())
		if !strings.Contains(paint, expected) {
			t.Fatalf("shell %d does not paint %q:\n%s", i, expected, paint)
		}
	}
}

func (f renameConnectedFixture) hasNotice(text string) bool {
	for _, notice := range f.shells[0].app.ws.Status().Notifications(true) {
		if strings.Contains(notice.Summary, text) {
			return true
		}
	}
	return false
}

func (f renameConnectedFixture) assertFailurePaint(t *testing.T, text string) {
	t.Helper()
	remoteJourneyUntil(t, f.shells, func() bool { return f.hasNotice(text) })
	one := f.shells[0]
	one.app.toggleWorkspacePanel(termdock.PanelNotices)
	if paint := ansi.Strip(one.app.View().RenderContent()); !strings.Contains(paint, "Rename failed") {
		t.Fatalf("failure is absent from the rendered Notices panel:\n%s", paint)
	}
	one.app.hideWorkspacePanel()
}

func TestConnectedRenameKeysCancelApplySafetyAndInspect(t *testing.T) {
	f := newRenameConnectedFixture(t, renameConnectedRaw, false)
	one, two := f.shells[0], f.shells[1]
	port, foreign := one.app.ws.(shellport.ReviewedEdits), two.app.ws.(shellport.ReviewedEdits)
	cancelled := f.preview(t)
	// Enter without navigation must select Cancel, never Apply.
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
	remoteJourneyUntil(t, f.shells, func() bool { return one.app.LSPPopup.EditPreview == nil })
	f.assertBytes(t, renameConnectedMain, renameConnectedPeer)
	f.assertPaint(t, "var old = 1", "var use = old")
	if result, err := port.InspectReviewedEdit(t.Context(), cancelled.ID, cancelled.Digest); err != nil || result.State == "committed" || result.SafetySnapshotID != "" {
		t.Fatalf("Cancel admitted an effect: %+v %v", result, err)
	}
	p := f.preview(t)
	if p.ID == cancelled.ID {
		t.Fatal("new review reused cancelled operation")
	}
	if result, err := foreign.ApplyReviewedEdit(t.Context(), p.ID, p.Digest); err != nil || result.State != "refused" {
		t.Fatalf("peer acquired private review token: %+v %v", result, err)
	}
	f.assertBytes(t, renameConnectedMain, renameConnectedPeer)
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyLeft})
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
	remoteJourneyUntil(t, f.shells, func() bool {
		return one.app.LSPPopup.EditPreview == nil && f.main.Value() == "package journey\n\nvar new = 1\n" &&
			f.peer.Value() == "package journey\n\nvar use = new\n" && !f.main.Modified() && !f.peer.Modified()
	})
	f.assertBytes(t, "package journey\n\nvar new = 1\n", "package journey\n\nvar use = new\n")
	f.assertPaint(t, "var new = 1", "var use = new")
	result, err := port.InspectReviewedEdit(t.Context(), p.ID, p.Digest)
	if err != nil || result.ID != p.ID || result.Digest != p.Digest || result.State != "committed" || result.SafetySnapshotID == "" || result.SafetyReference == "" {
		t.Fatalf("original operation has no exact safe outcome: %+v %v", result, err)
	}
	if f.ws.LoadAppSettings().LocalHistory.Enabled != "off" {
		t.Fatal("fixture accidentally enabled automatic history")
	}
	safety, err := f.ws.LocalHistory.LoadRetainedSnapshot(t.Context(), result.SafetyReference)
	if err != nil || safety.ID != result.SafetySnapshotID {
		t.Fatalf("safety reference is not retained: %+v %v", safety, err)
	}
	for path, before := range map[string]string{"main.go": renameConnectedMain, "peer.go": renameConnectedPeer} {
		hash := sha256.Sum256([]byte(before))
		if safety.Entries[path].Hash != hex.EncodeToString(hash[:]) {
			t.Fatalf("safety %s did not retain exact original bytes", path)
		}
	}
	if again, err := port.InspectReviewedEdit(t.Context(), p.ID, p.Digest); err != nil || again != result {
		t.Fatalf("Inspect changed original outcome: %+v %v", again, err)
	}
	if _, err := foreign.InspectReviewedEdit(t.Context(), p.ID, p.Digest); err == nil {
		t.Fatal("peer inspected another attachment's completed operation")
	}
	f.engine.mu.Lock()
	calls := f.engine.calls
	f.engine.mu.Unlock()
	if calls != 2 {
		t.Fatalf("Cancel/Apply/Inspect replayed provider requests: %d", calls)
	}
	if one.app.Input.Value() != "private first shell draft" || two.app.Input.Value() != "private second shell draft" ||
		f.main.CursorLine() != 2 || f.main.CursorCol() != 4 || f.peer.CursorLine() != 0 || f.peer.CursorCol() != 7 {
		t.Fatalf("rename changed private state: drafts=%q/%q cursors=%d:%d/%d:%d", one.app.Input.Value(), two.app.Input.Value(),
			f.main.CursorLine(), f.main.CursorCol(), f.peer.CursorLine(), f.peer.CursorCol())
	}
}

func TestConnectedRenamePeerChangesInvalidateImmutableReview(t *testing.T) {
	for _, undo := range []bool{false, true} {
		t.Run(fmt.Sprintf("peer-undo-%t", undo), func(t *testing.T) {
			f := newRenameConnectedFixture(t, renameConnectedRaw, false)
			p := f.preview(t)
			two := f.shells[1]
			// A different target changes, so the invoking editor itself stays
			// current. The real core, not a shell-local stale check, must refuse.
			renameConnectedKey(t, two, tea.KeyPressMsg{Code: 'X', Text: "X"})
			remoteJourneyUntil(t, f.shells, func() bool { return !two.app.liveEditors[f.peer].sync && f.peer.DocumentState().PendingEdits == 0 })
			if undo {
				renameConnectedKey(t, two, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
				remoteJourneyUntil(t, f.shells, func() bool {
					return f.peer.Value() == renameConnectedPeer && !f.peer.DocumentState().HistoryQueued && !f.peer.DocumentState().UndoPending
				})
			}
			one := f.shells[0]
			if one.app.LSPPopup.EditPreview.request.preview.Text != p.Text {
				t.Fatal("peer mutation rewrote immutable review")
			}
			renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyLeft})
			renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
			remoteJourneyUntil(t, f.shells, func() bool {
				return strings.Contains(ansi.Strip(one.app.View().RenderContent()), "Rename refused without a disk effect")
			})
			f.assertBytes(t, renameConnectedMain, renameConnectedPeer)
			if f.main.Value() != renameConnectedMain || (!undo && f.peer.Value() != "packageX journey\n\nvar use = old\n") || (undo && f.peer.Value() != renameConnectedPeer) {
				t.Fatalf("refusal lost peer work: %q / %q", f.main.Value(), f.peer.Value())
			}
			if undo {
				f.assertPaint(t, "var old = 1", "var use = old")
			} else {
				f.assertPaint(t, "var old = 1", "packageX journey")
			}
		})
	}
}

func TestConnectedRenameMissingHistoryFailsClosed(t *testing.T) {
	f := newRenameConnectedFixture(t, renameConnectedRaw, true)
	f.prompt(t)
	f.assertFailurePaint(t, "recoverable Local History")
	if f.shells[0].app.LSPPopup.EditPreview != nil {
		t.Fatal("absent history dependency offered an applicable preview")
	}
	f.assertBytes(t, renameConnectedMain, renameConnectedPeer)
	f.assertPaint(t, "var old = 1", "var use = old")
}

func TestConnectedRenameRawProviderUnsupportedEditsFailClosed(t *testing.T) {
	cases := map[string]string{
		"null-version-open": strings.NewReplacer("$MAIN_VERSION", "null", "$PEER_VERSION", "null").Replace(renameConnectedRaw),
		"missing-version":   strings.Replace(renameConnectedRaw, `,"version":$MAIN_VERSION`, "", 1),
		"annotation":        strings.Replace(renameConnectedRaw, `"newText":"new"`, `"newText":"new","annotationId":"approval"`, 1),
		"resource":          `{"documentChanges":[{"kind":"rename","oldUri":$MAIN,"newUri":$PEER}]}`,
		"both-unsupported": `{"changes":{$MAIN:[{"range":{"start":{"line":2,"character":4},"end":{"line":2,"character":7}},"newText":"new","annotationId":"hidden"}]},
			"documentChanges":[{"textDocument":{"uri":$MAIN,"version":$MAIN_VERSION},"edits":[{"range":{"start":{"line":2,"character":4},"end":{"line":2,"character":7}},"newText":"new"}]}]}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			f := newRenameConnectedFixture(t, raw, false)
			f.prompt(t)
			f.assertFailurePaint(t, "Rename failed")
			if f.shells[0].app.LSPPopup.EditPreview != nil {
				t.Fatal("unsupported provider edit offered partial Apply")
			}
			f.assertBytes(t, renameConnectedMain, renameConnectedPeer)
			f.assertPaint(t, "var old = 1", "var use = old")
		})
	}
}
