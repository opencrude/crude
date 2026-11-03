package workspace

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"
	"time"

	"crude/document"
	"crude/insight/catalog"
	"crude/network"
	"crude/toolstore"
	"crude/types"
)

type setupStoreFake struct {
	mu        sync.Mutex
	tools     []toolstore.Tool
	requests  []toolstore.Request
	uses      []string
	before    func(context.Context, toolstore.Request) error
	beforeUse func()
}

func (s *setupStoreFake) List(ctx context.Context) ([]toolstore.Tool, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]toolstore.Tool(nil), s.tools...), 0, ctx.Err()
}

func (s *setupStoreFake) AcquireExecutable(ctx context.Context, _ string) (func(), bool, error) {
	return func() {}, true, ctx.Err()
}

func (s *setupStoreFake) Install(ctx context.Context, r toolstore.Request) (toolstore.Tool, error) {
	s.mu.Lock()
	s.requests = append(s.requests, r)
	s.mu.Unlock()
	if s.before != nil {
		if err := s.before(ctx, r); err != nil {
			return toolstore.Tool{}, err
		}
	}
	if r.PreparePublication == nil || r.AdmitPublication == nil {
		return toolstore.Tool{}, errors.New("missing publication checks")
	}
	if err := r.PreparePublication(ctx); err != nil {
		return toolstore.Tool{}, err
	}
	if err := r.AdmitPublication(ctx); err != nil {
		return toolstore.Tool{}, err
	}
	tool := toolstore.Tool{ID: r.Package + "/fixture", Package: r.Package, Version: r.Version,
		Source: r.Source, URL: r.URL, Archive: r.Archive, Bin: r.Bin, Command: r.Command, Executable: filepath.Join(os.TempDir(), "fake-setup-executable")}
	if r.Evaluate != nil {
		if err := r.Evaluate(ctx, tool); err != nil {
			return toolstore.Tool{}, err
		}
	}
	s.mu.Lock()
	s.tools = append(s.tools, tool)
	s.mu.Unlock()
	return tool, nil
}

