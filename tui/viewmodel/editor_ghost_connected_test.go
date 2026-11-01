package viewmodel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/core"
	"crude/document"
	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/workspace"
	"github.com/charmbracelet/x/ansi"
)

// Candidates below are explicitly INTERNAL fixture injections, not automatic
// prediction/provider results. Everything after preparation uses native shell
// input, actual framed remote ports, core receipts and applied document topics.
// No model is queried, and these tests establish no provider/consent acceptance.
type connectedGhostJourney struct {
	runtime *core.Runtime
	root    string
	path    string
	one     remoteJourneyShell
	two     remoteJourneyShell
	a, b    *editor.Editor
}

func newConnectedGhostJourney(t *testing.T) *connectedGhostJourney {
	t.Helper()
	j := &connectedGhostJourney{runtime: remoteJourneyRuntime(t, "http://127.0.0.1:1"), root: t.TempDir()}
	j.path = filepath.Join(j.root, "ghost.txt")
	if err := os.WriteFile(j.path, []byte("base"), 0600); err != nil {
		t.Fatal(err)
	}
	j.one, j.two = newRemoteJourneyShell(t, j.runtime, j.root), newRemoteJourneyShell(t, j.runtime, j.root)
	j.a, j.b = liveTestOpen(t, j.one.app, j.path), liveTestOpen(t, j.two.app, j.path)
	j.one.app.setFocusEditor()
	j.two.app.setFocusEditor()
	j.one.app.Input.SetInput("private ghost one")
	j.two.app.Input.SetInput("private ghost two")
	j.b.SetCursorPosition(0, 0)
	j.settle(t, "base")
	return j
}

func (j *connectedGhostJourney) settle(t *testing.T, want string) {
	t.Helper()
	apps := []*App{j.one.app, j.two.app}
	for _, app := range apps {
		runAppCommand(t, app, app.drainLiveEditorCommands())
	}
	liveTestPump(t, apps, nil)
	for _, app := range apps {
		ed := app.EditorState.Editor
		s := ed.DocumentState()
		if ed.Value() != want || string(ed.DocumentSnapshot().Text) != want ||
			s.PendingEdits != 0 || s.InFlight || s.UndoPending || s.HistoryQueued || s.Conflict != nil ||
			app.liveEditors[ed].err != nil || app.liveEditors[ed].sync {
			t.Fatalf("canonical text did not settle to %q: value=%q canonical=%q state=%+v live=%+v",
				want, ed.Value(), ed.DocumentSnapshot().Text, s, app.liveEditors[ed])
		}
		if paint := ansi.Strip(app.View().RenderContent()); !strings.Contains(paint, want) {
			t.Fatalf("settled canonical text %q missing from actual shell paint:\n%s", want, paint)
		}
	}
	if _, rest, present := j.b.GhostText(); present {
		t.Fatalf("peer acquired private prediction %q", rest)
	}
	if j.one.app.Input.Value() != "private ghost one" || j.two.app.Input.Value() != "private ghost two" {
		t.Fatal("connected editor interaction consumed a private composer draft")
	}
}

func connectedGhostKey(t *testing.T, shell remoteJourneyShell, key tea.KeyPressMsg) {
	t.Helper()
	_, cmd := shell.app.Update(key)
	runAppCommand(t, shell.app, cmd)
}

func connectedGhostRest(t *testing.T, ed *editor.Editor, anchor int, want string) {
	t.Helper()
	gotAnchor, got, present := ed.GhostText()
	if present != (want != "") || got != want || present && gotAnchor != anchor {
		t.Fatalf("private remainder=(%d,%q,%t), want=(%d,%q,%t)", gotAnchor, got, present, anchor, want, want != "")
	}
}

func (j *connectedGhostJourney) offer(t *testing.T, anchor int, text string) {
	t.Helper()
	j.a.SetCursorPosition(0, anchor)
	if err := j.a.SetGhostText(j.a.Revision(), anchor, text); err != nil {
		t.Fatal("internal candidate preparation:", err)
	}
	// A real shell event adopts the private key lifetime; paint is not its owner.
	_, cmd := j.one.app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	runAppCommand(t, j.one.app, cmd)
	connectedGhostRest(t, j.a, anchor, text)
}

