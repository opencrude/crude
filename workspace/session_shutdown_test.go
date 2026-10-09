package workspace

import (
	"context"
	"strings"
	"testing"

	"crude/provider"
	"crude/types"
)

type shutdownRegistryLogger struct {
	testLogger
	errors []string
}

func (l *shutdownRegistryLogger) Error(_, msg string, _ ...any) {
	l.errors = append(l.errors, msg)
}

func TestCloseAllSessionsSyncsFinalMetadataWithoutReadingClosedDB(t *testing.T) {
	setTestHome(t)
	log := &shutdownRegistryLogger{}
	w := NewWorkspace(t.TempDir(), "history-model", "tester", types.Settings{}, log)
	t.Cleanup(w.Close)
	w.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
	w.SetBackendConfig(historyE2EConfig("http://127.0.0.1:1"), t.TempDir())
	w.SetEventSender(&historyE2ESender{events: make(chan historyE2EEvent, 1024)})
	created := w.NewAgentSession()
	if created.Error != "" {
		t.Fatal(created.Error)
	}
	owner := w.SessionsByID[created.SessionID]
	db := owner.DB
	intent, err := w.PrepareAgentInputAdmission(created.OrchID, provider.PlainInput("saved input during shutdown"), 1)
	if err != nil {
		t.Fatal(err)
	}
	// Close must preserve a prepared input before mirroring final metadata, but
	// must not launch a provider request. Syncing before close would miss this.
	w.CloseAllSessions()
	if len(log.errors) != 0 {
		t.Fatalf("shutdown emitted registry errors: %v", log.errors)
	}
	if err := db.PingContext(context.Background()); err == nil {
		t.Fatal("shutdown left the session DB open")
	}
	if result := owner.backend.CommitInputAdmission(context.Background(), intent); result.Status != InputAdmissionPreservationPending {
		t.Fatalf("shutdown did not preserve its prepared input: %+v", result)
	}
	metas, err := w.WorkspaceDB.ListSessions()
	if err != nil || len(metas) != 1 || !strings.Contains(metas[0].FirstPrompt.String, "saved input during shutdown") || metas[0].MessageCount == 0 {
		t.Fatalf("final registry missed preserved input: %+v err=%v", metas, err)
	}
}
