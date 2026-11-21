package worktree

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// BenchmarkPlanInvestigationPinCycle measures the retained investigation's
// actual pin and unpin path, not baseline capture or checkout setup.
func BenchmarkPlanInvestigationPinCycle(b *testing.B) {
	base := b.TempDir()
	root, store := filepath.Join(base, "project"), filepath.Join(base, "artifacts")
	if err := os.Mkdir(root, 0700); err != nil {
		b.Fatal(err)
	}
	for _, args := range [][]string{{"init"}, {"-c", "user.email=test@example.invalid", "-c", "user.name=Test", "commit", "--allow-empty", "-m", "initial"}} {
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			b.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	payload := bytes.Repeat([]byte{0x00, 0xfc, 0x03, 0xf9}, 7*(4<<18))
	for i := range 4 {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("fixture-%d.bin", i)), payload, 0600); err != nil {
			b.Fatal(err)
		}
	}
	s, err := Open(root, store, Options{})
	if err != nil {
		b.Fatal(err)
	}
	binding := Investigation{SessionID: "session", Cycle: "cycle"}
	c, err := s.Create(b.Context(), Request{Owner: testOwner, Mode: Isolated, Investigation: binding})
	if err != nil {
		b.Fatal(err)
	}
	owner := Owner{AgentID: testOwner.AgentID, RunID: "next-run"}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := s.PinInvestigation(b.Context(), c.ID, binding, "", owner); err != nil {
			b.Fatal(err)
		}
		if err := s.Unpin(c.ID, owner); err != nil {
			b.Fatal(err)
		}
	}
}

