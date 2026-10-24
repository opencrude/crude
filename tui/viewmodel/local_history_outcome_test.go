package viewmodel

import (
	"errors"
	"testing"

	"crude/coreclient"
	"crude/fileio"
	"crude/localhistory"
	historyscreen "crude/tui/screens/localhistory"
)

// An unknown restore outcome keeps its original request as the only operation;
// a fresh Apply would mint a new request ID and could repeat the disk effect.
func TestUncertainRestoreBlocksFreshApplyUntilObserved(t *testing.T) {
	app := newRemoteJourneyShell(t, remoteJourneyRuntime(t, "http://127.0.0.1:1"), t.TempDir()).app
	if app.historyApplyCmd(historyscreen.ApplyRequest{PlanID: "idle"}) == nil {
		t.Fatal("connected shell cannot issue a restore while idle")
	}
	app.historyOperation = nil
	screen := historyscreen.New(app.Theme)
	app.historyScreen = screen
	operation := &historyOperation{owner: app.ws, generation: app.workspaceSwitchGen.Load(), screen: screen, busy: true}
	app.historyOperation = operation
	cmd := app.applyHistoryResult(historyApplyResultMsg{operation: operation, err: &coreclient.UncertainError{RequestID: "original", Cause: errors.New("carrier lost"), State: "uncertain"}})
	if cmd == nil {
		t.Fatal("uncertain outcome was not delivered to the history screen")
	}
	state, ok := cmd().(historyscreen.AppliedMsg)
	if !ok || state.RequestID != "original" || state.Error == "" {
		t.Fatalf("uncertain outcome lost its request identity: %+v", state)
	}
	if app.historyOperation != operation || operation.requestID != "original" {
		t.Fatal("uncertain restore released its operation before observation")
	}
	if app.historyApplyCmd(historyscreen.ApplyRequest{PlanID: "fresh", Choices: []localhistory.RestoreChoice{{Path: "file"}}}) != nil {
		t.Fatal("fresh Apply admitted while the original outcome is unknown")
	}
	if app.historyCheckCmd("other") != nil || app.historyCheckCmd("original") == nil {
		t.Fatal("receipt observation is not bound to the original request")
	}
}

func TestCommittedRestoreWarningKeepsSafetyEvidence(t *testing.T) {
	app := newTestApp(t)
	screen := historyscreen.New(app.Theme)
	app.historyScreen = screen
	operation := &historyOperation{owner: app.ws, generation: app.workspaceSwitchGen.Load(), screen: screen, busy: true}
	app.historyOperation = operation
	result := localhistory.RestoreResult{SafetySnapshotID: "safety", Paths: []string{"file"}}
	cmd := app.applyHistoryResult(historyApplyResultMsg{operation: operation, result: result, err: fileio.CommittedWarning("publication failed")})
	state, _ := cmd().(historyscreen.AppliedMsg)
	if state.Error != "" || state.Warning == "" || state.Result.SafetySnapshotID != "safety" {
		t.Fatalf("committed restore reported as refusal or lost safety: %+v", state)
	}
	if app.historyOperation != nil {
		t.Fatal("known committed restore kept the operation blocked")
	}
}
