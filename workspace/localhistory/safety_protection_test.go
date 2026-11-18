package localhistory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

func protectionDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func protectionChange(t *testing.T, reference string, manifest Manifest) SafetyProtectionChange {
	t.Helper()
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return SafetyProtectionChange{
		Reference: reference, SnapshotID: manifest.ID, Action: "release",
		Evidence: SafetyProtectionEvidence{
			WorkspaceKey: protectionDigest([]byte("domain-owned-canonical-root")),
			OperationID:  reference, OperationDigest: protectionDigest([]byte("original-preview")),
			JournalSHA256:  protectionDigest([]byte("durable-original-journal")),
			ManifestSHA256: protectionDigest(payload), Outcome: "committed",
			OwnerClientID: "original-owner", OwnerEpoch: 4,
		},
		ActorClientID: "replacement-human-client", ActorEpoch: 7, RequestID: "release-1",
	}
}

func protectionExec(t *testing.T, store *Store, query string, args ...any) {
	t.Helper()
	if _, err := store.db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func protectionCounts(t *testing.T, store *Store, pins, events int) {
	t.Helper()
	var gotPins, gotEvents int
	if err := store.db.QueryRow(`SELECT (SELECT COUNT(*) FROM history_retained_snapshots),
(SELECT COUNT(*) FROM history_safety_protection_events)`).Scan(&gotPins, &gotEvents); err != nil {
		t.Fatal(err)
	}
	if gotPins != pins || gotEvents != events {
		t.Fatalf("pins/events = %d/%d, want %d/%d", gotPins, gotEvents, pins, events)
	}
}

func protectionImmutableBytes(t *testing.T, store *Store) map[string]string {
	t.Helper()
	out := make(map[string]string)
	for _, dir := range []string{"manifests", "objects"} {
		err := filepath.WalkDir(filepath.Join(store.root, dir), func(path string, entry os.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[path] = protectionDigest(data)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func TestSafetyProtectionLifecycleExactPinsPruneReplayAndReopen(t *testing.T) {
	store, workspace := newTestStore(t)
	ctx := context.Background()
	writeTestFile(t, workspace, "review.txt", []byte("original\x00\r\n"), 0o640)
	before := retainedSnapshot(t, store, "review-1")
	writeTestFile(t, workspace, "review.txt", []byte("different original for near neighbor"), 0o640)
	other := retainedSnapshot(t, store, "review-10")
	protectionExec(t, store, `INSERT INTO history_retained_snapshots(reference,snapshot_id) VALUES('review-1_extra',?)`, before.ID)
	in := protectionChange(t, "review-1", before)
	immutable := protectionImmutableBytes(t, store)
	event, err := store.ChangeSafetyProtection(ctx, in)
	if err != nil || event.Revision != 1 || event.Action != "release" || event.Change != in || event.Snapshot.ID != before.ID {
		t.Fatalf("release: %+v %v", event, err)
	}
	protectionCounts(t, store, 2, 1)
	if !reflect.DeepEqual(immutable, protectionImmutableBytes(t, store)) {
		t.Fatal("release rewrote/deleted immutable evidence")
	}
	if got := string(readTestFile(t, workspace, "review.txt")); got != "different original for near neighbor" {
		t.Fatalf("release changed project source: %q", got)
	}
	for ref, id := range map[string]string{"review-10": other.ID, "review-1_extra": before.ID} {
		loaded, err := store.LoadRetainedSnapshot(ctx, ref)
		if err != nil || loaded.ID != id {
			t.Fatalf("release changed exact neighbor %q: %s %v", ref, loaded.ID, err)
		}
	}
	record, manifest, err := store.InspectSafetyProtection(ctx, in.Reference)
	if err != nil || record.Retained || !record.Available || record.Revision != 1 || !reflect.DeepEqual(manifest, before) {
		t.Fatalf("inspect released original: %+v %+v %v", record, manifest, err)
	}
	// Returned manifest is detached from subsequent inspections.
	delete(manifest.Entries, "review.txt")
	_, reloaded, err := store.InspectSafetyProtection(ctx, in.Reference)
	if err != nil || !reflect.DeepEqual(reloaded, before) {
		t.Fatalf("inspection leaked mutable state: %+v %v", reloaded, err)
	}
	again := in
	again.Action, again.ExpectedRevision, again.RequestID = "reprotect", 1, "protect-2"
	reprotected, err := store.ChangeSafetyProtection(ctx, again)
	if err != nil || reprotected.Revision != 2 {
		t.Fatalf("reprotect: %+v %v", reprotected, err)
	}
	if err := store.ReleaseRetainedSnapshot(ctx, in.Reference); err == nil {
		t.Fatal("low-level release bypassed audited protection")
	}
	for _, request := range []SafetyProtectionChange{in, again} {
		replayed, err := store.ChangeSafetyProtection(ctx, request)
		want := event
		if request.Action == "reprotect" {
			want = reprotected
		}
		if err != nil || replayed != want {
			t.Fatalf("exact historical replay: %+v %v", replayed, err)
		}
	}
	release3 := in
	release3.ExpectedRevision, release3.RequestID = 2, "release-3"
	if _, err := store.ChangeSafetyProtection(ctx, release3); err != nil {
		t.Fatal(err)
	}
	if err := store.ReleaseRetainedSnapshot(ctx, "review-1_extra"); err != nil {
		t.Fatal(err)
	}
	retentionSnapshot(t, store, "latest\n")
	if _, err := store.Prune(ctx, RetentionPolicy{MaxSnapshots: 1, BudgetBytes: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadManifest(before.ID); !os.IsNotExist(err) {
		t.Fatalf("audit secretly pinned released snapshot: %v", err)
	}
	if _, err := store.readFileBytes(before.Entries["review.txt"].Hash); !os.IsNotExist(err) {
		t.Fatalf("released unique content not collected: %v", err)
	}
	root := store.root
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = Open(root, workspace)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	record, manifest, err = store.InspectSafetyProtection(ctx, in.Reference)
	if err != nil || record.Retained || record.Available || record.Revision != 3 ||
		record.Snapshot != event.Snapshot || manifest.ID != "" {
		t.Fatalf("pruned durable audit: %+v %+v %v", record, manifest, err)
	}
	for _, request := range []SafetyProtectionChange{in, again} {
		replayed, err := store.ChangeSafetyProtection(ctx, request)
		want := event
		if request.Action == "reprotect" {
			want = reprotected
		}
		if err != nil || replayed != want {
			t.Fatalf("post-prune historical replay: %+v %v", replayed, err)
		}
	}
	again.ExpectedRevision, again.RequestID = 3, "protect-4"
	if _, err := store.ChangeSafetyProtection(ctx, again); err == nil {
		t.Fatal("reprotected a collected snapshot")
	}
	if _, err := store.LoadManifest(before.ID); !os.IsNotExist(err) {
		t.Fatalf("reprotection resurrected original: %v", err)
	}
	if _, err := store.SnapshotRetained(ctx, in.Reference, SnapshotOptions{Kind: "safety"}); err == nil {
		t.Fatal("historical reference captured new bytes")
	}
	if _, err := store.SnapshotRetainedFiles(ctx, in.Reference, "new", []RetainedFile{
		{Path: "new.txt", Mode: 0o644, Data: []byte("later bytes")},
	}); err == nil {
		t.Fatal("selected capture reused historical reference")
	}
	if err := store.ReleaseRetainedSnapshot(ctx, in.Reference); err == nil {
		t.Fatal("low-level release silently accepted audited released identity")
	}
	protectionCounts(t, store, 1, 3)
	var scopes int
	if err := store.db.QueryRow(`SELECT (SELECT COUNT(*) FROM history_message_scopes)+(SELECT COUNT(*) FROM history_agent_operations)`).Scan(&scopes); err != nil || scopes != 0 {
		t.Fatalf("fabricated agent/message scope: %d %v", scopes, err)
	}
}

func TestSafetyProtectionListingBeyondTimelineAndMetadataOnly(t *testing.T) {
	store, workspace := newTestStore(t)
	ctx := context.Background()
	writeTestFile(t, workspace, "review.txt", []byte("original"), 0o644)
	manifest := retainedSnapshot(t, store, "review-1")
	refs := []string{"review-1"}
	tx, err := store.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for i := range 1105 {
		ref := fmt.Sprintf("R-%04d", i)
		refs = append(refs, ref)
		if _, err := tx.Exec(`INSERT INTO history_retained_snapshots(reference,snapshot_id) VALUES(?,?)`, ref, manifest.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ChangeSafetyProtection(ctx, protectionChange(t, "review-1", manifest)); err != nil {
		t.Fatal(err)
	}
	sort.Strings(refs)
	var found []string
	var cursor string
	for {
		page, err := store.ListSafetyProtections(ctx, cursor, 37)
		if err != nil || page.RetainedCount != 1105 || len(page.Records) > 37 {
			t.Fatalf("page: %+v %v", page, err)
		}
		for _, record := range page.Records {
			found = append(found, record.Reference)
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor {
			t.Fatal("cursor did not advance")
		}
		cursor = page.NextCursor
	}
	if !reflect.DeepEqual(found, refs) {
		t.Fatalf("paging lost/duplicated old references: got %d want %d", len(found), len(refs))
	}
	for limit, count := range map[int]int{0: 100, 200: 200} {
		page, err := store.ListSafetyProtections(ctx, "", limit)
		if err != nil || len(page.Records) != count || page.NextCursor == "" {
			t.Fatalf("limit %d: count %d cursor %q err %v", limit, len(page.Records), page.NextCursor, err)
		}
	}
	// Listing does not read or promise health of every snapshot's objects.
	path, err := objectPath(store.root, "files", manifest.Entries["review.txt"].Hash, ".json")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	page, err := store.ListSafetyProtections(ctx, "", 1)
	if err != nil || !page.Records[0].Available {
		t.Fatalf("metadata listing pretended to verify bytes: %+v %v", page, err)
	}
	if _, _, err := store.InspectSafetyProtection(ctx, refs[0]); err == nil {
		t.Fatal("inspection accepted corrupt available content")
	}
}

func TestSafetyProtectionRejectsInvalidShapeBeforeStoreAccess(t *testing.T) {
	valid := protectionChange(t, "review-1", Manifest{ID: strings.Repeat("a", 64)})
	var store *Store
	mutations := []func(*SafetyProtectionChange){
		func(in *SafetyProtectionChange) { in.Reference = "../bad" },
		func(in *SafetyProtectionChange) { in.SnapshotID = strings.Repeat("A", 64) },
		func(in *SafetyProtectionChange) { in.Action = "prune" },
		func(in *SafetyProtectionChange) { in.ExpectedRevision = math.MaxUint64 },
		func(in *SafetyProtectionChange) { in.Evidence.OperationID = "review-10" },
		func(in *SafetyProtectionChange) { in.Evidence.WorkspaceKey = strings.Repeat("A", 64) },
		func(in *SafetyProtectionChange) { in.Evidence.OperationDigest = "" },
		func(in *SafetyProtectionChange) { in.Evidence.JournalSHA256 = "bad" },
		func(in *SafetyProtectionChange) { in.Evidence.ManifestSHA256 = "bad" },
		func(in *SafetyProtectionChange) { in.Evidence.Outcome = "unconfirmed" },
		func(in *SafetyProtectionChange) { in.Evidence.Outcome = "prepared" },
		func(in *SafetyProtectionChange) { in.Evidence.OwnerClientID = "" },
		func(in *SafetyProtectionChange) { in.Evidence.OwnerClientID = strings.Repeat("a", 513) },
		func(in *SafetyProtectionChange) { in.Evidence.OwnerClientID = "bad\nowner" },
		func(in *SafetyProtectionChange) { in.Evidence.OwnerEpoch = 0 },
		func(in *SafetyProtectionChange) { in.ActorClientID = "" },
		func(in *SafetyProtectionChange) { in.ActorClientID = "\xff" },
		func(in *SafetyProtectionChange) { in.ActorClientID = strings.Repeat("a", 513) },
		func(in *SafetyProtectionChange) { in.ActorEpoch = 0 },
		func(in *SafetyProtectionChange) { in.RequestID = "" },
		func(in *SafetyProtectionChange) { in.RequestID = "bad\x00request" },
		func(in *SafetyProtectionChange) { in.RequestID = strings.Repeat("a", 257) },
	}
	for i, mutate := range mutations {
		in := valid
		mutate(&in)
		if _, err := store.ChangeSafetyProtection(context.Background(), in); err == nil {
			t.Fatalf("invalid shape %d accepted", i)
		}
	}
	for _, input := range []struct {
		cursor string
		limit  int
	}{
		{"", -1}, {"", 201}, {"%", 1}, {strings.Repeat("A", 173), 1},
		{base64.RawURLEncoding.EncodeToString([]byte("../bad")), 1},
		{base64.URLEncoding.EncodeToString([]byte("a")), 1},
		{"YQ\n", 1},
	} {
		if _, err := store.ListSafetyProtections(context.Background(), input.cursor, input.limit); err == nil {
			t.Fatalf("invalid page accepted: %+v", input)
		}
	}
	if _, _, err := store.InspectSafetyProtection(context.Background(), "../bad"); err == nil {
		t.Fatal("invalid inspect accepted")
	}
}

func TestSafetyProtectionStaleMismatchedReplayAndImmutableEvidence(t *testing.T) {
	store, workspace := newTestStore(t)
	ctx := context.Background()
	writeTestFile(t, workspace, "review.txt", []byte("before"), 0o644)
	manifest := retainedSnapshot(t, store, "review-1")
	in := protectionChange(t, "review-1", manifest)
	// A syntactically valid but incorrect original manifest hash cannot release.
	wrong := in
	wrong.Evidence.ManifestSHA256 = protectionDigest([]byte("wrong"))
	if _, err := store.ChangeSafetyProtection(ctx, wrong); err == nil {
		t.Fatal("wrong original manifest binding accepted")
	}
	protectionCounts(t, store, 1, 0)
	event, err := store.ChangeSafetyProtection(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*SafetyProtectionChange){
		func(c *SafetyProtectionChange) { c.SnapshotID = strings.Repeat("b", 64) },
		func(c *SafetyProtectionChange) { c.Reference, c.Evidence.OperationID = "review-10", "review-10" },
		func(c *SafetyProtectionChange) { c.ExpectedRevision = 1 },
		func(c *SafetyProtectionChange) { c.Action = "reprotect" },
		func(c *SafetyProtectionChange) { c.ActorEpoch++ },
		func(c *SafetyProtectionChange) { c.Evidence.OwnerEpoch++ },
		func(c *SafetyProtectionChange) { c.Evidence.OperationDigest = strings.Repeat("b", 64) },
		func(c *SafetyProtectionChange) { c.Evidence.JournalSHA256 = strings.Repeat("b", 64) },
	} {
		wrong := in
		mutate(&wrong)
		if _, err := store.ChangeSafetyProtection(ctx, wrong); err == nil {
			t.Fatalf("mismatched replay accepted: %+v", wrong)
		}
	}
	stale := in
	stale.RequestID = "new-stale-request"
	if _, err := store.ChangeSafetyProtection(ctx, stale); err == nil {
		t.Fatal("stale revision accepted")
	}
	foreign := in
	foreign.ActorClientID = "different-client"
	if _, err := store.ChangeSafetyProtection(ctx, foreign); err == nil {
		t.Fatal("foreign request incorrectly replayed another actor's release")
	}
	reprotect := in
	reprotect.Action, reprotect.ExpectedRevision, reprotect.RequestID = "reprotect", 1, "protect-2"
	for _, mutate := range []func(*SafetyProtectionEvidence){
		func(e *SafetyProtectionEvidence) { e.WorkspaceKey = strings.Repeat("c", 64) },
		func(e *SafetyProtectionEvidence) { e.OperationDigest = strings.Repeat("c", 64) },
		func(e *SafetyProtectionEvidence) { e.JournalSHA256 = strings.Repeat("c", 64) },
		func(e *SafetyProtectionEvidence) { e.ManifestSHA256 = strings.Repeat("c", 64) },
		func(e *SafetyProtectionEvidence) { e.Outcome = "refused" },
		func(e *SafetyProtectionEvidence) { e.OwnerClientID = "substitute-owner" },
		func(e *SafetyProtectionEvidence) { e.OwnerEpoch++ },
	} {
		wrong := reprotect
		mutate(&wrong.Evidence)
		if _, err := store.ChangeSafetyProtection(ctx, wrong); err == nil {
			t.Fatalf("rewrote original evidence: %+v", wrong.Evidence)
		}
	}
	protectionCounts(t, store, 0, 1)
	replayed, err := store.ChangeSafetyProtection(ctx, in)
	if err != nil || replayed != event {
		t.Fatalf("refusals changed original event: %+v %v", replayed, err)
	}
	if _, _, err := store.InspectSafetyProtection(ctx, "unknown"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("unknown reference fabricated disposition: %v", err)
	}
}

func TestSafetyProtectionTransactionRollback(t *testing.T) {
	for _, fault := range []string{"event insert", "event insert ignored", "pin delete", "pin delete ignored", "commit", "pin insert", "pin insert ignored"} {
		t.Run(fault, func(t *testing.T) {
			store, workspace := newTestStore(t)
			ctx := context.Background()
			writeTestFile(t, workspace, "review.txt", []byte("before"), 0o644)
			manifest := retainedSnapshot(t, store, "review-1")
			in := protectionChange(t, "review-1", manifest)
			pins, events := 1, 0
			switch fault {
			case "event insert":
				protectionExec(t, store, `CREATE TRIGGER fail_event BEFORE INSERT ON history_safety_protection_events BEGIN SELECT RAISE(ABORT,'injected'); END`)
			case "event insert ignored":
				protectionExec(t, store, `CREATE TRIGGER fail_event BEFORE INSERT ON history_safety_protection_events BEGIN SELECT RAISE(IGNORE); END`)
			case "pin delete":
				protectionExec(t, store, `CREATE TRIGGER fail_pin BEFORE DELETE ON history_retained_snapshots BEGIN SELECT RAISE(ABORT,'injected after event'); END`)
			case "pin delete ignored":
				protectionExec(t, store, `CREATE TRIGGER fail_pin BEFORE DELETE ON history_retained_snapshots BEGIN SELECT RAISE(IGNORE); END`)
			case "commit":
				protectionExec(t, store, `CREATE TABLE fault_parent(id INTEGER PRIMARY KEY);
CREATE TABLE fault_child(id INTEGER REFERENCES fault_parent(id) DEFERRABLE INITIALLY DEFERRED);
CREATE TRIGGER fail_commit AFTER DELETE ON history_retained_snapshots BEGIN INSERT INTO fault_child(id) VALUES(1); END`)
			default:
				if _, err := store.ChangeSafetyProtection(ctx, in); err != nil {
					t.Fatal(err)
				}
				in.Action, in.ExpectedRevision, in.RequestID = "reprotect", 1, "protect-2"
				pins, events = 0, 1
				raise := "ABORT,'injected after event'"
				if fault == "pin insert ignored" {
					raise = "IGNORE"
				}
				protectionExec(t, store, `CREATE TRIGGER fail_pin BEFORE INSERT ON history_retained_snapshots BEGIN SELECT RAISE(`+raise+`); END`)
			}
			if result, err := store.ChangeSafetyProtection(ctx, in); err == nil || result.ID != "" {
				t.Fatalf("fault claimed success: %+v %v", result, err)
			}
			protectionCounts(t, store, pins, events)
			if _, err := store.LoadManifest(manifest.ID); err != nil {
				t.Fatalf("rollback destroyed manifest: %v", err)
			}
			record, _, err := store.InspectSafetyProtection(ctx, in.Reference)
			if err != nil || record.Retained != (pins == 1) || record.Revision != uint64(events) {
				t.Fatalf("rollback left audit/pin contradiction: %+v %v", record, err)
			}
		})
	}
}

func TestSafetyProtectionStoredCorruptionFailsClosed(t *testing.T) {
	for _, damage := range []string{"malformed payload", "oversized payload", "unknown payload field", "indexed action",
		"payload identity", "payload owner", "payload outcome", "unexpected pin", "missing pin", "original evidence changed", "revision gap"} {
		t.Run(damage, func(t *testing.T) {
			store, workspace := newTestStore(t)
			ctx := context.Background()
			writeTestFile(t, workspace, "review.txt", []byte("before"), 0o644)
			manifest := retainedSnapshot(t, store, "review-1")
			in := protectionChange(t, "review-1", manifest)
			event, err := store.ChangeSafetyProtection(ctx, in)
			if err != nil {
				t.Fatal(err)
			}
			switch damage {
			case "malformed payload":
				protectionExec(t, store, `UPDATE history_safety_protection_events SET payload='{}'`)
			case "oversized payload":
				protectionExec(t, store, `UPDATE history_safety_protection_events SET payload=?`, strings.Repeat("a", safetyProtectionPayloadLimit+1))
			case "unknown payload field":
				payload, err := json.Marshal(event)
				if err != nil {
					t.Fatal(err)
				}
				protectionExec(t, store, `UPDATE history_safety_protection_events SET payload=?`, `{"Unknown":1,`+string(payload[1:]))
			case "indexed action":
				protectionExec(t, store, `UPDATE history_safety_protection_events SET action='reprotect'`)
			case "unexpected pin":
				protectionExec(t, store, `INSERT INTO history_retained_snapshots(reference,snapshot_id) VALUES(?,?)`, in.Reference, in.SnapshotID)
			case "missing pin", "original evidence changed", "revision gap":
				reprotect := in
				reprotect.Action, reprotect.ExpectedRevision, reprotect.RequestID = "reprotect", 1, "protect-2"
				event, err = store.ChangeSafetyProtection(ctx, reprotect)
				if err != nil {
					t.Fatal(err)
				}
				if damage == "missing pin" {
					protectionExec(t, store, `DELETE FROM history_retained_snapshots`)
					break
				}
				if damage == "revision gap" {
					event.Revision, event.Change.ExpectedRevision = 4, 3
					protectionExec(t, store, `UPDATE history_safety_protection_events SET revision=4 WHERE event_id=?`, event.ID)
				} else {
					event.Change.Evidence.JournalSHA256 = strings.Repeat("c", 64)
				}
				fallthrough
			default:
				switch damage {
				case "payload identity":
					event.SnapshotID = strings.Repeat("b", 64)
				case "payload owner":
					event.Change.Evidence.OwnerClientID = "bad\nowner"
				case "payload outcome":
					event.Change.Evidence.Outcome = "unconfirmed"
				}
				payload, err := json.Marshal(event)
				if err != nil {
					t.Fatal(err)
				}
				protectionExec(t, store, `UPDATE history_safety_protection_events SET payload=? WHERE event_id=?`, string(payload), event.ID)
			}
			if _, _, err := store.InspectSafetyProtection(ctx, in.Reference); err == nil {
				t.Fatal("inspection silently normalized corrupt audited state")
			}
			if _, err := store.ListSafetyProtections(ctx, "", 10); err == nil {
				t.Fatal("list silently rendered contradictory/corrupt state")
			}
			newRequest := in
			newRequest.Action, newRequest.ExpectedRevision, newRequest.RequestID = "reprotect", 1, "new-protect"
			if _, err := store.ChangeSafetyProtection(ctx, newRequest); err == nil {
				t.Fatal("change accepted corrupt audited state")
			}
		})
	}
}

func TestSafetyProtectionMissingOrCorruptOriginalEvidence(t *testing.T) {
	for _, damage := range []string{"index", "manifest", "entry", "object", "chunk"} {
		for _, released := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/released=%t", damage, released), func(t *testing.T) {
				store, workspace := newTestStore(t)
				ctx := context.Background()
				writeTestFile(t, workspace, "review.txt", []byte("before"), 0o644)
				manifest := retainedSnapshot(t, store, "review-1")
				in := protectionChange(t, "review-1", manifest)
				if released {
					if _, err := store.ChangeSafetyProtection(ctx, in); err != nil {
						t.Fatal(err)
					}
					in.Action, in.ExpectedRevision, in.RequestID = "reprotect", 1, "protect-2"
				}
				switch damage {
				case "index":
					protectionExec(t, store, `PRAGMA foreign_keys=OFF`)
					protectionExec(t, store, `DELETE FROM history_snapshots WHERE snapshot_id=?`, manifest.ID)
					protectionExec(t, store, `PRAGMA foreign_keys=ON`)
				case "manifest":
					if err := os.Remove(filepath.Join(store.root, "manifests", manifest.ID+".json")); err != nil {
						t.Fatal(err)
					}
				case "entry":
					protectionExec(t, store, `UPDATE history_snapshot_entries SET mode=0 WHERE snapshot_id=?`, manifest.ID)
				default:
					hash := manifest.Entries["review.txt"].Hash
					path, err := objectPath(store.root, "files", hash, ".json")
					if err != nil {
						t.Fatal(err)
					}
					if damage == "chunk" {
						object, err := store.readFileObject(hash)
						if err != nil {
							t.Fatal(err)
						}
						path, err = objectPath(store.root, "chunks", object.Chunks[0].Hash, "")
						if err != nil {
							t.Fatal(err)
						}
					}
					if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := store.ChangeSafetyProtection(ctx, in); err == nil {
					t.Fatal("transition accepted missing/corrupt original")
				}
				events, pins := 0, 1
				if released {
					events, pins = 1, 0
				}
				protectionCounts(t, store, pins, events)
				record, _, err := store.InspectSafetyProtection(ctx, in.Reference)
				if released && damage == "index" {
					if err != nil || record.Available {
						t.Fatalf("released missing index not represented honestly: %+v %v", record, err)
					}
				} else if err == nil {
					t.Fatal("inspection accepted corrupt available original")
				}
			})
		}
	}
}

func TestSafetyProtectionProcessGateCancellationAndShutdown(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "review.txt", []byte("before"), 0o644)
	manifest := retainedSnapshot(t, store, "review-1")
	in := protectionChange(t, "review-1", manifest)
	lock := flock.New(filepath.Join(store.root, "mutation.lock"))
	if err := lock.Lock(); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []func(context.Context) error{
		func(ctx context.Context) error { _, err := store.ListSafetyProtections(ctx, "", 1); return err },
		func(ctx context.Context) error {
			_, _, err := store.InspectSafetyProtection(ctx, in.Reference)
			return err
		},
		func(ctx context.Context) error { _, err := store.ChangeSafetyProtection(ctx, in); return err },
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
		err := operation(ctx)
		cancel()
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("bypassed process gate: %v", err)
		}
	}
	if err := lock.Unlock(); err != nil {
		t.Fatal(err)
	}
	protectionCounts(t, store, 1, 0)
	ctx := &retainedOperationContext{Context: context.Background(), entered: make(chan struct{}), resume: make(chan struct{})}
	changed := make(chan error, 1)
	go func() { _, err := store.ChangeSafetyProtection(ctx, in); changed <- err }()
	<-ctx.entered
	closed := make(chan error, 1)
	go func() { closed <- store.Close() }()
	select {
	case err := <-closed:
		close(ctx.resume)
		if cleanupErr := <-changed; cleanupErr != nil {
			t.Logf("Cleanup join: %v", cleanupErr)
		}
		t.Fatalf("shutdown did not join admitted audited change: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(ctx.resume)
	if err := <-changed; err != nil {
		t.Fatal(err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}
	if _, err := store.ListSafetyProtections(context.Background(), "", 1); err == nil {
		t.Fatal("closed store admitted list")
	}
	if _, _, err := store.InspectSafetyProtection(context.Background(), in.Reference); err == nil {
		t.Fatal("closed store admitted inspect")
	}
	if _, err := store.ChangeSafetyProtection(context.Background(), in); err == nil {
		t.Fatal("closed store admitted replay")
	}
	reopened, err := Open(store.root, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	protectionCounts(t, reopened, 0, 1)
}

func TestSafetyProtectionConcurrentPruneReleaseReprotect(t *testing.T) {
	for range 12 {
		store, workspace := newTestStore(t)
		ctx := context.Background()
		writeTestFile(t, workspace, "review.txt", []byte("before"), 0o644)
		manifest := retainedSnapshot(t, store, "review-1")
		// Do not depend on the original remaining the latest ordinary snapshot.
		writeTestFile(t, workspace, "review.txt", []byte("later"), 0o644)
		retentionSnapshot(t, store, "latest\n")
		in := protectionChange(t, "review-1", manifest)
		start := make(chan struct{})
		results := make(chan error, 2)
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.ChangeSafetyProtection(ctx, in)
			if err == nil {
				protect := in
				protect.Action, protect.ExpectedRevision, protect.RequestID = "reprotect", 1, "protect-2"
				// Either re-protection wins or pruning has already collected it.
				_, _ = store.ChangeSafetyProtection(ctx, protect)
			}
			results <- err
		}()
		go func() {
			defer wg.Done()
			<-start
			for range 3 {
				if _, err := store.Prune(ctx, RetentionPolicy{MaxSnapshots: 1, BudgetBytes: 1}); err != nil {
					results <- err
					return
				}
			}
			results <- nil
		}()
		close(start)
		wg.Wait()
		for range 2 {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		record, original, err := store.InspectSafetyProtection(ctx, in.Reference)
		if err != nil || record.Retained && (!record.Available || original.ID != manifest.ID) ||
			(record.Revision != 1 && record.Revision != 2) {
			t.Fatalf("concurrent mutation left dangling pin: %+v %+v %v", record, original, err)
		}
		if record.Retained {
			if _, err := store.LoadRetainedSnapshot(ctx, in.Reference); err != nil {
				t.Fatalf("concurrent pin does not verify: %v", err)
			}
		}
	}
}

func TestSafetyProtectionRefusedEvidenceAndBoundaryIdentities(t *testing.T) {
	store, workspace := newTestStore(t)
	writeTestFile(t, workspace, "review.txt", []byte("before"), 0o644)
	manifest := retainedSnapshot(t, store, "review-1")
	in := protectionChange(t, "review-1", manifest)
	in.Evidence.Outcome = "refused" // trusted positive-no-effect evidence, not inferred here
	in.Evidence.OwnerClientID = strings.Repeat("a", 512)
	in.Evidence.OwnerEpoch = math.MaxUint64
	in.ActorClientID = strings.Repeat("b", 512)
	in.ActorEpoch = math.MaxUint64
	in.RequestID = strings.Repeat("c", 256)
	first, err := store.ChangeSafetyProtection(context.Background(), in)
	if err != nil || first.Change != in {
		t.Fatalf("valid bounded identities/refused evidence: %+v %v", first, err)
	}
	again, err := store.ChangeSafetyProtection(context.Background(), in)
	if err != nil || again != first {
		t.Fatalf("bounded payload did not roundtrip: %+v %v", again, err)
	}
}

func TestSafetyProtectionListingDoesNotHideMalformedStoredReference(t *testing.T) {
	for _, reference := range []string{"", "../bad", "bad\nreference"} {
		t.Run(fmt.Sprintf("%q", reference), func(t *testing.T) {
			store, workspace := newTestStore(t)
			writeTestFile(t, workspace, "review.txt", []byte("before"), 0o644)
			retainedSnapshot(t, store, "review-1")
			protectionExec(t, store, `UPDATE history_retained_snapshots SET reference=?`, reference)
			if _, err := store.ListSafetyProtections(context.Background(), "", 1); err == nil {
				t.Fatal("listing silently hid corrupt retained reference")
			}
		})
	}
}
