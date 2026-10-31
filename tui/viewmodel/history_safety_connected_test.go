package viewmodel

import (
	"context"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/shellport"
	historyscreen "crude/tui/screens/localhistory"
	"crude/tui/ui"
	"crude/types/control"
	"crude/workspace"
)

func committedHistorySafetyFixture(t *testing.T, width, height int) (renameConnectedFixture, workspace.ReviewedEditPreview, workspace.ReviewedEditResult) {
	t.Helper()
	f := newRenameConnectedFixture(t, renameConnectedRaw, false)
	one := f.shells[0]
	preview := f.preview(t)
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyLeft})
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
	remoteJourneyUntil(t, f.shells, func() bool {
		return one.app.LSPPopup.RenamePreview == nil && f.main.Value() == "package journey\n\nvar new = 1\n"
	})
	result, err := one.app.ws.(shellport.ReviewedEdits).InspectReviewedEdit(t.Context(), preview.ID, preview.Digest)
	if err != nil || result.State != "committed" || result.SafetyReference != preview.ID {
		t.Fatal("real key-driven reviewed mutation has no original safety receipt", result, err)
	}
	_, cmd := one.app.Update(tea.WindowSizeMsg{Width: width, Height: height})
	runAppCommand(t, one.app, cmd)
	return f, preview, result
}

