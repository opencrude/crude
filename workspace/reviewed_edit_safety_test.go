package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/document"
	"crude/localhistory"
)

func safetyConsent(detail HistorySafetyDetail, action string) HistorySafetyChangeRequest {
	return HistorySafetyChangeRequest{
		Reference: detail.Reference, SnapshotID: detail.SnapshotID,
		OperationID: detail.Preview.ID, OperationDigest: detail.Preview.Digest,
		ExpectedRevision: detail.Revision, InspectionDigest: detail.InspectionDigest, Action: action,
	}
}

func safetyOriginal(t *testing.T, f reviewedFixture, id string) []byte {
	t.Helper()
	var raw []byte
	if err := f.documents.stateDB.QueryRow(`SELECT payload FROM workspace_reviewed_edits WHERE id=?`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	return raw
}

func committedSafety(t *testing.T) (reviewedFixture, ReviewedEditPreview, ReviewedEditResult) {
	t.Helper()
	f := newReviewedFixture(t)
	preview := f.prepare(t, func() error { return nil })
	result := f.documents.ApplyReviewedEdit(t.Context(), f.owner, preview.ID, preview.Digest, f.store, f.files)
	if result.State != "committed" || result.Error != "" || result.SafetySnapshotID == "" {
		t.Fatalf("real reviewed mutation did not commit with safety: %+v", result)
	}
	return f, preview, result
}

func TestHistorySafetyNativeDispositionPreservesOriginalWorkAndReceipts(t *testing.T) {
	f, preview, original := committedSafety(t)
	raw := safetyOriginal(t, f, preview.ID)
	before, err := f.documents.Snapshot(f.source.DocumentID)
	if err != nil {
		t.Fatal(err)
	}
	detail, err := f.documents.InspectHistorySafety(t.Context(), f.store, preview.ID)
	if err != nil || !detail.CanRelease || detail.CanReprotect || !detail.Retained || !detail.Available ||
		detail.Result != original || detail.OriginalOwner != f.owner || detail.OriginalState != "committed" ||
		detail.JournalSHA256 == "" || detail.InspectionDigest == "" {
		t.Fatalf("settled native detail lost original evidence: %+v err=%v", detail, err)
	}
	// A replacement human shell administers history without impersonating the
	// original editor owner or acquiring its Apply/Inspect permission.
	newShell := document.Owner{ClientID: "replacement-native-shell", Epoch: 4}
	released := f.documents.ChangeHistorySafetyProtection(t.Context(), f.store, newShell, "release-original", safetyConsent(detail, "release"))
	if released.State != "committed" || released.Error != "" || released.SnapshotDeleted ||
		released.Event.Action != "release" || released.Event.SnapshotID != original.SafetySnapshotID ||
		released.Event.Change.ActorClientID != newShell.ClientID || released.Event.Change.Evidence.OwnerClientID != f.owner.ClientID {
		t.Fatalf("exact native disposition not audited: %+v", released)
	}
	if _, err := f.store.LoadRetainedSnapshot(t.Context(), preview.ID); err == nil {
		t.Fatal("released reference is still pinned")
	}
	if _, err := f.store.LoadManifest(original.SafetySnapshotID); err != nil {
		t.Fatal("release deleted the snapshot instead of changing protection", err)
	}
	after, err := f.documents.Snapshot(f.source.DocumentID)
	if err != nil || !bytes.Equal(before.Text, after.Text) || before.Revision != after.Revision {
		t.Fatal("retention disposition edited canonical source", err)
	}
	disk, err := os.ReadFile(f.path)
	if err != nil || string(disk) != "var new = 1\r\nnew\r\n" {
		t.Fatal("retention disposition restored or edited project files", err)
	}
	if !bytes.Equal(safetyOriginal(t, f, preview.ID), raw) {
		t.Fatal("human disposition rewrote the original reviewed-edit receipt")
	}
	if _, err := f.documents.InspectReviewedEdit(t.Context(), newShell, preview.ID, preview.Digest); err == nil {
		t.Fatal("history inspection granted the foreign shell original edit authority")
	}
	again, err := f.documents.InspectHistorySafety(t.Context(), f.store, preview.ID)
	if err != nil || again.CanRelease || !again.CanReprotect || again.Retained ||
		again.LastEvent == nil || again.LastEvent.ID != released.Event.ID || again.Result != original {
		t.Fatalf("released audit not discoverable with original outcome: %+v err=%v", again, err)
	}
	protected := f.documents.ChangeHistorySafetyProtection(t.Context(), f.store, newShell, "protect-original", safetyConsent(again, "reprotect"))
	if protected.State != "committed" || protected.Event.Action != "reprotect" || protected.Event.Revision != 2 {
		t.Fatalf("exact available original could not be protected again: %+v", protected)
	}
	if _, err := f.store.LoadRetainedSnapshot(t.Context(), preview.ID); err != nil {
		t.Fatal("re-protection did not restore the exact original pin", err)
	}
	stale := f.documents.ChangeHistorySafetyProtection(t.Context(), f.store, newShell, "stale-release", safetyConsent(detail, "release"))
	if stale.State != "refused" || stale.Error == "" {
		t.Fatalf("peer/re-protection failed to retire stale consent: %+v", stale)
	}
	if !bytes.Equal(safetyOriginal(t, f, preview.ID), raw) {
		t.Fatal("re-protection rewrote the original operation")
	}
}

func TestHistorySafetyUnconfirmedNeverSettlesFromCurrentBytes(t *testing.T) {
	for _, present := range []string{"var new = 1\r\nnew\r\n", "var old = 1\r\nold\r\n", "external later work\n"} {
		t.Run(strings.TrimSpace(present), func(t *testing.T) {
			f, preview, _ := committedSafety(t)
			var journal reviewedEditJournal
			if err := json.Unmarshal(safetyOriginal(t, f, preview.ID), &journal); err != nil {
				t.Fatal(err)
			}
			journal.Result.State = "unconfirmed"
			journal.Result.Error = "historical commit completion was not confirmed"
			if err := f.documents.saveReviewedJournal(journal); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(f.path, []byte(present), 0600); err != nil {
				t.Fatal(err)
			}
			detail, err := f.documents.InspectHistorySafety(t.Context(), f.store, preview.ID)
			if err != nil || detail.CanRelease || detail.CanReprotect || !detail.Retained ||
				detail.OriginalState != "unconfirmed" || !strings.Contains(detail.BlockedReason, "historical outcome") {
				t.Fatalf("present bytes invented historical settlement: %+v err=%v", detail, err)
			}
			result := f.documents.ChangeHistorySafetyProtection(t.Context(), f.store, f.owner, "forged-release", safetyConsent(detail, "release"))
			if result.State != "refused" || result.Error == "" {
				t.Fatalf("unconfirmed historical effect lost protection: %+v", result)
			}
			if _, err := f.store.LoadRetainedSnapshot(t.Context(), preview.ID); err != nil {
				t.Fatal("ambiguous work was unpinned", err)
			}
		})
	}
}

func TestHistorySafetyMissingCorruptUnsealedAndUnknownRemainProtected(t *testing.T) {
	for _, bad := range []string{"prepared", "unsealed", "foreign-reference", "digest", "unknown-member", "duplicate-member", "missing", "unknown-state"} {
		t.Run(bad, func(t *testing.T) {
			f, preview, _ := committedSafety(t)
			raw := safetyOriginal(t, f, preview.ID)
			var journal reviewedEditJournal
			if err := json.Unmarshal(raw, &journal); err != nil {
				t.Fatal(err)
			}
			switch bad {
			case "prepared":
				journal.Result.State = "prepared"
			case "unsealed":
				journal.Result.SafetySnapshotID = ""
			case "foreign-reference":
				journal.Result.SafetyReference = "review-another"
			case "digest":
				journal.Preview.Files[0].BeforeSHA256 = strings.Repeat("a", 64)
			case "unknown-state":
				journal.Result.State = "looks-good-now"
			}
			switch bad {
			case "unknown-member":
				raw = append(bytes.TrimSuffix(raw, []byte("}")), []byte(`,"ignored":true}`)...)
			case "duplicate-member":
				raw = append(bytes.TrimSuffix(raw, []byte("}")), []byte(`,"Version":1}`)...)
			case "missing":
				if _, err := f.documents.stateDB.Exec(`DELETE FROM workspace_reviewed_edits WHERE id=?`, preview.ID); err != nil {
					t.Fatal(err)
				}
			default:
				var err error
				raw, err = json.Marshal(journal)
				if err != nil {
					t.Fatal(err)
				}
			}
			if bad != "missing" {
				if _, err := f.documents.stateDB.Exec(`UPDATE workspace_reviewed_edits SET payload=? WHERE id=?`, raw, preview.ID); err != nil {
					t.Fatal(err)
				}
			}
			detail, err := f.documents.InspectHistorySafety(t.Context(), f.store, preview.ID)
			if err != nil || detail.CanRelease || detail.CanReprotect || !detail.Retained || detail.BlockedReason == "" {
				t.Fatalf("incomplete/corrupt evidence became release authority: %+v err=%v", detail, err)
			}
			if _, err := f.store.LoadRetainedSnapshot(t.Context(), preview.ID); err != nil {
				t.Fatal("bad journal caused protection loss", err)
			}
		})
	}
}

func TestHistorySafetyGenericReferencePagingAndWrongWorkspace(t *testing.T) {
	f, preview, _ := committedSafety(t)
	for _, ref := range []string{"unattributed-A", "unattributed-B", "unattributed-C"} {
		if _, err := f.store.SnapshotRetainedFiles(t.Context(), ref, "Unattributed safety", []localhistory.RetainedFile{
			{Path: "source.txt", Data: []byte("original unrelated operation"), Mode: 0600},
		}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := f.documents.ListHistorySafety(t.Context(), f.store, HistorySafetyListRequest{Limit: 2})
	if err != nil || len(page.Records) != 2 || page.NextCursor == "" || page.RetainedCount != 4 {
		t.Fatalf("bounded safety page omitted coverage: %+v err=%v", page, err)
	}
	next, err := f.documents.ListHistorySafety(t.Context(), f.store, HistorySafetyListRequest{Limit: 2, Cursor: page.NextCursor})
	if err != nil || len(next.Records) != 2 || next.NextCursor != "" || next.RetainedCount != 4 {
		t.Fatalf("bounded safety continuation omitted references: %+v err=%v", next, err)
	}
	seen := map[string]bool{}
	for _, row := range append(page.Records, next.Records...) {
		if seen[row.Reference] {
			t.Fatal("safety page repeated a reference")
		}
		seen[row.Reference] = true
	}
	unknown, err := f.documents.InspectHistorySafety(t.Context(), f.store, "unattributed-B")
	if err != nil || unknown.CanRelease || unknown.CanReprotect || unknown.BlockedReason == "" || !unknown.Retained {
		t.Fatalf("reference spelling fabricated reviewed-edit provenance: %+v err=%v", unknown, err)
	}
	other, err := localhistory.Open(filepath.Join(t.TempDir(), "history"), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	if _, err := f.documents.InspectHistorySafety(t.Context(), other, preview.ID); err == nil {
		t.Fatal("foreign history Store was adopted by this workspace's document authority")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.documents.InspectHistorySafety(ctx, f.store, preview.ID); err == nil {
		t.Fatal("cancelled inspection returned current evidence")
	}
}

func TestHistorySafetyRetiredNativeAttachmentCannotRelease(t *testing.T) {
	f, preview, original := committedSafety(t)
	detail, err := f.documents.InspectHistorySafety(t.Context(), f.store, preview.ID)
	if err != nil || !detail.CanRelease {
		t.Fatal(detail, err)
	}
	f.documents.Detach(f.owner)
	refused := f.documents.ChangeHistorySafetyProtection(t.Context(), f.store, f.owner, "detached-native", safetyConsent(detail, "release"))
	if refused.State != "refused" || refused.Error == "" {
		t.Fatal("detached attachment retained human consent authority", refused)
	}
	// A new epoch can administer the old original evidence without opening any
	// editor, but that never revives the detached epoch.
	next := document.Owner{ClientID: f.owner.ClientID, Epoch: f.owner.Epoch + 1}
	released := f.documents.ChangeHistorySafetyProtection(t.Context(), f.store, next, "current-native", safetyConsent(detail, "release"))
	if released.State != "committed" {
		t.Fatal("current native attachment cannot administer historical evidence", released)
	}
	current, err := f.documents.InspectHistorySafety(t.Context(), f.store, preview.ID)
	if err != nil || !current.CanReprotect || current.Result != original {
		t.Fatal(current, err)
	}
	stale := f.documents.ChangeHistorySafetyProtection(t.Context(), f.store, f.owner, "retired-native", safetyConsent(current, "reprotect"))
	if stale.State != "refused" || stale.Error == "" {
		t.Fatal("old epoch adopted a newer human inspection", stale)
	}
}