// Hold the actual command result and actual topic independently, without a
// synthetic AcceptDocumentReceipt or fabricated publication. Other command and
// FIFO traffic still goes through Update. Commands execute off Update just as
// runAppCommand does; only installation of this one edit's evidence is delayed.
func connectedGhostHold(t *testing.T, shell remoteJourneyShell, ed *editor.Editor, cmd tea.Cmd) (liveEditorResult, shellport.LiveDocumentsChangedMsg) {
	t.Helper()
	results := make(chan tea.Msg, 64)
	pending, launched := 0, 0
	launch := func(command tea.Cmd) {
		if command == nil {
			return
		}
		launched++
		if launched > cap(results) {
			t.Fatal("held connected command did not settle")
		}
		pending++
		go func() { results <- command() }()
	}
	launch(cmd)
	var receipt liveEditorResult
	var topic shellport.LiveDocumentsChangedMsg
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	for pending > 0 || receipt.kind == "" || topic.DocumentID == "" {
		var msg tea.Msg
		select {
		case msg = <-results:
			pending--
		case msg = <-shell.fifo:
		case <-deadline.C:
			t.Fatalf("missing real held evidence: pending=%d receipt=%+v topic=%+v", pending, receipt, topic)
		}
		switch m := msg.(type) {
		case tea.BatchMsg:
			for _, command := range m {
				launch(command)
			}
		case liveEditorResult:
			if m.ed != ed || m.kind != "edit" || receipt.kind != "" {
				t.Fatalf("unexpected held command result: %+v", m)
			}
			receipt = m
		case shellport.LiveDocumentsChangedMsg:
			if m.DocumentID == ed.DocumentState().DocumentID && m.Update.State.Revision > ed.DocumentState().Revision {
				if topic.DocumentID != "" {
					t.Fatal("second text publication overtook an unacknowledged first publication")
				}
				topic = m
			} else {
				_, next := shell.app.Update(m)
				launch(next)
			}
		default:
			if msg != nil {
				_, next := shell.app.Update(msg)
				launch(next)
			}
		}
	}
	if receipt.err != nil || !receipt.receipt.Accepted || receipt.receipt.RequestID != receipt.requestID ||
		topic.Update.State.Revision != receipt.receipt.Revision || topic.Done == nil || topic.Valid == nil || !topic.Valid() {
		t.Fatalf("not exact accepted core evidence: receipt=%+v topic=%+v", receipt, topic)
	}
	return receipt, topic
}

func connectedGhostSparse(t *testing.T, receipt liveEditorResult, topic shellport.LiveDocumentsChangedMsg, anchor int, text string) {
	t.Helper()
	changes := topic.Update.Changes
	if topic.Update.Snapshot != nil || len(changes) != 1 || changes[0].RequestID != receipt.requestID ||
		len(changes[0].Edits) != 1 || changes[0].Edits[0] != (document.Edit{Start: anchor, End: anchor, Text: text}) {
		t.Fatalf("native acceptance was not one exact sparse insertion at %d of %q: %+v", anchor, text, topic.Update)
	}
}