// Enter through the actual command palette and timeline row. No test-only
// shortcut sets a page, authorizes a change, or patches canonical product state.
func openHistorySafetyKeys(t *testing.T, f renameConnectedFixture) {
	t.Helper()
	one := f.shells[0]
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl | tea.ModAlt})
	for _, r := range "Local History" {
		renameConnectedKey(t, one, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
	remoteJourneyUntil(t, f.shells, func() bool {
		return one.app.historyScreen != nil && strings.Contains(ansi.Strip(one.app.View().RenderContent()), "Review safety references")
	})
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
	remoteJourneyUntil(t, f.shells, func() bool {
		return one.app.historySafety != nil && len(one.app.historySafety.page.Records) != 0
	})
	paint := ansi.Strip(one.app.View().RenderContent())
	if !strings.Contains(paint, "retained") || !strings.Contains(paint, "Review safety") {
		t.Fatal("native safety list not rendered", paint)
	}
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
	remoteJourneyUntil(t, f.shells, func() bool {
		v := one.app.historySafety
		if v == nil {
			return false
		}
		_, ok := v.screen.SelectedDetail()
		return ok
	})
}

func historySafetyClick(t *testing.T, one remoteJourneyShell, action string) {
	t.Helper()
	node := localControlNode(t, one.app, string(ui.NewID(ui.NewID(historyscreen.SafetyScreenID, "actions"), action)))
	if node.Bounds == nil || node.Bounds.Width == 0 || node.Bounds.Height == 0 {
		t.Fatal("rendered native protection action has no bounds", node)
	}
	fw := one.app.overlayHost.Window(ui.OverlayLocalHistory)
	x, y := fw.Position()
	for _, msg := range []tea.Msg{
		tea.MouseClickMsg{X: x + node.Bounds.X + 1, Y: y + node.Bounds.Y, Button: tea.MouseLeft},
		tea.MouseReleaseMsg{X: x + node.Bounds.X + 1, Y: y + node.Bounds.Y, Button: tea.MouseLeft},
	} {
		_, cmd := one.app.Update(msg)
		runAppCommand(t, one.app, cmd)
	}
}

func TestConnectedHistorySafetyRenderedNativeConsentAndExactOriginal(t *testing.T) {
	for _, size := range []ui.Size{{W: 160, H: 40}, {W: 80, H: 24}} {
		t.Run(fmt.Sprintf("%dx%d", size.W, size.H), func(t *testing.T) {
			f, preview, original := committedHistorySafetyFixture(t, size.W, size.H)
			one, two := f.shells[0], f.shells[1]
			openHistorySafetyKeys(t, f)
			v := one.app.historySafety
			detail, ok := v.screen.SelectedDetail()
			if !ok || detail.Reference != preview.ID || !detail.CanRelease || detail.Result != original {
				t.Fatal("rendered detail lost original binding", detail)
			}
			paint := ansi.Strip(one.app.View().RenderContent())
			for _, want := range []string{"Review safety reference", "Release protection", "Restore preview", "Back"} {
				if !strings.Contains(paint, want) {
					t.Fatalf("missing %q in real connected paint:\n%s", want, paint)
				}
			}
			// Detail Enter is Back. The later confirmation has its own Cancel
			// default; neither safe default is a silent release.
			renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
			if _, ok := v.screen.SelectedDetail(); ok {
				t.Fatal("safe initial detail Enter did not go back")
			}
			if _, err := f.ws.LocalHistory.LoadRetainedSnapshot(t.Context(), original.SafetyReference); err != nil {
				t.Fatal("detail default released protection", err)
			}
			renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
			remoteJourneyUntil(t, f.shells, func() bool { _, ok := v.screen.SelectedDetail(); return ok })
			historySafetyClick(t, one, historyscreen.ActionSafetyRelease)
			if v.confirm == nil || !one.app.overlayHost.IsOpen(historySafetyConfirmOverlay) {
				t.Fatal("rendered release action bypassed native confirmation")
			}
			paint = ansi.Strip(one.app.View().RenderContent())
			if !strings.Contains(paint, "Release safety protection") || !strings.Contains(paint, "Cancel") {
				t.Fatal("native shared confirmation not painted", paint)
			}
			_, cmd := one.app.Update(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
			runAppCommand(t, one.app, cmd)
			_, cmd = one.app.Update(tea.PasteMsg{Content: "must not enter composer"})
			runAppCommand(t, one.app, cmd)
			if v.confirm == nil || one.app.Input.Value() != "private first shell draft" {
				t.Fatal("outside click or paste escaped protection confirmation")
			}
			renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
			if v.confirm != nil || one.app.historySafetyOperation != nil {
				t.Fatal("default Cancel admitted a protection mutation")
			}
			if _, err := f.ws.LocalHistory.LoadRetainedSnapshot(t.Context(), original.SafetyReference); err != nil {
				t.Fatal("Cancel unpinned original evidence", err)
			}
			historySafetyClick(t, one, historyscreen.ActionSafetyRelease)
			renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyLeft})
			renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
			remoteJourneyUntil(t, f.shells, func() bool {
				d, ok := v.screen.SelectedDetail()
				return ok && one.app.historySafetyOperation == nil && !d.Retained && d.CanReprotect
			})
			released, _ := v.screen.SelectedDetail()
			if released.LastEvent == nil || released.LastEvent.Action != "release" || released.Result != original {
				t.Fatal("release rewrote original operation or lost audit", released)
			}
			receipt, err := one.app.ws.(shellport.HistorySafety).HistorySafetyReceipt(t.Context(), released.LastEvent.Change.RequestID)
			if err != nil || receipt.State != "completed" || receipt.Method != coreprotocol.HistorySafetyChange {
				t.Fatal("native protection mutation has no durable exact core receipt", receipt, err)
			}
			if _, err := f.ws.LocalHistory.LoadManifest(original.SafetySnapshotID); err != nil {
				t.Fatal("release deleted the original snapshot", err)
			}
			historySafetyClick(t, one, historyscreen.ActionSafetyReprotect)
			renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyLeft})
			renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
			remoteJourneyUntil(t, f.shells, func() bool {
				d, ok := v.screen.SelectedDetail()
				return ok && one.app.historySafetyOperation == nil && d.Retained && d.Revision == 2
			})
			reprotected, _ := v.screen.SelectedDetail()
			if reprotected.Result != original || reprotected.LastEvent.Action != "reprotect" {
				t.Fatal("re-protection failed to retain original provenance", reprotected)
			}
			f.assertBytes(t, "package journey\n\nvar new = 1\n", "package journey\n\nvar use = new\n")
			if one.app.Input.Value() != "private first shell draft" || two.app.Input.Value() != "private second shell draft" ||
				f.main.CursorLine() != 2 || f.main.CursorCol() != 4 || f.peer.CursorLine() != 0 || f.peer.CursorCol() != 7 {
				t.Fatal("protection administration touched private editor/composer state")
			}
			foreign, err := two.app.ws.(shellport.HistorySafety).InspectHistorySafety(t.Context(), preview.ID)
			if err != nil || foreign.Result != original || !foreign.CanRelease {
				t.Fatal("replacement workspace shell cannot inspect original retention evidence", foreign, err)
			}
			if _, err := two.app.ws.(shellport.ReviewedEdits).InspectReviewedEdit(t.Context(), preview.ID, preview.Digest); err == nil {
				t.Fatal("history administration renewed foreign original Apply/Inspect authority")
			}
			historySafetyClick(t, one, historyscreen.ActionSafetyRestore)
			remoteJourneyUntil(t, f.shells, func() bool {
				return one.app.historySafety == nil && strings.Contains(ansi.Strip(one.app.View().RenderContent()), "Restore preview")
			})
			f.assertBytes(t, "package journey\n\nvar new = 1\n", "package journey\n\nvar use = new\n")
			renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEsc})
			if _, err := f.ws.LocalHistory.LoadRetainedSnapshot(t.Context(), original.SafetyReference); err != nil {
				t.Fatal("restoration preview consumed protection", err)
			}
		})
	}
}