func TestPlanInvestigationAdoptsExactReadyManifestAndRejectsOrdinaryEffects(t *testing.T) {
	root, store, s := fixture(t)
	req := Request{
		Owner: testOwner, Mode: Isolated, Investigation: Investigation{SessionID: "session", Cycle: "cycle"},
		Documents: map[string]Document{"a.txt": {Revision: 7, Exists: true, Data: []byte("accepted dirty revision\n")}},
	}
	c, err := s.Create(t.Context(), req)
	if err != nil || c.BaselineDigest == "" || len(c.Baseline) != 0 || !s.HasPlanInvestigations(req.Investigation.SessionID) {
		t.Fatalf("creation did not return compact baseline identity: %+v %v", c, err)
	}
	// Reopen before the caller has saved its session binding. Retry from a
	// new exact run must adopt these bytes, not recapture the changed source.
	s, err = Open(root, store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	req.Owner.RunID = "replacement-run"
	req.Documents["a.txt"] = Document{Revision: 8, Exists: true, Data: []byte("later accepted revision\n")}
	b, err := s.Create(t.Context(), req)
	if err != nil || b.ID != c.ID || b.BaselineDigest != c.BaselineDigest || !slices.Contains(b.Pins, req.Owner) || len(s.List()) != 1 {
		t.Fatalf("retry recaptured or lost exact checkout: %+v %v", b, err)
	}
	if err := s.Pin(c.ID, req.Owner); err == nil {
		t.Fatal("ordinary pin admitted an investigation")
	}
	for _, mode := range []Mode{Shared, Isolated} {
		if _, err := s.Create(t.Context(), Request{Owner: req.Owner, Mode: mode, SourceContextID: c.ID}); err == nil {
			t.Fatal("ordinary child was launched from an investigation:", mode)
		}
	}
	if err := s.ValidateOrdinaryMutation([]string{filepath.Join(root, "new-dir", "ordinary.txt")}); err != nil {
		t.Fatal("ordinary project effect was blocked", err)
	}
	for _, path := range []string{filepath.Join(c.Root, "new-dir", "experiment.txt"), filepath.Join(store, c.ID+".json")} {
		if err := s.ValidateOrdinaryMutation([]string{path}); err == nil {
			t.Fatal("ordinary effect could alter investigation", path)
		}
	}
	put(t, c.Root, "a.txt", []byte("experiment\n"))
	review, err := s.Review(t.Context(), c.ID)
	if err != nil || review.BaselineDigest != c.BaselineDigest || len(review.Changes) != 1 ||
		string(review.Changes[0].Before.Data) != "accepted dirty revision\n" ||
		string(review.Changes[0].After.Data) != "experiment\n" {
		t.Fatalf("source review lost exact accepted baseline or new bytes: %+v %v", review, err)
	}
	if err := s.ValidateDestination(t.Context(), review, nil); !errors.Is(err, ErrInvestigationIntegration) {
		t.Fatal("source-only plan accepted as an integration", err)
	}
}

func TestFailedInvestigationDoesNotDuplicateAndRequiresExactDiscard(t *testing.T) {
	root, store, s := fixture(t)
	req := Request{Owner: testOwner, Mode: Isolated, Investigation: Investigation{SessionID: "session", Cycle: "cycle"}}
	c, err := s.Create(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	put(t, c.Root, "harmless-remnant.txt", []byte("preparation evidence"))
	full, err := s.Inspect(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	full.Ready = false
	if err := s.save(full); err != nil {
		t.Fatal(err)
	}
	s, err = Open(root, store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.Create(t.Context(), req); err == nil || got.ID != c.ID || got.Ready || len(s.List()) != 1 {
		t.Fatalf("failed checkout was run or duplicated: %+v: %v", got, err)
	}
	if err := s.Unpin(c.ID, c.Owner); err != nil {
		t.Fatal(err)
	}
	review, err := s.ReviewDiscard(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Cleanup(t.Context(), c.ID, c.Owner, CleanupOptions{Discard: true, Digest: review.DiscardDigest}); err == nil {
		t.Fatal("discard bypassed retained-use authority")
	}
	opts := CleanupOptions{Discard: true, Digest: review.DiscardDigest, Check: func(context.Context) error { return nil }}
	if err := s.Cleanup(t.Context(), c.ID, c.Owner, opts); err != nil {
		t.Fatal(err)
	}
	archived, err := s.Inspect(c.ID)
	if err != nil || !archived.Removed || string(archived.ArchivedResult["harmless-remnant.txt"].Data) != "preparation evidence" {
		t.Fatalf("reviewed cleanup lost remnants: %+v: %v", archived, err)
	}
	if s.HasPlanInvestigations(req.Investigation.SessionID) {
		t.Fatal("removed investigation still schedules cleanup")
	}
	ready, err := s.Create(t.Context(), req)
	if err != nil || ready.ID == c.ID || !ready.Ready {
		t.Fatalf("reviewed retirement did not allow fresh preparation: %+v: %v", ready, err)
	}
}

func TestPlanInvestigationStoresMutableStateApartFromExactBaseline(t *testing.T) {
	root, store, s := fixture(t)
	binding := Investigation{SessionID: "session", Cycle: "cycle"}
	dirty := bytes.Repeat([]byte{0, 0xfc, 0x03, 0xf9}, 1<<18)
	c, err := s.Create(t.Context(), Request{
		Owner: testOwner, Mode: Isolated, Investigation: binding,
		Documents: map[string]Document{
			"a.txt":   {Revision: 7, Exists: true, Data: dirty},
			"deleted": {Revision: 9, Exists: false},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	baselinePath := filepath.Join(store, c.ID+".plan-baseline")
	beforeInfo, err := os.Stat(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	owner := Owner{AgentID: testOwner.AgentID, RunID: "next-run"}
	pinned, err := s.PinInvestigation(t.Context(), c.ID, binding, "", owner)
	if err != nil || !slices.Contains(pinned.Pins, owner) {
		t.Fatalf("pin was not admitted: %+v %v", pinned, err)
	}
	pinned.Investigation.Cycle = "mutated summary"
	pinned.Pins[0] = Owner{}
	if err := s.Unpin(c.ID, testOwner); err != nil {
		t.Fatal(err)
	}
	compact, err := s.InspectSummary(c.ID)
	if err != nil || compact.Investigation == nil || *compact.Investigation != binding ||
		!slices.Equal(compact.Pins, []Owner{owner}) || compact.Baseline != nil || compact.DocumentRevisions != nil {
		t.Fatalf("summary aliased state or included evidence: %+v %v", compact, err)
	}
	ledger, err := os.Stat(filepath.Join(store, c.ID+".json"))
	if err != nil || ledger.Size() > 8<<10 {
		t.Fatalf("pin updates did not stay compact: %v %v", ledger, err)
	}
	afterInfo, err := os.Stat(baselinePath)
	if err != nil || !os.SameFile(beforeInfo, afterInfo) || beforeInfo.ModTime() != afterInfo.ModTime() {
		t.Fatalf("pin updates replaced or changed immutable evidence: %v %v %v", beforeInfo, afterInfo, err)
	}
	if got := s.List(); len(got) != 1 || got[0].StorageBytes != ledger.Size()+beforeInfo.Size() {
		t.Fatalf("inventory did not count live evidence: %+v", got)
	}
	s, err = Open(root, store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	full, err := s.Inspect(c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(full.Pins, []Owner{owner}) || full.BaselineDigest != c.BaselineDigest ||
		!bytes.Equal(full.Baseline["a.txt"].Data, dirty) || full.Baseline["a.txt"].DocumentRevision != 7 ||
		full.Baseline["deleted"].Type != "" || full.DocumentRevisions["deleted"] != 9 {
		t.Fatal("restart lost retained pins, accepted bytes, or revisions")
	}
	full.Baseline["a.txt"].Data[0] = 99
	full.DocumentRevisions["deleted"] = 1
	full, err = s.Inspect(c.ID)
	if err != nil || !bytes.Equal(full.Baseline["a.txt"].Data, dirty) || full.DocumentRevisions["deleted"] != 9 {
		t.Fatal("exact evidence reader exposed mutable storage", err)
	}
}

func TestPlanInvestigationPinWriteFailureRetainsPreviousState(t *testing.T) {
	root, store, s := fixture(t)
	binding := Investigation{SessionID: "session", Cycle: "cycle"}
	c, err := s.Create(t.Context(), Request{Owner: testOwner, Mode: Isolated, Investigation: binding})
	if err != nil {
		t.Fatal(err)
	}
	ledger := filepath.Join(store, c.ID+".json")
	if err := os.Rename(ledger, ledger+".backed-up"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(ledger, 0700); err != nil {
		t.Fatal(err)
	}
	owner := Owner{AgentID: testOwner.AgentID, RunID: "next-run"}
	if _, err := s.PinInvestigation(t.Context(), c.ID, binding, "", owner); err == nil {
		t.Fatal("pin reported success when its ledger could not be replaced")
	}
	if compact, err := s.InspectSummary(c.ID); err != nil || !slices.Equal(compact.Pins, []Owner{testOwner}) {
		t.Fatalf("failed pin changed live state: %+v %v", compact, err)
	}
	if err := os.Rename(ledger, ledger+".obstacle"); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(ledger+".backed-up", ledger); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(root, store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := reopened.PinInvestigation(t.Context(), c.ID, binding, "", owner); err != nil || !slices.Equal(got.Pins, []Owner{testOwner, owner}) {
		t.Fatalf("retry did not pin the exact retained checkout: %+v %v", got, err)
	}
}

func TestPlanInvestigationDisposalRequiresVerifiedDurableEvidence(t *testing.T) {
	root, store, s := fixture(t)
	req := Request{Owner: testOwner, Mode: Isolated, Investigation: Investigation{SessionID: "session", Cycle: "cycle"}}
	c, err := s.Create(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	put(t, c.Root, "secret", []byte("ignore but preserve the result"))
	opts := CleanupOptions{Discard: true, Check: func(context.Context) error { return nil }}
	if opts.Digest, err = s.PrepareInvestigationDiscard(t.Context(), c.ID); err != nil || opts.Digest == "" {
		t.Fatal("could not prepare disposal", err)
	}
	opts.InvestigationAutomatic = true
	if err := s.Cleanup(t.Context(), c.ID, c.Owner, opts); err == nil {
		t.Fatal("verified discard overrode run retention")
	}
	if err := s.Unpin(c.ID, c.Owner); err != nil {
		t.Fatal(err)
	}

	baselinePath := filepath.Join(store, c.ID+".plan-baseline")
	baseline, err := os.ReadFile(baselinePath)
	if err != nil {
		t.Fatal(err)
	}
	corrupt := bytes.Replace(baseline, []byte("Y29tbWl0dGVkCg=="), []byte("eG9tbWl0dGVkCg=="), 1)
	if bytes.Equal(corrupt, baseline) {
		t.Fatal("test did not change the baseline payload")
	}
	put(t, store, filepath.Base(baselinePath), corrupt)
	if _, err := Open(root, store, Options{}); err == nil {
		t.Fatal("cold resume accepted corrupt baseline bytes")
	}
	if err := s.Cleanup(t.Context(), c.ID, c.Owner, opts); err == nil {
		t.Fatal("warm service disposed of checkout without durable baseline")
	}
	put(t, store, filepath.Base(baselinePath), baseline)

	// A staged artifact can survive a crash before publication of its
	// ledger reference. Do not trust its name or replace it on retry.
	exact, err := s.capture(t.Context(), c.Root, c.CommonGitDir, nil, []string{"secret"}, true)
	if err != nil {
		t.Fatal(err)
	}
	result, err := json.Marshal(exact)
	if err != nil {
		t.Fatal(err)
	}
	resultPath := filepath.Join(store, c.ID+"."+hash(result)+".plan-result")
	put(t, store, filepath.Base(resultPath), []byte("corrupt orphan"))
	s, err = Open(root, store, Options{})
	if err != nil {
		t.Fatal("an unreferenced staging file changed the live ledger", err)
	}
	if err := s.Cleanup(t.Context(), c.ID, c.Owner, opts); err == nil {
		t.Fatal("discard accepted corrupt staged archive bytes")
	}
	if data, err := os.ReadFile(filepath.Join(c.Root, "secret")); err != nil || string(data) != "ignore but preserve the result" {
		t.Fatal("failed archive attempt removed or changed checkout bytes", err)
	}
	put(t, store, filepath.Base(resultPath), result)
	if err := s.Cleanup(t.Context(), c.ID, c.Owner, opts); err != nil {
		t.Fatal("discard refused verified staged evidence", err)
	}
	if _, err := os.Stat(c.Root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("checkout remained after verified archival", err)
	}
	s, err = Open(root, store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	archived, err := s.Inspect(c.ID)
	if err != nil || !archived.Removed || archived.ArchiveCommit == "" || archived.BaselineDigest != c.BaselineDigest ||
		!reflect.DeepEqual(archived.ArchivedResult, exact) {
		t.Fatalf("restart lost verified archive or baseline identity: %+v %v", archived, err)
	}
	if string(archived.Baseline["a.txt"].Data) != "committed\n" || s.HasPlanInvestigations(req.Investigation.SessionID) {
		t.Fatal("retirement lost original bytes or kept a live investigation")
	}
	put(t, store, filepath.Base(resultPath), []byte("corrupt archived result"))
	if compact, err := s.InspectSummary(c.ID); err != nil || !compact.Removed || compact.ArchivedResult != nil {
		t.Fatalf("summary did not remain available without reading the archive: %+v %v", compact, err)
	}
	if _, err := s.Inspect(c.ID); err == nil {
		t.Fatal("exact reader accepted corrupt archived results")
	}
}