func TestConnectedGhostPrivatePaintLiteralTabHistorySaveAndReopen(t *testing.T) {
	j := newConnectedGhostJourney(t)
	before, diskGeneration := j.a.DocumentSnapshot(), j.a.DiskGeneration()
	j.offer(t, 4, `("literal`)
	if j.a.Value() != "base" || j.b.Value() != "base" || j.a.Modified() || j.b.Modified() ||
		j.a.DocumentState().PendingEdits != 0 || j.a.DocumentState().Revision != before.Revision ||
		string(j.a.DocumentSnapshot().Saved) != "base" || j.a.DiskGeneration() != diskGeneration {
		t.Fatal("private candidate changed canonical source, savepoint or dirty state before acceptance")
	}
	if disk, err := os.ReadFile(j.path); err != nil || string(disk) != "base" {
		t.Fatalf("candidate leaked to disk: %q %v", disk, err)
	}
	paint := ansi.Strip(j.one.app.View().RenderContent())
	if !strings.Contains(paint, `base("literal`) || strings.Contains(ansi.Strip(j.two.app.View().RenderContent()), `("literal`) {
		t.Fatal("candidate did not paint exclusively in its invoking shell")
	}
	// Inspect the actual mounted shell cells, not a separate editor render.
	buf := j.one.app.mainSurf.Buffer()
	foundMuted := false
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x+6 < buf.Width(); x++ {
			if buf.Get(x, y).Char != 'l' {
				continue
			}
			match := true
			for i, r := range "literal" {
				c := buf.Get(x+i, y)
				match = match && c.Char == r && c.FG == j.one.app.rootHost.Tokens().Color(ui.ColorMuted)
			}
			foundMuted = foundMuted || match
		}
	}
	if !foundMuted {
		t.Fatal("candidate's mounted glyphs do not use the muted design token")
	}
	_, cmd := j.one.app.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if j.a.Value() != `base("literal` || j.b.Value() != "base" || j.b.CursorCol() != 0 {
		t.Fatal("Tab did not optimistically insert exact literal bytes without auto-pairs")
	}
	receipt, topic := connectedGhostHold(t, j.one, j.a, cmd)
	connectedGhostSparse(t, receipt, topic, 4, `("literal`)
	_, next := j.one.app.Update(receipt)
	runAppCommand(t, j.one.app, next)
	_, next = j.one.app.Update(topic)
	runAppCommand(t, j.one.app, next)
	j.settle(t, `base("literal`)
	connectedGhostRest(t, j.a, 0, "")
	if j.b.CursorLine() != 0 || j.b.CursorCol() != 0 {
		t.Fatal("peer publication moved its unrelated private cursor")
	}
	if disk, err := os.ReadFile(j.path); err != nil || string(disk) != "base" {
		t.Fatalf("unsaved acceptance wrote disk: %q %v", disk, err)
	}
	connectedGhostKey(t, j.one, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	j.settle(t, "base")
	connectedGhostRest(t, j.a, 0, "")
	connectedGhostKey(t, j.one, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl | tea.ModShift})
	j.settle(t, `base("literal`)
	connectedGhostRest(t, j.a, 0, "")
	j.one.app.requestEditorSaveWithFormat(j.a, workspace.DocumentSaveManual, false, false)
	j.settle(t, `base("literal`)
	if j.a.Modified() || j.b.Modified() {
		t.Fatal("canonical savepoint did not clear both dirty projections")
	}
	disk, err := os.ReadFile(j.path)
	if err != nil || string(disk) != `base("literal` {
		t.Fatalf("saved literal differs: %q %v", disk, err)
	}
	three := newRemoteJourneyShell(t, j.runtime, j.root)
	reopened := liveTestOpen(t, three.app, j.path)
	liveTestPump(t, []*App{j.one.app, j.two.app, three.app}, nil)
	if reopened.Value() != `base("literal` || reopened.Modified() || string(reopened.DocumentSnapshot().Saved) != `base("literal` {
		t.Fatal("reopened canonical document did not retain exact saved literal")
	}
	connectedGhostRest(t, reopened, 0, "")
}

func TestConnectedGhostTypedPrefixAndWordSurviveActualReceiptPublicationOrders(t *testing.T) {
	for _, order := range []string{"receipt-first", "publication-first"} {
		t.Run(order, func(t *testing.T) {
			j := newConnectedGhostJourney(t)
			j.offer(t, 4, "alpha + beta")
			for _, step := range []struct {
				key                 tea.KeyPressMsg
				insert, value, rest string
				anchor, nextAnchor  int
			}{
				{tea.KeyPressMsg{Code: 'a', Text: "al"}, "al", "baseal", "pha + beta", 4, 6},
				{tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl}, "pha", "basealpha", " + beta", 6, 9},
				{tea.KeyPressMsg{Code: tea.KeyRight, Mod: tea.ModCtrl}, " +", "basealpha +", " beta", 9, 11},
			} {
				_, cmd := j.one.app.Update(step.key)
				if j.a.Value() != step.value {
					t.Fatalf("native partial input text=%q, want %q", j.a.Value(), step.value)
				}
				connectedGhostRest(t, j.a, step.nextAnchor, step.rest)
				receipt, topic := connectedGhostHold(t, j.one, j.a, cmd)
				connectedGhostSparse(t, receipt, topic, step.anchor, step.insert)
				acked := false
				done := topic.Done
				topic.Done = func(err error) {
					if err != nil {
						t.Errorf("real canonical publication refused: %v", err)
					}
					acked = true
					done(err)
				}
				first, second := tea.Msg(receipt), tea.Msg(topic)
				if order == "publication-first" {
					first, second = topic, receipt
				}
				_, next := j.one.app.Update(first)
				runAppCommand(t, j.one.app, next)
				if acked {
					t.Fatal("topic ACK released before receipt and canonical installation both existed")
				}
				connectedGhostRest(t, j.a, step.nextAnchor, step.rest)
				_, next = j.one.app.Update(second)
				runAppCommand(t, j.one.app, next)
				if !acked {
					t.Fatal("matching installed canonical publication did not release actual applied ACK")
				}
				j.settle(t, step.value)
				connectedGhostRest(t, j.a, step.nextAnchor, step.rest)
				if j.b.CursorCol() != 0 {
					t.Fatal("partial acceptance changed peer's private cursor")
				}
			}
			connectedGhostKey(t, j.one, tea.KeyPressMsg{Code: tea.KeyTab})
			j.settle(t, "basealpha + beta")
			connectedGhostRest(t, j.a, 0, "")
			if disk, err := os.ReadFile(j.path); err != nil || string(disk) != "base" {
				t.Fatalf("partial native acceptance implicitly saved: %q %v", disk, err)
			}
		})
	}
}