func TestConnectedHistorySafetyGenericControlCannotGrantHumanConsent(t *testing.T) {
	f, _, original := committedHistorySafetyFixture(t, 100, 32)
	one := f.shells[0]
	one.app.ws.(shellport.ShellControl).StartShellControl()
	requestShellForTest(t, f.runtime, f.shells, one, "shell.inspect", struct{}{}, nil)
	openHistorySafetyKeys(t, f)
	writer := analysisControlGateway(t, f.runtime, one, one.app.ws.RootPath(), []control.Effect{control.Read, control.Write, control.Delete})
	releaseID := string(ui.NewID(ui.NewID(historyscreen.SafetyScreenID, "actions"), historyscreen.ActionSafetyRelease))
	node := localControlNode(t, one.app, releaseID)
	op := ui.ControlOperation{Verb: "activate"}
	desc, _, err := one.app.describeShellControl(shellControlIntent{ComponentID: node.ID, Operation: op}, one.app.ws.(shellport.ShellControl).ControlIdentity())
	if err != nil || !desc.HumanOnly || desc.Effect != control.Write {
		t.Fatal("actual owner release action lost human-only metadata", desc, err)
	}
	result := writer(node, op, desc.ID)
	if result.RPCFault == nil && result.State != control.Failed || one.app.historySafety.confirm != nil {
		t.Fatal("ordinary generic grants opened human-only retention consent", result)
	}
	historySafetyClick(t, one, historyscreen.ActionSafetyRelease)
	node = localControlNode(t, one.app, string(ui.ConfirmButtonID(0)))
	desc, _, err = one.app.describeShellControl(shellControlIntent{ComponentID: node.ID, Operation: op}, one.app.ws.(shellport.ShellControl).ControlIdentity())
	if err != nil || !desc.HumanOnly {
		t.Fatal("generic confirm activation bypasses human-only protection", desc, err)
	}
	result = writer(node, op, desc.ID)
	if result.RPCFault == nil && result.State != control.Failed || one.app.historySafetyOperation != nil {
		t.Fatal("generic shared-confirm route granted retention consent", result)
	}
	if _, err := f.ws.LocalHistory.LoadRetainedSnapshot(t.Context(), original.SafetyReference); err != nil {
		t.Fatal("denied generic consent released protection", err)
	}
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEsc})
}

type lostHistorySafetyReply struct {
	shellport.HistorySafety
	requestID string
	calls     int
}

func (l *lostHistorySafetyReply) ChangeHistorySafetyProtection(ctx context.Context, in workspace.HistorySafetyChangeRequest) (workspace.HistorySafetyChangeResult, error) {
	l.calls++
	out, err := l.HistorySafety.ChangeHistorySafetyProtection(ctx, in)
	if err != nil {
		return out, err
	}
	l.requestID = out.RequestID
	return workspace.HistorySafetyChangeResult{}, &coreclient.UncertainError{RequestID: out.RequestID, Cause: fmt.Errorf("injected lost final protection reply")}
}