func (s *setupStoreFake) MarkUsedContext(ctx context.Context, id, project string, admit func(context.Context) error) error {
	if s.beforeUse != nil {
		s.beforeUse()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := admit(ctx); err != nil {
		return err
	}
	s.uses = append(s.uses, id+":"+project)
	return nil
}

func setupServiceFixture(t *testing.T) (*Workspace, *ProjectToolSetupOwner, *setupStoreFake, document.Owner) {
	t.Helper()
	dir := t.TempDir()
	doc := catalog.Document{Format: 1}
	for _, name := range []string{"setup-alpha", "setup-beta"} {
		doc.Packages = append(doc.Packages, catalog.PackageRecord{
			Name: name, Licenses: []string{"MIT"},
			Source: catalog.PackageSourceRecord{ID: "pkg:github/fixture/" + name + "@v1",
				Assets: []catalog.AssetRecord{{Targets: catalog.CurrentTargets(), File: "server.zip", Bin: setupFixtureBin()}}},
			Bin: map[string]string{"serve": "{{source.asset.bin}}"},
		})
	}
	for index, id := range []string{"a", "b"} {
		doc.Languages = append(doc.Languages, catalog.LanguageRecord{ID: id, Name: id, LSPLanguageID: id, Extensions: []string{".setup-" + id}, Servers: []string{id}})
		doc.Servers = append(doc.Servers, catalog.ServerRecord{ID: id, Command: "serve",
			Package: []string{"setup-alpha", "setup-beta"}[index],
			Args:    []string{"-test.run=^TestFixtureLanguageServerHelper$"}, Environment: map[string]string{"CRUDE_FIXTURE_LS": "1"}})
	}
	if err := os.WriteFile(filepath.Join(dir, "setup.json"), mustJSON(t, doc), 0600); err != nil {
		t.Fatal(err)
	}
	if problems := catalog.UsePacks(dir); len(problems) != 0 {
		t.Fatal(problems)
	}
	t.Cleanup(func() { catalog.UsePacks(filepath.Join(dir, "none")) })
	w, _ := decisionFixture(t)
	w.LSPMgr.SetRuntime(&lifecycleRuntime{})
	trustAnalysis(t, w.LSPMgr, w.RootPath)
	w.LSPMgr.mu.Lock()
	w.LSPMgr.assembly.Languages = []types.AssemblyLanguage{
		{ID: "a", ServerID: "a", Sample: filepath.Join(w.RootPath, "main.setup-a"), Status: types.ToolMissing, Package: "setup-alpha", Command: "serve"},
		{ID: "b", ServerID: "b", Sample: filepath.Join(w.RootPath, "main.setup-b"), Status: types.ToolMissing, Package: "setup-beta", Command: "serve"},
		{ID: "c", ServerID: "c", Status: types.ToolMissing, Package: "setup-unknown", Command: "serve"},
	}
	w.LSPMgr.mu.Unlock()
	s := &setupStoreFake{}
	o := NewProjectToolSetupOwner(w, nil)
	o.store = s
	t.Cleanup(o.Close)
	return w, o, s, document.Owner{ClientID: "native-shell", Epoch: 1}
}

func setupReview(t *testing.T, o *ProjectToolSetupOwner, owner document.Owner) types.ProjectToolSetupReview {
	t.Helper()
	r, err := o.Review(t.Context(), owner, false)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func setupInstallable(r types.ProjectToolSetupReview) []string {
	var ids []string
	for _, item := range r.Review.Items {
		if item.Installable {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func TestProjectToolSetupDeclinePersistsAcrossReopenAndOnlyNewNeedAppears(t *testing.T) {
	w, o, s, owner := setupServiceFixture(t)
	r := setupReview(t, o, owner)
	if !r.NeedsDecision || r.Quiet || len(r.Review.Items) != 3 {
		t.Fatalf("initial grouped review: %+v", r)
	}
	if again := setupReview(t, o, owner); again.ID != r.ID {
		t.Fatal("unchanged review allocated another preview")
	}
	result, err := o.Decide(t.Context(), owner, "decline", r.ID, nil)
	if err != nil || result.DecisionID == "" || len(s.requests) != 0 || len(s.uses) != 0 {
		t.Fatalf("decline: %+v %v", result, err)
	}
	records := decisionsFromDB(t, w.WorkspaceDB)
	if len(records) != 1 || len(records[0].Rejected) != 3 || records[0].EffectAuthority {
		t.Fatalf("saved refusal: %+v", records)
	}
	o.Close()
	db, err := openWorkspaceDBAt(w.WorkspaceDB.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	w.WorkspaceDB = db
	reopened := NewProjectToolSetupOwner(w, nil)
	reopened.store = s
	defer reopened.Close()
	if got := setupReview(t, reopened, owner); got.NeedsDecision || got.ID != "" || len(got.Review.Items) != 0 {
		t.Fatalf("repeated a saved refusal: %+v", got)
	}
	w.LSPMgr.mu.Lock()
	w.LSPMgr.assemblyRequested++
	w.LSPMgr.assembly.Generation = w.LSPMgr.assemblyRequested
	w.LSPMgr.assembly.Languages = append(w.LSPMgr.assembly.Languages,
		types.AssemblyLanguage{ID: "new", ServerID: "new", Status: types.ToolMissing, Package: "setup-new", Command: "new"})
	w.LSPMgr.mu.Unlock()
	next := setupReview(t, reopened, owner)
	if !next.NeedsDecision || !next.Quiet || len(next.Review.Items) != 1 || next.Review.Items[0].Package != "setup-new" {
		t.Fatalf("new need repeated existing choices: %+v", next)
	}
	if _, err := reopened.Decide(t.Context(), owner, "new-decline", next.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got := setupReview(t, reopened, owner); got.NeedsDecision {
		t.Fatalf("new refusal did not settle: %+v", got)
	}
}

func TestProjectToolSetupRejectsForeignForgedExpiredAndReplayedDecisions(t *testing.T) {
	w, o, s, owner := setupServiceFixture(t)
	r := setupReview(t, o, owner)
	for _, tc := range []struct {
		owner document.Owner
		id    string
		ids   []string
	}{
		{document.Owner{ClientID: "foreign", Epoch: 1}, r.ID, nil},
		{owner, r.Review.Token, nil},
		{owner, r.ID, []string{"forged"}},
		{owner, r.ID, append(setupInstallable(r), setupInstallable(r)[0])},
	} {
		if _, err := o.Decide(t.Context(), tc.owner, "bad", tc.id, tc.ids); err == nil {
			t.Fatal("foreign or forged decision accepted")
		}
	}
	// The returned review is a detached copy, never caller-owned authority.
	r.Review.Items[0].URL = "https://example.invalid/forged"
	r.Review.Items[0].ID = reviewHash(r.Review.Items[0])
	if _, err := o.Decide(t.Context(), owner, "forgery", r.ID, []string{r.Review.Items[0].ID}); err == nil {
		t.Fatal("accepted caller-mutated review")
	}
	if len(s.requests) != 0 || len(decisionsFromDB(t, w.WorkspaceDB)) != 0 {
		t.Fatal("invalid decision caused an effect")
	}
	o.mu.Lock()
	o.pending[r.ID].expires = time.Now().Add(-time.Second)
	o.mu.Unlock()
	if _, err := o.Decide(t.Context(), owner, "expired", r.ID, nil); err == nil {
		t.Fatal("expired decision accepted")
	}
	r = setupReview(t, o, owner)
	if _, err := o.Decide(t.Context(), owner, "once", r.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Decide(t.Context(), owner, "replay", r.ID, nil); err == nil {
		t.Fatal("consumed decision replayed")
	}
}

func TestProjectToolSetupPreviewBoundsAndDetachedOwner(t *testing.T) {
	_, o, _, owner := setupServiceFixture(t)
	for epoch := uint64(1); epoch <= projectToolPreviewLimit; epoch++ {
		owner.Epoch = epoch
		if _, err := o.Review(t.Context(), owner, false); err != nil {
			t.Fatal(err)
		}
	}
	owner.Epoch++
	if _, err := o.Review(t.Context(), owner, false); !errors.Is(err, document.ErrLimit) {
		t.Fatalf("preview bound: %v", err)
	}
	owner.Epoch = 1
	o.Detach(owner)
	if _, err := o.Review(t.Context(), owner, false); !errors.Is(err, document.ErrOwner) {
		t.Fatalf("detached owner reviewed tools: %v", err)
	}
	owner.Epoch = projectToolPreviewLimit + 1
	if _, err := o.Review(t.Context(), owner, false); err != nil {
		t.Fatalf("detach did not release preview: %v", err)
	}
	o.Close()
	if _, err := o.Review(t.Context(), owner, false); !errors.Is(err, document.ErrOwner) {
		t.Fatalf("closed owner reviewed tools: %v", err)
	}
}

func TestProjectToolSetupPartialFailureAccountsSuccessfulToolAndDoesNotLoop(t *testing.T) {
	w, o, s, owner := setupServiceFixture(t)
	original := w.LSPMgr.Assembly()
	r := setupReview(t, o, owner)
	ids := setupInstallable(r)
	if len(ids) != 2 {
		t.Fatalf("expected two installable candidates: %+v", r)
	}
	offline := &net.DNSError{Err: "offline", Name: "fixture"}
	s.before = func(_ context.Context, req toolstore.Request) error {
		if req.Package == "setup-alpha" {
			return offline
		}
		return nil
	}
	result, err := o.Decide(t.Context(), owner, "approve", r.ID, ids)
	if !errors.Is(err, offline) || result.DecisionID == "" || len(result.Items) != 2 {
		t.Fatalf("partial outcome: %+v %v", result, err)
	}
	s.mu.Lock()
	if len(s.requests) != 2 || len(s.tools) != 1 || len(s.uses) != 1 {
		t.Fatalf("independent tool or accounting lost: %+v %+v %+v", s.requests, s.tools, s.uses)
	}
	s.mu.Unlock()
	installed := 0
	for _, item := range result.Items {
		if !item.Attempted || item.Message == "" {
			t.Fatalf("lost item outcome: %+v", result)
		}
		if item.Installed {
			installed++
		}
	}
	if installed != 1 {
		t.Fatalf("wrong installed outcomes: %+v", result)
	}
	awaitAssembly(t, w.LSPMgr, func(p types.AssemblyPlan) bool {
		return p.Generation > original.Generation && p.State == types.AssemblyReady
	})
	// Simulate a reopen that discovers the failed candidate again. Saved
	// approvals suppress the automatic question, but never start another worker.
	w.LSPMgr.mu.Lock()
	original.Generation = w.LSPMgr.assemblyRequested
	w.LSPMgr.assembly = original
	w.LSPMgr.mu.Unlock()
	o.Close()
	reopened := NewProjectToolSetupOwner(w, nil)
	reopened.store = s
	defer reopened.Close()
	if got := setupReview(t, reopened, owner); got.NeedsDecision {
		t.Fatalf("failed candidate repeated the automatic question: %+v", got)
	}
	if len(s.requests) != 2 {
		t.Fatal("audit evidence authorized another download")
	}
}

func TestProjectToolSetupRevocationStopsDownloadAndLateEffects(t *testing.T) {
	for _, mode := range []string{"detach", "close", "cancel", "trust", "assembly", "root", "catalog"} {
		t.Run(mode, func(t *testing.T) {
			w, o, s, owner := setupServiceFixture(t)
			r := setupReview(t, o, owner)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			started, proceed, canceled := make(chan struct{}), make(chan struct{}), make(chan struct{})
			s.before = func(ctx context.Context, _ toolstore.Request) error {
				close(started)
				if mode == "root" || mode == "catalog" {
					<-proceed
					return nil
				}
				<-ctx.Done()
				close(canceled)
				return ctx.Err()
			}
			done := make(chan error, 1)
			go func() {
				_, err := o.Decide(ctx, owner, "decision", r.ID, setupInstallable(r)[:1])
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("installer did not start")
			}
			switch mode {
			case "detach":
				o.Detach(owner)
			case "close":
				o.Close()
			case "cancel":
				cancel()
			case "trust":
				w.LSPMgr.SetAnalysisPolicy(types.DefaultWorkspaceAnalysisSettings(), "")
			case "assembly":
				w.LSPMgr.mu.Lock()
				w.LSPMgr.assemblyRequested++
				w.LSPMgr.bumpVersionLocked()
				w.LSPMgr.mu.Unlock()
			case "root":
				if err := os.Rename(w.RootPath, w.RootPath+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(w.RootPath, 0700); err != nil {
					t.Fatal(err)
				}
				close(proceed)
			case "catalog":
				catalog.UsePacks(t.TempDir())
				close(proceed)
			}
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("revoked operation succeeded")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("revoked worker did not stop")
			}
			if mode != "root" && mode != "catalog" {
				select {
				case <-canceled:
				default:
					t.Fatal("revocation did not cancel the download")
				}
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if len(s.tools) != 0 || len(s.uses) != 0 {
				t.Fatalf("late publication or usage after %s: %+v %+v", mode, s.tools, s.uses)
			}
			if _, err := o.Decide(t.Context(), owner, "replay", r.ID, setupInstallable(r)[:1]); err == nil {
				t.Fatal("revoked preview replayed")
			}
		})
	}
}

func TestProjectToolSetupRevocationBeforeUsageRejectsAccounting(t *testing.T) {
	_, o, s, owner := setupServiceFixture(t)
	r := setupReview(t, o, owner)
	s.beforeUse = func() { o.Detach(owner) }
	result, err := o.Decide(t.Context(), owner, "approve", r.ID, setupInstallable(r)[:1])
	if err == nil || len(s.tools) != 1 || len(s.uses) != 0 || len(result.Items) != 1 || !result.Items[0].Installed {
		t.Fatalf("stale accounting: %+v %v %+v", result, err, s.uses)
	}
}

func TestProjectToolSetupOwnerAndWorkspaceIsolation(t *testing.T) {
	w, o, s, owner := setupServiceFixture(t)
	r := setupReview(t, o, owner)
	otherOwner := document.Owner{ClientID: "other-shell", Epoch: 1}
	other := setupReview(t, o, otherOwner)
	// Two shells can inspect the same project, but only one exact decision
	// can take effect. A second old preview cannot repeat a saved choice.
	if _, err := o.Decide(t.Context(), owner, "decline", r.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Decide(t.Context(), otherOwner, "also-decline", other.ID, nil); err == nil {
		t.Fatal("another shell repeated an already committed decision")
	}
	w2, _ := decisionFixture(t)
	w2.LSPMgr.mu.Lock()
	w2.LSPMgr.assembly.Languages = w.LSPMgr.Assembly().Languages
	w2.LSPMgr.mu.Unlock()
	o2 := NewProjectToolSetupOwner(w2, nil)
	o2.store = s
	defer o2.Close()
	if _, err := o2.Decide(t.Context(), owner, "foreign", r.ID, nil); err == nil {
		t.Fatal("foreign workspace accepted a preview")
	}
	otherWorkspace := setupReview(t, o2, owner)
	if !otherWorkspace.NeedsDecision || len(otherWorkspace.Review.Items) != 3 {
		t.Fatalf("other workspace inherited a refusal: %+v", otherWorkspace)
	}
}

func TestProjectToolSetupExplicitRetryAfterOfflineNeedsNewDecision(t *testing.T) {
	w, o, s, owner := setupServiceFixture(t)
	initial := setupReview(t, o, owner)
	selected := setupInstallable(initial)[:1]
	offline := &net.DNSError{Err: "offline", Name: "fixture"}
	s.before = func(context.Context, toolstore.Request) error { return offline }
	result, err := o.Decide(t.Context(), owner, "offline", initial.ID, selected)
	if !errors.Is(err, offline) || result.DecisionID == "" || len(s.requests) != 1 || len(s.tools) != 0 {
		t.Fatalf("offline attempt: %+v %v %+v", result, err, s.requests)
	}
	if automatic := setupReview(t, o, owner); automatic.NeedsDecision || len(s.requests) != 1 {
		t.Fatalf("offline attempt repeated automatically: %+v", automatic)
	}
	retry, err := o.Review(t.Context(), owner, true)
	if err != nil || !retry.NeedsDecision || retry.ID == initial.ID || len(retry.Review.Items) != 3 {
		t.Fatalf("explicit retry did not offer a fresh review: %+v %v", retry, err)
	}
	if len(s.requests) != 1 || len(decisionsFromDB(t, w.WorkspaceDB)) != 1 {
		t.Fatal("explicit review replayed installation authority")
	}
	s.before = nil
	result, err = o.Decide(t.Context(), owner, "retry", retry.ID, selected)
	if err != nil || len(result.Items) != 1 || !result.Items[0].Installed || len(s.requests) != 2 {
		t.Fatalf("new decision did not make exactly one new attempt: %+v %v %+v", result, err, s.requests)
	}
	if _, err := o.Decide(t.Context(), owner, "replay", retry.ID, selected); err == nil || len(s.requests) != 2 {
		t.Fatal("retry preview was reusable")
	}
}

func TestProjectToolSetupExplicitPreviewInvalidatedByOtherShellHistory(t *testing.T) {
	w, o, s, owner := setupServiceFixture(t)
	initial := setupReview(t, o, owner)
	if _, err := o.Decide(t.Context(), owner, "decline", initial.ID, nil); err != nil {
		t.Fatal(err)
	}
	older, err := o.Review(t.Context(), owner, true)
	if err != nil || !older.NeedsDecision {
		t.Fatalf("explicit review: %+v %v", older, err)
	}
	otherOwner := document.Owner{ClientID: "other-shell", Epoch: 1}
	other, err := o.Review(t.Context(), otherOwner, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.Decide(t.Context(), otherOwner, "other-decline", other.ID, nil); err != nil {
		t.Fatal(err)
	}
	// The exact candidates and catalog have not changed. History alone must
	// revoke the old review, including a newly selected installation.
	if _, err := o.Decide(t.Context(), owner, "stale", older.ID, setupInstallable(older)[:1]); err == nil {
		t.Fatal("another shell's decision did not invalidate the explicit preview")
	}
	if len(s.requests) != 0 || len(decisionsFromDB(t, w.WorkspaceDB)) != 2 {
		t.Fatal("stale history caused a decision or installation")
	}
	fresh, err := o.Review(t.Context(), owner, true)
	if err != nil || !fresh.NeedsDecision || fresh.ID == older.ID {
		t.Fatalf("history change did not create a new preview: %+v %v", fresh, err)
	}
}

func TestProjectToolSetupExplicitChoicesCannotRunSimultaneously(t *testing.T) {
	_, o, s, owner := setupServiceFixture(t)
	first, err := o.Review(t.Context(), owner, true)
	if err != nil {
		t.Fatal(err)
	}
	otherOwner := document.Owner{ClientID: "other-shell", Epoch: 1}
	second, err := o.Review(t.Context(), otherOwner, true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	started := make(chan struct{}, 2)
	s.before = func(ctx context.Context, _ toolstore.Request) error {
		started <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	done := make(chan error, 1)
	go func() {
		_, err := o.Decide(ctx, owner, "first", first.ID, setupInstallable(first)[:1])
		done <- err
	}()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Errorf("canceled first attempt: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("first attempt did not join")
		}
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first decision did not reach installation")
	}
	if _, err := o.Decide(t.Context(), otherOwner, "second", second.ID, setupInstallable(second)[:1]); err == nil {
		t.Fatal("another shell started a simultaneous project decision")
	}
	activeReview, err := o.Review(t.Context(), otherOwner, true)
	if err != nil || activeReview.NeedsDecision {
		t.Fatalf("explicit review offered an active decision: %+v %v", activeReview, err)
	}
	s.mu.Lock()
	attempts := len(s.requests)
	s.mu.Unlock()
	if attempts != 1 {
		t.Fatalf("simultaneous project decisions made %d attempts", attempts)
	}
}

func TestProjectToolSetupConcurrentSettingsAndCanceledDecision(t *testing.T) {
	w, o, s, owner := setupServiceFixture(t)
	r := setupReview(t, o, owner)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := o.Decide(ctx, owner, "canceled", r.ID, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled decision: %v", err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := saveWorkspacePreferences(w.WorkspaceDB, w.AppDefaults(), []types.SettingEdit{
			{Path: []string{"editor", "tab_size"}, After: json.RawMessage("7")},
		}, "", predictionPreferenceBinding{})
		done <- err
	}()
	if _, err := o.Decide(t.Context(), owner, "decline", r.ID, nil); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(decisionsFromDB(t, w.WorkspaceDB)) != 1 || w.LoadWorkspaceOverrides().Editor == nil ||
		len(s.requests) != 0 || !reflect.DeepEqual(decisionsFromDB(t, w.WorkspaceDB)[0].Selected, []string{}) {
		t.Fatal("cancellation or concurrent settings lost the decision")
	}
}

// The catalog still supplies the real HTTPS URL. The explicit test proxy
// routes that host to a pinned local TLS server; no external network is used.
func setupFixtureBin() string {
	if runtime.GOOS == "windows" {
		return "bin/server.exe"
	}
	return "bin/server"
}

func setupFixtureExecutable(t *testing.T) []byte {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func fixtureExecutableArchive(t *testing.T, bin string) []byte {
	t.Helper()
	var archive bytes.Buffer
	writer := zip.NewWriter(&archive)
	// Compression is not under test here. Store the real executable unchanged
	// so race instrumentation does not turn fixture preparation into a CPU gate.
	header := &zip.FileHeader{Name: bin, Method: zip.Store}
	header.SetMode(0755)
	file, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write(setupFixtureExecutable(t)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return archive.Bytes()
}

func serveSetupCatalogArtifact(t *testing.T) {
	t.Helper()
	serveCatalogArtifact(t, fixtureExecutableArchive(t, setupFixtureBin()), "/fixture/setup-alpha/releases/download/v1/server.zip")
}

func serveCatalogArtifact(t *testing.T, archive []byte, requestPath string) {
	t.Helper()
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "github.com" || r.URL.Path != requestPath {
			http.Error(w, "unexpected catalog request", http.StatusNotFound)
			return
		}
		w.Header().Set("Connection", "close")
		_, _ = w.Write(archive)
	}))
	t.Cleanup(target.Close)
	var tunnels sync.WaitGroup
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != "github.com:443" {
			http.Error(w, "unexpected proxy target", http.StatusForbidden)
			return
		}
		upstream, err := net.DialTimeout("tcp", target.Listener.Addr().String(), 2*time.Second)
		if err != nil {
			http.Error(w, "local target unavailable", http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		tunnels.Add(1)
		defer tunnels.Done()
		client, buffer, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer client.Close()
		if _, err := buffer.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err := buffer.Flush(); err != nil {
			return
		}
		copied := make(chan struct{})
		go func() {
			_, _ = io.Copy(upstream, buffer)
			_ = upstream.Close()
			close(copied)
		}()
		_, _ = io.Copy(client, upstream)
		_ = client.Close()
		<-copied
	}))
	t.Cleanup(func() { proxy.Close(); tunnels.Wait() })
	previous := network.Current()
	settings := network.DefaultSettings()
	settings.ProxyMode, settings.ProxyURL = network.ProxyModeCustom, proxy.URL
	settings.TrustedCertificateHost = "github.com"
	digest := sha256.Sum256(target.Certificate().Raw)
	settings.TrustedCertificateSHA256 = hex.EncodeToString(digest[:])
	if err := network.ConfigureDefault(settings); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := network.ConfigureDefault(previous); err != nil {
			t.Error(err)
		}
	})
}

func TestProjectToolSetupRealCatalogInstallAndSharedStoreReopen(t *testing.T) {
	w, o, _, owner := setupServiceFixture(t)
	w.LSPMgr.SetRuntime(NewLSPRuntime())
	serveSetupCatalogArtifact(t)
	root := t.TempDir()
	store, err := toolstore.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	o.store = store
	w.LSPMgr.SetToolStore(store)
	r := setupReview(t, o, owner)
	if _, err := o.Decide(t.Context(), owner, "not-now", r.ID, nil); err != nil {
		t.Fatal(err)
	}
	if automatic := setupReview(t, o, owner); automatic.NeedsDecision {
		t.Fatalf("Not now repeated automatically: %+v", automatic)
	}
	initialID := r.ID
	r, err = o.Review(t.Context(), owner, true)
	if err != nil || !r.NeedsDecision || r.ID == initialID {
		t.Fatalf("Set up tools did not create a fresh review: %+v %v", r, err)
	}
	if tools, _, err := store.List(t.Context()); err != nil || len(tools) != 0 ||
		len(decisionsFromDB(t, w.WorkspaceDB)) != 1 {
		t.Fatalf("fresh review installed or recorded a choice: %+v %v", tools, err)
	}
	var selected string
	for _, item := range r.Review.Items {
		if item.Package == "setup-alpha" {
			selected = item.ID
		}
	}
	if selected == "" {
		t.Fatal("catalog did not provide the fixture tool")
	}
	result, err := o.Decide(t.Context(), owner, "install-one", r.ID, []string{selected})
	if err != nil || len(result.Items) != 1 || !result.Items[0].Installed || result.Items[0].Evaluation != toolEvaluationPassed {
		t.Fatalf("real catalog installation: %+v %v", result, err)
	}
	tool, ok := store.Lookup("setup-alpha", "v1")
	if !ok || !reflect.DeepEqual(tool.Workspaces, []string{w.RootPath}) || tool.LastUsed.IsZero() ||
		tool.Approval != result.DecisionID || tool.DigestOrigin != toolstore.DigestFirstDownload {
		t.Fatalf("accounting/provenance: %+v", tool)
	}
	if data, err := os.ReadFile(tool.Executable); err != nil || !bytes.Equal(data, setupFixtureExecutable(t)) {
		t.Fatalf("actual installed executable differs: %v", err)
	}
	if _, ok := store.Lookup("setup-beta", "v1"); ok {
		t.Fatal("deselected tool was installed")
	}
	records := decisionsFromDB(t, w.WorkspaceDB)
	if len(records) != 2 || len(records[0].Selected) != 0 || len(records[1].Selected) != 1 || len(records[1].Rejected) != 2 {
		t.Fatalf("mixed approval/refusal: %+v", records)
	}
	awaitAssembly(t, w.LSPMgr, func(p types.AssemblyPlan) bool {
		return p.Generation > r.Review.AssemblyGeneration && p.State == types.AssemblyReady
	})
	// Even an explicit review must never offer the successfully installed tool.
	remaining, err := o.Review(t.Context(), owner, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range remaining.Review.Items {
		if item.Package == "setup-alpha" {
			t.Fatalf("explicit review offered an installed tool: %+v", remaining)
		}
	}
	tools, _, err := store.List(t.Context())
	if err != nil || len(tools) != 1 {
		t.Fatalf("expected exactly one installation: %+v %v", tools, err)
	}
	history, _, err := store.History(t.Context(), 0, 100)
	installs := 0
	for _, event := range history {
		if event.Kind == "install" {
			installs++
		}
	}
	if err != nil || installs != 1 {
		t.Fatalf("expected exactly one install attempt: %+v %v", history, err)
	}
	// A separate project does not inherit refusals. It also does not ask for
	// the exact already installed artifact after the shared store reopens.
	reopened, err := toolstore.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	w2, _ := decisionFixture(t)
	w2.LSPMgr.mu.Lock()
	w2.LSPMgr.assembly.Languages = []types.AssemblyLanguage{
		{ID: "other", ServerID: "other", Status: types.ToolMissing, Package: "setup-alpha", Command: "serve"},
	}
	w2.LSPMgr.mu.Unlock()
	o2 := NewProjectToolSetupOwner(w2, reopened)
	defer o2.Close()
	if got := setupReview(t, o2, owner); got.NeedsDecision || len(got.Review.Items) != 0 {
		t.Fatalf("installed shared artifact prompted again: %+v", got)
	}
	explicit, err := o2.Review(t.Context(), owner, true)
	if err != nil || explicit.NeedsDecision || len(explicit.Review.Items) != 0 {
		t.Fatalf("explicit review offered an installed shared artifact: %+v %v", explicit, err)
	}
}