func TestConnectedGhostPeerMutationAndExactSourceABARetireAcceptance(t *testing.T) {
	j := newConnectedGhostJourney(t)
	j.offer(t, 4, "candidate")
	revision := j.a.DocumentState().Revision
	connectedGhostKey(t, j.two, tea.KeyPressMsg{Code: '!', Text: "!"})
	j.settle(t, "!base")
	connectedGhostRest(t, j.a, 0, "")
	connectedGhostKey(t, j.two, tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	j.settle(t, "base")
	if j.a.DocumentState().Revision <= revision {
		t.Fatal("fixture did not traverse canonical source ABA revisions")
	}
	connectedGhostRest(t, j.a, 0, "")
	j.a.SetCursorPosition(0, 4)
	connectedGhostKey(t, j.one, tea.KeyPressMsg{Code: tea.KeyTab})
	j.settle(t, "base    ")
	connectedGhostRest(t, j.a, 0, "")
}

func TestConnectedGhostNativeEscapeAndOwnerDeparturesCannotReviveAcceptance(t *testing.T) {
	for _, departure := range []string{"escape", "focus", "tab", "blur", "source-replacement"} {
		t.Run(departure, func(t *testing.T) {
			j := newConnectedGhostJourney(t)
			j.offer(t, 4, "candidate")
			switch departure {
			case "escape":
				connectedGhostKey(t, j.one, tea.KeyPressMsg{Code: tea.KeyEsc})
				if !j.a.IsOpen() || j.one.app.EditorState.Editor != j.a {
					t.Fatal("first native Escape closed candidate's source tab")
				}
			case "focus":
				j.one.app.setFocusTree()
			case "tab":
				other := filepath.Join(j.root, "other.txt")
				if err := os.WriteFile(other, []byte("other"), 0600); err != nil {
					t.Fatal(err)
				}
				liveTestOpen(t, j.one.app, other)
				j.one.app.activateEditorTab(j.path)
			case "blur":
				_, cmd := j.one.app.Update(tea.BlurMsg{})
				runAppCommand(t, j.one.app, cmd)
				_, cmd = j.one.app.Update(tea.FocusMsg{})
				runAppCommand(t, j.one.app, cmd)
			case "source-replacement":
				// Exact-source replacement is an internal lifecycle seam, not a
				// fabricated core publication; the connected controller survives.
				if err := j.a.InstallDocument(j.a.DocumentSnapshot()); err != nil {
					t.Fatal(err)
				}
			}
			_, cmd := j.one.app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
			runAppCommand(t, j.one.app, cmd)
			connectedGhostRest(t, j.a, 0, "")
			j.one.app.setFocusEditor()
			j.a.SetCursorPosition(0, 4)
			connectedGhostKey(t, j.one, tea.KeyPressMsg{Code: tea.KeyTab})
			j.settle(t, "base    ")
			if strings.Contains(ansi.Strip(j.one.app.View().RenderContent()), "basecandidate") {
				t.Fatal("retired candidate paint or acceptance revived on returning to its owner")
			}
		})
	}
}

func TestConnectedGhostZAttachmentEpochReplacementRetiresCandidate(t *testing.T) {
	j := newConnectedGhostJourney(t)
	j.offer(t, 4, "candidate")
	owner := j.one.app.ws.(shellport.LiveDocuments).DocumentOwner()
	replacement := owner
	replacement.Epoch++
	// Direct binding deliberately isolates the attachment fence. This is not
	// evidence of a transport reconnect; the connected editing tests above use
	// the actual attachment owner without replacement.
	if err := j.a.BindDocumentOwner(replacement); err != nil {
		t.Fatal(err)
	}
	connectedGhostRest(t, j.a, 0, "")
	// A direct binding fixture must not fabricate a reconnect by moving the
	// attachment epoch backwards. Native editing/Tab is exercised above using
	// actual connected owners; this case isolates successful replacement only.
	if err := j.a.BindDocumentOwner(owner); err == nil {
		t.Fatal("replacement attachment allowed an older epoch to bind again")
	}
	if j.a.Value() != "base" || j.b.Value() != "base" {
		t.Fatal("replacing attachment authority mutated canonical or private source")
	}
}