func TestConnectedHistorySafetyLostReplyChecksOriginalWithoutRepeating(t *testing.T) {
	f, _, original := committedHistorySafetyFixture(t, 80, 24)
	one := f.shells[0]
	openHistorySafetyKeys(t, f)
	view := one.app.historySafety
	lost := &lostHistorySafetyReply{HistorySafety: view.port}
	view.port = lost // transport-result fault, never a canonical state mutation
	historySafetyClick(t, one, historyscreen.ActionSafetyRelease)
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyLeft})
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
	op := one.app.historySafetyOperation
	if op == nil || op.busy || op.requestID == "" || op.requestID != lost.requestID || lost.calls != 1 {
		t.Fatal("lost reply did not retain exact unresolved original operation", op, lost.calls)
	}
	if paint := ansi.Strip(one.app.View().RenderContent()); !strings.Contains(paint, "Check original") ||
		strings.Contains(paint, "Release protection") || strings.Contains(paint, "Retry") {
		t.Fatal("ambiguous effect offered a new mutation instead of original observation", paint)
	}
	historySafetyClick(t, one, historyscreen.ActionSafetyCheck)
	remoteJourneyUntil(t, f.shells, func() bool {
		d, ok := view.screen.SelectedDetail()
		return ok && one.app.historySafetyOperation == nil && !d.Retained
	})
	if lost.calls != 1 {
		t.Fatal("Check replayed the original protection mutation", lost.calls)
	}
	detail, _ := view.screen.SelectedDetail()
	if detail.LastEvent == nil || detail.LastEvent.Change.RequestID != lost.requestID || detail.Result != original {
		t.Fatal("original receipt observation lost immutable audit provenance", detail)
	}
	f.assertBytes(t, "package journey\n\nvar new = 1\n", "package journey\n\nvar use = new\n")
}

func TestConnectedHistorySafetyPeerChangeRetiresOpenConsent(t *testing.T) {
	f, _, original := committedHistorySafetyFixture(t, 120, 36)
	one, two := f.shells[0], f.shells[1]
	openHistorySafetyKeys(t, f)
	historySafetyClick(t, one, historyscreen.ActionSafetyRelease)
	if one.app.historySafety.confirm == nil {
		t.Fatal("first shell did not retain exact consent")
	}
	peerView := f
	peerView.shells = []remoteJourneyShell{two, one}
	openHistorySafetyKeys(t, peerView)
	historySafetyClick(t, two, historyscreen.ActionSafetyRelease)
	renameConnectedKey(t, two, tea.KeyPressMsg{Code: tea.KeyLeft})
	renameConnectedKey(t, two, tea.KeyPressMsg{Code: tea.KeyEnter})
	remoteJourneyUntil(t, f.shells, func() bool {
		detail, ok := two.app.historySafety.screen.SelectedDetail()
		return ok && !detail.Retained && detail.Revision == 1
	})
	peerDetail, _ := two.app.historySafety.screen.SelectedDetail()
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyLeft})
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEnter})
	remoteJourneyUntil(t, f.shells, func() bool {
		detail, ok := one.app.historySafety.screen.SelectedDetail()
		return ok && one.app.historySafetyOperation == nil && detail.Revision == 1
	})
	refreshed, _ := one.app.historySafety.screen.SelectedDetail()
	if refreshed.Retained || refreshed.LastEvent == nil || refreshed.LastEvent.ID != peerDetail.LastEvent.ID ||
		refreshed.Result != original || refreshed.LastEvent.Change.ActorClientID == refreshed.OriginalOwner.ClientID {
		t.Fatal("stale consent changed the peer's protection or substituted its original provenance", refreshed)
	}
	f.assertBytes(t, "package journey\n\nvar new = 1\n", "package journey\n\nvar use = new\n")
}

func TestConnectedHistorySafetyClosedReaderDropsLateInspection(t *testing.T) {
	f, _, original := committedHistorySafetyFixture(t, 100, 32)
	one := f.shells[0]
	openHistorySafetyKeys(t, f)
	old := one.app.historySafety
	sequence := old.sequence
	detail, _ := old.screen.SelectedDetail()
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyEsc})
	if one.app.historySafety != nil {
		t.Fatal("Escape did not retire the exact reader")
	}
	openHistorySafetyKeys(t, f)
	current := one.app.historySafety
	before, _ := current.screen.SelectedDetail()
	runAppCommand(t, one.app, one.app.applyHistorySafetyRead(historySafetyReadMsg{
		view: old, sequence: sequence, kind: "inspect",
		detail: workspace.HistorySafetyDetail{Reference: "foreign", CanRelease: true},
	}))
	after, ok := current.screen.SelectedDetail()
	if !ok || after.Reference != original.SafetyReference || after.InspectionDigest != before.InspectionDigest ||
		current == old || detail.Result != original {
		t.Fatal("retired inspection rebound to a reopened surface", after)
	}
}
