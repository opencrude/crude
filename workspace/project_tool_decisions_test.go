package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"crude/fileio"
	"crude/types"
)

func decisionFixture(t *testing.T) (*Workspace, types.ProjectToolReview) {
	t.Helper()
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "project")
	if err = os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	identity, err := fileio.DirectoryIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	db := openTestWorkspaceDB(t)
	settings, _ := json.Marshal(types.WorkspaceOverrides{
		Analysis: json.RawMessage(`{"trust":"trusted"}`), AnalysisRootIdentity: identity,
		Capabilities: json.RawMessage(`{"unknown":{"keep":[1,2,3]}}`),
	})
	if err := db.SetSetting(workspaceSettingsKey, string(settings)); err != nil {
		t.Fatal(err)
	}
	m := NewLSPManager(nil)
	t.Cleanup(m.Close)
	m.mu.Lock()
	m.workspaceRoot = root
	m.analysisRootIdentity = identity
	m.analysisSettings = types.WorkspaceAnalysisSettings{Trust: types.AnalysisTrusted}
	m.assemblyRequested = 3
	m.assembly = types.AssemblyPlan{State: types.AssemblyReady, Generation: 3, Languages: []types.AssemblyLanguage{
		{ID: "fixture", ServerID: "fixture", Status: types.ToolMissing, Package: "amber-lsp", Command: "amber-lsp"},
		{ID: "unknown", ServerID: "unknown", Status: types.ToolMissing, Package: "not-in-catalog", Command: "unknown"},
	}}
	plan := types.CloneAssemblyPlan(m.assembly)
	m.mu.Unlock()
	w := &Workspace{RootPath: root, WorkspaceDB: db, LSPMgr: m, Logger: testLogger{}}
	review, err := BuildProjectToolReview(root, identity, plan)
	if err != nil {
		t.Fatal(err)
	}
	return w, review
}

func decisionsFromDB(t *testing.T, db *WorkspaceDB) []projectToolDecision {
	t.Helper()
	var raw string
	if err := db.DB.QueryRow(`SELECT value FROM workspace_metadata WHERE key=?`, workspaceSettingsKey).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var row struct {
		Capabilities struct {
			Decisions []projectToolDecision `json:"project_tool_decisions"`
			Unknown   json.RawMessage       `json:"unknown"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal([]byte(raw), &row); err != nil {
		t.Fatal(err)
	}
	if string(row.Capabilities.Unknown) != `{"keep":[1,2,3]}` {
		t.Fatalf("neighbor capability lost: %s", row.Capabilities.Unknown)
	}
	return row.Capabilities.Decisions
}

func TestProjectToolDecisionPersistDenyAndReplay(t *testing.T) {
	w, review := decisionFixture(t)
	work, err := w.PrepareProjectToolDecision(review, nil, "receipt-denial", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := work(context.Background()); err != nil {
		t.Fatal(err)
	}
	records := decisionsFromDB(t, w.WorkspaceDB)
	if len(records) != 1 || len(records[0].Selected) != 0 || len(records[0].Rejected) != len(review.Items) ||
		records[0].EffectAuthority || !reflect.DeepEqual(records[0].Review, review) || records[0].ReceiptID != "receipt-denial" {
		t.Fatalf("wrong durable decision: %+v", records)
	}
	if _, err := work(context.Background()); err == nil {
		t.Fatal("replayed receipt succeeded")
	}
	reopened, err := openWorkspaceDBAt(w.WorkspaceDB.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if got := decisionsFromDB(t, reopened); !reflect.DeepEqual(got, records) {
		t.Fatalf("restart changed decision: %+v", got)
	}
}

func TestProjectToolDecisionRejectsStaleAndCancellation(t *testing.T) {
	w, review := decisionFixture(t)
	if _, err := w.PrepareProjectToolDecision(review, []string{"missing"}, "bad", ""); err == nil {
		t.Fatal("accepted foreign item")
	}
	work, err := w.PrepareProjectToolDecision(review, nil, "cancel", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := work(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	w.LSPMgr.mu.Lock()
	w.LSPMgr.assemblyRequested++
	w.LSPMgr.mu.Unlock()
	if _, err := work(context.Background()); err == nil {
		t.Fatal("accepted superseded assembly")
	}
	if len(decisionsFromDB(t, w.WorkspaceDB)) != 0 {
		t.Fatal("stale/cancelled decision committed")
	}
}

func TestProjectToolDecisionWaitingForSettingsIsCancellable(t *testing.T) {
	w, review := decisionFixture(t)
	work, err := w.PrepareProjectToolDecision(review, nil, "blocked", "")
	if err != nil {
		t.Fatal(err)
	}
	w.WorkspaceDB.settingsWriteMu.Lock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, err = work(ctx)
	w.WorkspaceDB.settingsWriteMu.Unlock()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("waiting did not cancel: %v", err)
	}
}

func TestProjectToolDecisionPreparationDoesNotWaitForWorkspaceSwitch(t *testing.T) {
	w, review := decisionFixture(t)
	w.capabilityMu.Lock()
	defer w.capabilityMu.Unlock()
	if _, err := w.PrepareProjectToolDecision(review, nil, "switching", ""); err == nil {
		t.Fatal("prepared a decision while the workspace was changing")
	}
}

func TestProjectToolDecisionMalformedAuthorityFailsClosed(t *testing.T) {
	w, review := decisionFixture(t)
	work, err := w.PrepareProjectToolDecision(review, nil, "bad", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, corrupt := range []string{
		`{"analysis":{"trust":"trusted"},"analysis_root_identity":` + string(mustJSON(t, review.RootIdentity)) + `,"capabilities":{"project_tool_decisions":null}}`,
		`{"analysis":{"trust":"trusted"},"analysis_root_identity":` + string(mustJSON(t, review.RootIdentity)) + `,"capabilities":{"project_tool_decisions":[],"project_tool_decisions":[]}}`,
	} {
		if err := w.WorkspaceDB.SetSetting(workspaceSettingsKey, corrupt); err != nil {
			t.Fatal(err)
		}
		if _, err := work(context.Background()); err == nil {
			t.Fatal("accepted malformed authority")
		}
		if got := w.WorkspaceDB.GetSetting(workspaceSettingsKey); got != corrupt {
			t.Fatal("corrupt authority was erased")
		}
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestProjectToolDecisionSelectedSetIsDetached(t *testing.T) {
	w, review := decisionFixture(t)
	var selected []string
	for _, item := range review.Items {
		if item.Installable {
			selected = append(selected, item.ID)
		}
	}
	if len(selected) == 0 {
		t.Skip("fixture has no installable package for this platform")
	}
	original := selected[0]
	work, err := w.PrepareProjectToolDecision(review, selected[:1], "selection", "")
	if err != nil {
		t.Fatal(err)
	}
	selected[0] = "changed"
	review.Items[0].URL = "changed"
	if _, err := work(context.Background()); err != nil {
		t.Fatal(err)
	}
	records := decisionsFromDB(t, w.WorkspaceDB)
	if len(records) != 1 || !reflect.DeepEqual(records[0].Selected, []string{original}) ||
		len(records[0].Rejected) != len(records[0].Review.Items)-1 || records[0].EffectAuthority {
		t.Fatalf("wrong selection: %+v", records)
	}
}

func TestProjectToolDecisionRejectsRootAndTrustChange(t *testing.T) {
	for _, mode := range []string{"root", "trust"} {
		t.Run(mode, func(t *testing.T) {
			w, review := decisionFixture(t)
			work, err := w.PrepareProjectToolDecision(review, nil, "decision", "")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "trust" {
				w.LSPMgr.SetAnalysisPolicy(types.DefaultWorkspaceAnalysisSettings(), "")
			} else {
				if err := os.Rename(w.RootPath, w.RootPath+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(w.RootPath, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := work(context.Background()); err == nil {
				t.Fatal("accepted changed project")
			}
			if len(decisionsFromDB(t, w.WorkspaceDB)) != 0 {
				t.Fatal("changed project persisted a decision")
			}
		})
	}
}

func TestProjectToolDecisionConcurrentPreference(t *testing.T) {
	w, review := decisionFixture(t)
	work, err := w.PrepareProjectToolDecision(review, nil, "concurrent", "")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := saveWorkspacePreferences(w.WorkspaceDB, w.AppDefaults(), []types.SettingEdit{
			{Path: []string{"editor", "tab_size"}, After: json.RawMessage("7")},
		}, "", predictionPreferenceBinding{})
		done <- err
	}()
	if _, err := work(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(decisionsFromDB(t, w.WorkspaceDB)) != 1 || w.LoadWorkspaceOverrides().Editor == nil {
		t.Fatal("concurrent decision or preference was lost")
	}
}

func TestProjectToolDecisionWriterRejectsChangedHistoryPreimage(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "first-choice", true: "explicit-retry"}[existing], func(t *testing.T) {
			w, review := decisionFixture(t)
			expectedCount := 1
			if existing {
				work, err := w.PrepareProjectToolDecision(review, nil, "initial-choice", "")
				if err != nil {
					t.Fatal(err)
				}
				if _, err := work(t.Context()); err != nil {
					t.Fatal(err)
				}
				expectedCount++
			}
			_, history, err := readProjectToolDecisions(t.Context(), w.WorkspaceDB)
			if err != nil {
				t.Fatal(err)
			}
			// Both workers are prepared while the reviewed history is still
			// current. Only the writer's transactional check can fence the
			// second worker after the first worker commits.
			first, err := w.PrepareProjectToolDecision(review, nil, "first-shell", history)
			if err != nil {
				t.Fatal(err)
			}
			second, err := w.PrepareProjectToolDecision(review, nil, "second-shell", history)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := first(t.Context()); err != nil {
				t.Fatal(err)
			}
			if saved, err := second(t.Context()); err == nil || saved.ReceiptID != "" {
				t.Fatalf("changed decision history was appended: %+v %v", saved, err)
			}
			records := decisionsFromDB(t, w.WorkspaceDB)
			if len(records) != expectedCount || records[len(records)-1].ReceiptID != "first-shell" {
				t.Fatalf("stale writer changed history: %+v", records)
			}
		})
	}
}
