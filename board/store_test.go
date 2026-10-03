package board

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

const (
	testProject = "project-1"
	testBoard   = "collaboration-1"
)

func openTestDB(t *testing.T, path string) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(100)&_pragma=foreign_keys(ON)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func attachTestStore(t *testing.T, database *sql.DB) *Store {
	t.Helper()
	store, err := Attach(database, testProject)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func seedBoard(t *testing.T, store *Store) {
	t.Helper()
	ctx := context.Background()
	if err := store.Create(ctx, testBoard, "root"); err != nil {
		t.Fatal(err)
	}
	for _, member := range []Member{
		{ID: "root", Name: "Coordinator", Availability: AvailabilityActive},
		{ID: "peer", Name: "Researcher", Availability: AvailabilityActive},
		{ID: "finished", Name: "Completed worker", Availability: AvailabilityFinished},
	} {
		if err := store.Join(ctx, testBoard, member); err != nil {
			t.Fatal(err)
		}
	}
}

func newTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	database := openTestDB(t, filepath.Join(t.TempDir(), "workspace.db"))
	store := attachTestStore(t, database)
	seedBoard(t, store)
	return store, database
}

func testPost(identity string) PostRequest {
	return PostRequest{
		BoardID:           testBoard,
		ProjectID:         testProject,
		SenderID:          "root",
		ProviderMessageID: "provider-" + identity,
		ToolCallID:        "call-" + identity,
		To:                "peer",
		Type:              TypeInfo,
		Body:              "Finding " + identity,
	}
}

func readRequest(agentID string) ReadRequest {
	return ReadRequest{BoardID: testBoard, ProjectID: testProject, AgentID: agentID, Limit: MaxLimit}
}

func postOK(t *testing.T, store *Store, request PostRequest) PostResult {
	t.Helper()
	result, err := store.Post(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func readOK(t *testing.T, store *Store, request ReadRequest) Page {
	t.Helper()
	page, err := store.Read(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func TestStoreReopenPreservesMembershipEvidenceOffersAndCursors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.db")
	database := openTestDB(t, path)
	store := attachTestStore(t, database)
	seedBoard(t, store)
	ctx := context.Background()
	if err := store.Join(ctx, testBoard, Member{ID: "child", Name: "Child", ParentID: "peer", Availability: AvailabilityActive}); err != nil {
		t.Fatal(err)
	}
	request := testPost("persistent")
	request.References = []Reference{{Kind: "message", ID: "source-42"}, {Kind: "snapshot", ID: "sha256:abc"}}
	first := postOK(t, store, request)
	page := readOK(t, store, readRequest("peer"))
	if page.AcceptedThrough != 0 || page.AcceptThrough != first.Message.Sequence {
		t.Fatalf("read advanced or failed to offer cursor: %+v", page)
	}
	if err := store.AcceptRead(ctx, testBoard, "peer", page.AcceptThrough); err != nil {
		t.Fatal(err)
	}
	second := postOK(t, store, testPost("unaccepted"))
	page = readOK(t, store, readRequest("peer"))
	if page.AcceptedThrough != first.Message.Sequence || page.AcceptThrough != second.Message.Sequence {
		t.Fatalf("unexpected page: %+v", page)
	}
	if notice, err := store.PendingNotice(ctx, testBoard, "peer"); err != nil || notice != PendingNoticeText {
		t.Fatalf("notice = %q, %v", notice, err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database = openTestDB(t, path)
	store = attachTestStore(t, database)
	members, err := store.Roster(ctx, testBoard)
	if err != nil || len(members) != 4 || members[0].ParentID != "peer" {
		t.Fatalf("reopened members = %+v, %v", members, err)
	}
	duplicate := postOK(t, store, request)
	if !duplicate.Duplicate || !reflect.DeepEqual(duplicate.Message, first.Message) {
		t.Fatalf("reopened retry changed stable evidence: %+v, %+v", duplicate, first)
	}
	page = readOK(t, store, readRequest("peer"))
	if page.AcceptedThrough != 1 || page.HumanReadThrough != 0 || !reflect.DeepEqual(page.Messages[0].References, request.References) {
		t.Fatalf("reopened cursor/evidence = %+v", page)
	}
	if notice, err := store.PendingNotice(ctx, testBoard, "peer"); err != nil || notice == "" {
		t.Fatalf("reopen lost pending notice: %q, %v", notice, err)
	}
	if err := store.AcceptRead(ctx, testBoard, "peer", second.Message.Sequence); err != nil {
		t.Fatalf("durable offered page did not survive reopen: %v", err)
	}
	if notice, err := store.PendingNotice(ctx, testBoard, "peer"); err != nil || notice != "" {
		t.Fatalf("accepted notice = %q, %v", notice, err)
	}
	if unread, err := store.HumanUnread(ctx, testBoard); err != nil || unread != 2 {
		t.Fatalf("agent acceptance changed human unread: %d, %v", unread, err)
	}
}

func TestStoreConcurrentRetryDeduplicatesAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.db")
	firstDB := openTestDB(t, path)
	secondDB := openTestDB(t, path)
	stores := []*Store{attachTestStore(t, firstDB), attachTestStore(t, secondDB)}
	seedBoard(t, stores[0])
	const workers = 32
	results := make(chan PostResult, workers)
	failures := make(chan error, workers)
	start := make(chan struct{})
	var group sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for worker := 0; worker < workers; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			<-start
			result, err := stores[worker%len(stores)].Post(ctx, testPost("same-retry"))
			if err != nil {
				failures <- err
				return
			}
			results <- result
		}(worker)
	}
	close(start)
	group.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Errorf("concurrent retry: %v", err)
	}
	var expectedID string
	originals := 0
	for result := range results {
		if expectedID == "" {
			expectedID = result.Message.ID
		}
		if result.Message.ID != expectedID || result.Message.Sequence != 1 {
			t.Errorf("retry allocated new evidence: %+v", result)
		}
		if !result.Duplicate {
			originals++
		}
	}
	if originals != 1 {
		t.Fatalf("committed originals = %d", originals)
	}
	page := readOK(t, stores[0], readRequest("peer"))
	if len(page.Messages) != 1 || page.LatestSequence != 1 {
		t.Fatalf("duplicate persisted: %+v", page)
	}
}

func TestStoreChangedRetryRejectedWithoutSequenceAdvance(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	request := testPost("original")
	request.References = []Reference{{Kind: "message", ID: "original-source"}}
	original := postOK(t, store, request)
	if err := store.Create(ctx, "other-board", "root"); err != nil {
		t.Fatal(err)
	}
	if err := store.Join(ctx, "other-board", Member{ID: "root"}); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		name   string
		mutate func(*PostRequest)
	}{
		{"body", func(request *PostRequest) { request.Body = "corrected body" }},
		{"recipient", func(request *PostRequest) { request.To = "finished" }},
		{"type", func(request *PostRequest) { request.Type = TypeVeto }},
		{"reply", func(request *PostRequest) { request.ReplyTo = original.Message.ID }},
		{"reference", func(request *PostRequest) {
			request.References = []Reference{{Kind: "message", ID: "different-source"}}
		}},
		{"board", func(request *PostRequest) { request.BoardID = "other-board" }},
	} {
		t.Run(change.name, func(t *testing.T) {
			changed := request
			change.mutate(&changed)
			if _, err := store.Post(ctx, changed); !errors.Is(err, ErrChangedRetry) {
				t.Fatalf("changed retry = %v", err)
			}
		})
	}
	if err := store.Join(ctx, testBoard, Member{ID: "peer", Name: "Now finished", Availability: AvailabilityFinished}); err != nil {
		t.Fatal(err)
	}
	duplicate := postOK(t, store, request)
	if !duplicate.Duplicate || duplicate.Message.RecipientAvailability != AvailabilityActive {
		t.Fatalf("retry did not retain original availability: %+v", duplicate)
	}
	next := postOK(t, store, testPost("next"))
	if next.Message.Sequence != 2 {
		t.Fatalf("failed retry consumed sequence: %+v", next)
	}
}

func TestStoreValidatesMembershipProjectAndExplicitInheritance(t *testing.T) {
	store, database := newTestStore(t)
	ctx := context.Background()
	if _, err := Attach(nil, testProject); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil db = %v", err)
	}
	if _, err := Attach(database, "different-project"); !errors.Is(err, ErrProjectMismatch) {
		t.Fatalf("cross-project attach = %v", err)
	}
	if err := store.Create(ctx, testBoard, "different-coordinator"); !errors.Is(err, ErrConflict) {
		t.Fatalf("coordinator replacement = %v", err)
	}
	request := testPost("unknown-sender")
	request.SenderID = "unrelated"
	if _, err := store.Post(ctx, request); !errors.Is(err, ErrNotMember) {
		t.Fatalf("unknown sender = %v", err)
	}
	request = testPost("unknown-recipient")
	request.To = "unrelated"
	if _, err := store.Post(ctx, request); !errors.Is(err, ErrNotMember) {
		t.Fatalf("unknown recipient = %v", err)
	}
	request = testPost("wrong-project")
	request.ProjectID = "different-project"
	if _, err := store.Post(ctx, request); !errors.Is(err, ErrProjectMismatch) {
		t.Fatalf("cross-project post = %v", err)
	}
	reading := readRequest("unrelated")
	if _, err := store.Read(ctx, reading); !errors.Is(err, ErrNotMember) {
		t.Fatalf("unknown reader = %v", err)
	}
	if _, err := store.Search(ctx, testBoard, testProject, "unrelated", "Finding", 10); !errors.Is(err, ErrNotMember) {
		t.Fatalf("unknown searcher = %v", err)
	}
	if _, err := store.PendingNotice(ctx, testBoard, "unrelated"); !errors.Is(err, ErrNotMember) {
		t.Fatalf("unknown notice recipient = %v", err)
	}
	if err := store.AcceptRead(ctx, testBoard, "unrelated", 0); !errors.Is(err, ErrNotMember) {
		t.Fatalf("unknown cursor owner = %v", err)
	}
	reading = readRequest("peer")
	reading.ProjectID = "different-project"
	if _, err := store.Read(ctx, reading); !errors.Is(err, ErrProjectMismatch) {
		t.Fatalf("cross-project read = %v", err)
	}
	if _, err := store.Search(ctx, testBoard, "different-project", "peer", "Finding", 10); !errors.Is(err, ErrProjectMismatch) {
		t.Fatalf("cross-project search = %v", err)
	}
	if err := store.Archive(ctx, testBoard, "different-project", "absent"); !errors.Is(err, ErrProjectMismatch) {
		t.Fatalf("cross-project archive = %v", err)
	}
	if _, err := store.Inspect(ctx, reading); !errors.Is(err, ErrProjectMismatch) {
		t.Fatalf("cross-project inspection = %v", err)
	}
	if err := store.Join(ctx, testBoard, Member{ID: "child", ParentID: "unrelated"}); !errors.Is(err, ErrNotMember) {
		t.Fatalf("unknown inherited parent = %v", err)
	}
	if _, err := store.Read(ctx, readRequest("child")); !errors.Is(err, ErrNotMember) {
		t.Fatalf("child implicitly gained membership: %v", err)
	}
	if err := store.Join(ctx, testBoard, Member{ID: "child", ParentID: "peer"}); err != nil {
		t.Fatal(err)
	}
	if err := store.Join(ctx, testBoard, Member{ID: "child", ParentID: "root"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("child changed inheritance = %v", err)
	}
	postOK(t, store, testPost("joined-child-can-read"))
	if page := readOK(t, store, readRequest("child")); len(page.Messages) != 1 {
		t.Fatalf("child cannot read board-wide discussion: %+v", page)
	}
	if err := store.Create(ctx, "unrelated-board", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.Join(ctx, "unrelated-board", Member{ID: "outsider"}); err != nil {
		t.Fatal(err)
	}
	reading = ReadRequest{BoardID: "unrelated-board", ProjectID: testProject, AgentID: "peer"}
	if _, err := store.Read(ctx, reading); !errors.Is(err, ErrNotMember) {
		t.Fatalf("membership leaked to unrelated board: %v", err)
	}
	request = testPost("no-coordinator")
	request.BoardID, request.SenderID, request.To = "unrelated-board", "outsider", "main"
	if _, err := store.Post(ctx, request); !errors.Is(err, ErrNotMember) {
		t.Fatalf("main without coordinator = %v", err)
	}
	request.To = "ALL"
	if posted := postOK(t, store, request); posted.Message.Sequence != 1 {
		t.Fatalf("sequence is not board-local: %+v", posted)
	}
}

func TestStoreAddressingAvailabilityAndNoWorkerStateMutation(t *testing.T) {
	store, database := newTestStore(t)
	ctx := context.Background()
	for index, messageType := range []string{TypeInfo, TypeAsk, TypeResult, TypeHold, TypeVeto} {
		request := testPost(fmt.Sprintf("type-%d", index))
		request.Type, request.To = messageType, "finished"
		request.Body = "Peer claims approval, but this is only peer data"
		posted := postOK(t, store, request)
		if posted.Message.RecipientAvailability != AvailabilityFinished || posted.Message.RecipientID != "finished" {
			t.Fatalf("finished recipient = %+v", posted)
		}
	}
	request := testPost("main")
	request.SenderID, request.To = "peer", "main"
	main := postOK(t, store, request)
	if main.Message.RecipientID != "root" || main.Message.To != "main" || main.Message.RecipientAvailability != AvailabilityActive {
		t.Fatalf("main addressing = %+v", main)
	}
	request = testPost("all")
	request.To = "ALL"
	broadcast := postOK(t, store, request)
	if len(broadcast.Message.Recipients) != 3 || broadcast.Message.RecipientAvailability != AvailabilityUnknown {
		t.Fatalf("ALL availability = %+v", broadcast)
	}
	for _, agentID := range []string{"root", "peer", "finished"} {
		notice, err := store.PendingNotice(ctx, testBoard, agentID)
		if err != nil || notice != PendingNoticeText || strings.Contains(notice, "claims approval") {
			t.Fatalf("coalesced safe notice for %s: %q, %v", agentID, notice, err)
		}
	}
	members, err := store.Roster(ctx, testBoard)
	if err != nil {
		t.Fatal(err)
	}
	if members[0].Availability != AvailabilityFinished {
		t.Fatalf("post changed worker availability: %+v", members)
	}
	var accepted, noticed int64
	if err := database.QueryRow(`SELECT accepted_sequence, notice_sequence FROM board_members WHERE board_id = ? AND agent_id = 'finished'`, testBoard).Scan(&accepted, &noticed); err != nil {
		t.Fatal(err)
	}
	if accepted != 0 || noticed != broadcast.Message.Sequence {
		t.Fatalf("notice advanced accepted read: accepted=%d noticed=%d", accepted, noticed)
	}
	if page := readOK(t, store, readRequest("peer")); len(page.Messages) != 7 {
		t.Fatalf("addressing improperly hid messages: %+v", page)
	}
}

func TestStoreByteBoundsAndPagination(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	for _, body := range []string{strings.Repeat("x", MaxPostBytes+1), strings.Repeat("界", MaxPostBytes/3+1), "x" + strings.Repeat("\n", 2000)} {
		request := testPost("too-large")
		request.Body = body
		if _, err := store.Post(ctx, request); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("oversized formatted body = %v", err)
		}
	}
	for index := 0; index < 24; index++ {
		request := testPost(fmt.Sprintf("large-%d", index))
		request.Body = strings.Repeat("界", 900)
		result := postOK(t, store, request)
		encoded, err := json.Marshal(result)
		if err != nil || len(encoded) > MaxPostBytes {
			t.Fatalf("post result bytes = %d, %v", len(encoded), err)
		}
	}
	reading := readRequest("peer")
	seen := 0
	pages := 0
	for {
		page := readOK(t, store, reading)
		encoded, err := json.Marshal(page)
		if err != nil || len(encoded) > MaxPageBytes || len(encoded) != page.Bytes {
			t.Fatalf("page bytes = %d, reported=%d, %v", len(encoded), page.Bytes, err)
		}
		if len(page.Messages) == 0 || page.Cursor != int64(seen+len(page.Messages)) || page.AcceptThrough != page.Cursor {
			t.Fatalf("page dropped evidence: %+v", page)
		}
		for index, message := range page.Messages {
			if message.Sequence != int64(seen+index+1) || message.Body != strings.Repeat("界", 900) {
				t.Fatalf("bounded page truncated/reordered message: %+v", message)
			}
		}
		seen += len(page.Messages)
		pages++
		if err := store.AcceptRead(ctx, testBoard, "peer", page.AcceptThrough); err != nil {
			t.Fatal(err)
		}
		if !page.HasMore {
			break
		}
		reading.Since = page.Cursor
	}
	if seen != 24 || pages < 3 {
		t.Fatalf("bounded pagination = %d messages in %d pages", seen, pages)
	}
	request := testPost("after-bounds")
	if result := postOK(t, store, request); result.Message.Sequence != 25 {
		t.Fatalf("rejected size consumed sequence: %+v", result)
	}
}

func TestStoreRejectsUnofferedFilteredOrSkippedCursors(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	first := postOK(t, store, testPost("first"))
	second := postOK(t, store, testPost("second"))
	if err := store.AcceptRead(ctx, testBoard, "peer", first.Message.Sequence); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cursor accepted without read: %v", err)
	}
	if _, err := store.Search(ctx, testBoard, testProject, "peer", "second", 10); err != nil {
		t.Fatal(err)
	}
	reading := readRequest("peer")
	reading.Thread = second.Message.ID
	if page := readOK(t, store, reading); page.AcceptThrough != 0 || len(page.Messages) != 1 {
		t.Fatalf("filtered read offered global cursor: %+v", page)
	}
	reading.Thread, reading.Since = "", first.Message.Sequence
	if page := readOK(t, store, reading); page.AcceptThrough != 0 {
		t.Fatalf("skipped read offered global cursor: %+v", page)
	}
	if err := store.AcceptRead(ctx, testBoard, "peer", second.Message.Sequence); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("filtered/skipped cursor accepted: %v", err)
	}
	reading.Since, reading.Limit = 0, 1
	page := readOK(t, store, reading)
	if !page.HasMore || page.Cursor != first.Message.Sequence || page.AcceptedThrough != 0 {
		t.Fatalf("partial page = %+v", page)
	}
	if err := store.AcceptRead(ctx, testBoard, "peer", second.Message.Sequence); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("accepted beyond offered page: %v", err)
	}
	if notice, err := store.PendingNotice(ctx, testBoard, "peer"); err != nil || notice == "" {
		t.Fatalf("unaccepted page consumed notice: %q, %v", notice, err)
	}
	if err := store.AcceptRead(ctx, testBoard, "peer", page.AcceptThrough); err != nil {
		t.Fatal(err)
	}
	third := postOK(t, store, testPost("concurrent-newer"))
	reading.Since = page.Cursor
	page = readOK(t, store, reading)
	if page.LatestSequence != third.Message.Sequence || page.AcceptThrough != second.Message.Sequence {
		t.Fatalf("partial page followed concurrent sequence: %+v", page)
	}
	if err := store.AcceptRead(ctx, testBoard, "peer", third.Message.Sequence); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("concurrent post accepted without offered body: %v", err)
	}
	if err := store.AcceptRead(ctx, testBoard, "peer", page.AcceptThrough); err != nil {
		t.Fatal(err)
	}
	if notice, err := store.PendingNotice(ctx, testBoard, "peer"); err != nil || notice == "" {
		t.Fatalf("partial acceptance lost later notice: %q, %v", notice, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	reading.Since = page.Cursor
	if _, err := store.Read(cancelled, reading); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read = %v", err)
	}
	if err := store.AcceptRead(ctx, testBoard, "peer", third.Message.Sequence); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("cancelled read offered cursor: %v", err)
	}
}

func TestStoreHumanUnreadNeverAdvancesAgentCursor(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	postOK(t, store, testPost("one"))
	postOK(t, store, testPost("two"))
	page, err := store.Inspect(ctx, readRequest(""))
	if err != nil || page.AcceptThrough != 2 {
		t.Fatalf("human inspection = %+v, %v", page, err)
	}
	if unread, err := store.HumanUnread(ctx, testBoard); err != nil || unread != 2 {
		t.Fatalf("inspection implicitly accepted human cursor: %d, %v", unread, err)
	}
	if err := store.AcceptRead(ctx, testBoard, "peer", page.Cursor); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("human view offered agent cursor: %v", err)
	}
	if err := store.AcceptHumanRead(ctx, testBoard, page.AcceptThrough); err != nil {
		t.Fatal(err)
	}
	if unread, err := store.HumanUnread(ctx, testBoard); err != nil || unread != 0 {
		t.Fatalf("human acceptance = %d, %v", unread, err)
	}
	if notice, err := store.PendingNotice(ctx, testBoard, "peer"); err != nil || notice == "" {
		t.Fatalf("human view delivered to agent: %q, %v", notice, err)
	}
	page = readOK(t, store, readRequest("peer"))
	if page.AcceptedThrough != 0 || page.HumanReadThrough != 2 {
		t.Fatalf("cursor independence = %+v", page)
	}
	if err := store.AcceptRead(ctx, testBoard, "peer", page.AcceptThrough); err != nil {
		t.Fatal(err)
	}
	postOK(t, store, testPost("three"))
	if unread, err := store.HumanUnread(ctx, testBoard); err != nil || unread != 1 {
		t.Fatalf("new human unread = %d, %v", unread, err)
	}
	if _, err := store.Read(ctx, readRequest("")); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty agent bypassed membership: %v", err)
	}
}

func TestStoreArchivesWholeThreadsWithoutLosingEvidenceOrNotices(t *testing.T) {
	store, database := newTestStore(t)
	ctx := context.Background()
	request := testPost("archived-evidence")
	request.Body = "Needle original evidence 100%_literal"
	request.References = []Reference{{Kind: "receipt", ID: "receipt-1"}}
	original := postOK(t, store, request)
	reply := testPost("reply")
	reply.Body, reply.ReplyTo = "Needle correction", original.Message.ID
	correction := postOK(t, store, reply)
	if correction.Message.ThreadID != original.Message.ID {
		t.Fatalf("reply detached from origin: %+v", correction)
	}
	if err := store.Archive(ctx, testBoard, testProject, correction.Message.ID); err != nil {
		t.Fatal(err)
	}
	page, err := store.Search(ctx, testBoard, testProject, "peer", "needle", 1)
	if err != nil || len(page.Messages) != 1 || !page.Messages[0].Archived || !page.HasMore || page.AcceptThrough != 0 {
		t.Fatalf("archived search page = %+v, %v", page, err)
	}
	if page.Messages[0].ID != original.Message.ID || !reflect.DeepEqual(page.Messages[0].References, request.References) {
		t.Fatalf("archival changed evidence: %+v", page.Messages[0])
	}
	reading := readRequest("peer")
	reading.Since = page.Cursor
	page, err = store.SearchPage(ctx, reading, "needle")
	if err != nil || len(page.Messages) != 1 || page.Messages[0].ID != correction.Message.ID || !page.Messages[0].Archived {
		t.Fatalf("archived paginated search = %+v, %v", page, err)
	}
	if err := store.AcceptRead(ctx, testBoard, "peer", correction.Message.Sequence); !errors.Is(err, ErrInvalidCursor) {
		t.Fatalf("search advanced accepted cursor: %v", err)
	}
	page, err = store.Search(ctx, testBoard, testProject, "peer", "%_literal", 10)
	if err != nil || len(page.Messages) != 1 {
		t.Fatalf("search did not treat wildcard characters literally: %+v, %v", page, err)
	}
	if notice, err := store.PendingNotice(ctx, testBoard, "peer"); err != nil || notice == "" {
		t.Fatalf("archive dropped unread notice: %q, %v", notice, err)
	}
	duplicate := postOK(t, store, request)
	if !duplicate.Duplicate || !duplicate.Message.Archived || duplicate.Message.ID != original.Message.ID {
		t.Fatalf("archived retry = %+v", duplicate)
	}
	reply = testPost("archived-reply")
	reply.Body, reply.ReplyTo = "Needle later resolution", correction.Message.ID
	if posted := postOK(t, store, reply); !posted.Message.Archived || posted.Message.ThreadID != original.Message.ID {
		t.Fatalf("reply resurrected archived thread: %+v", posted)
	}
	for index := 0; index <= MaxActiveMessages; index++ {
		request := testPost(fmt.Sprintf("bounded-history-%d", index))
		if index == 0 {
			request.Body = "Oldest bounded history evidence"
		}
		postOK(t, store, request)
	}
	var active, total int
	if err := database.QueryRow(`SELECT SUM(CASE WHEN archived = 0 THEN 1 ELSE 0 END), COUNT(*) FROM board_messages WHERE board_id = ?`, testBoard).Scan(&active, &total); err != nil {
		t.Fatal(err)
	}
	if active > MaxActiveMessages || total != MaxActiveMessages+4 {
		t.Fatalf("history cap discarded evidence or did not archive: active=%d total=%d", active, total)
	}
	page, err = store.Search(ctx, testBoard, testProject, "peer", "Oldest bounded history evidence", 10)
	if err != nil || len(page.Messages) != 1 || !page.Messages[0].Archived {
		t.Fatalf("automatic archive not searchable: %+v, %v", page, err)
	}
}

func TestStoreConcurrentPagesPostsAndAcceptancePreserveEverySequence(t *testing.T) {
	store, database := newTestStore(t)
	secondStore := attachTestStore(t, database)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	postOK(t, store, testPost("initial"))
	const additional = 40
	failures := make(chan error, additional)
	start := make(chan struct{})
	var group sync.WaitGroup
	for worker := 0; worker < additional; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			<-start
			if _, err := secondStore.Post(ctx, testPost(fmt.Sprintf("race-%d", worker))); err != nil {
				failures <- err
			}
		}(worker)
	}
	close(start)
	reading := readRequest("peer")
	reading.Limit = 3
	seen := make(map[string]bool)
	var through int64
	for through < additional+1 {
		page, err := store.Read(ctx, reading)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Messages) == 0 {
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(time.Millisecond):
			}
			continue
		}
		for _, message := range page.Messages {
			if seen[message.ID] || message.Sequence != through+1 {
				t.Fatalf("concurrent page lost/replayed evidence: through=%d message=%+v", through, message)
			}
			seen[message.ID] = true
			through = message.Sequence
		}
		if page.AcceptThrough != through || page.Cursor != through || page.LatestSequence < through {
			t.Fatalf("concurrent page cursor = %+v", page)
		}
		if err := store.AcceptRead(ctx, testBoard, "peer", through+1); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("page race allowed unoffered next sequence: %v", err)
		}
		if err := store.AcceptRead(ctx, testBoard, "peer", through); err != nil {
			t.Fatal(err)
		}
		reading.Since = page.Cursor
	}
	group.Wait()
	close(failures)
	for err := range failures {
		t.Errorf("concurrent post: %v", err)
	}
	if len(seen) != additional+1 {
		t.Fatalf("read %d distinct messages", len(seen))
	}
	if notice, err := store.PendingNotice(ctx, testBoard, "peer"); err != nil || notice != "" {
		t.Fatalf("fully accepted race retains notice: %q, %v", notice, err)
	}
}

func TestStoreRejectsInvalidArgumentsAndKeepsRosterBounded(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	for _, change := range []struct {
		name   string
		mutate func(*PostRequest)
	}{
		{"missing provider identity", func(request *PostRequest) { request.ProviderMessageID = "" }},
		{"missing tool identity", func(request *PostRequest) { request.ToolCallID = "" }},
		{"invalid type", func(request *PostRequest) { request.Type = "WAKE" }},
		{"empty body", func(request *PostRequest) { request.Body = " \n" }},
		{"invalid UTF-8", func(request *PostRequest) { request.Body = string([]byte{255}) }},
		{"reference content", func(request *PostRequest) { request.References = []Reference{{Kind: "text body", ID: "identity"}} }},
		{"reference missing id", func(request *PostRequest) { request.References = []Reference{{Kind: "message"}} }},
	} {
		t.Run(change.name, func(t *testing.T) {
			request := testPost("invalid")
			change.mutate(&request)
			if _, err := store.Post(ctx, request); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid arguments = %v", err)
			}
		})
	}
	for _, member := range []Member{{ID: "main"}, {ID: "ALL"}, {ID: "self", ParentID: "self"}, {ID: "bad-state", Availability: "stopped"}} {
		if err := store.Join(ctx, testBoard, member); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid member %+v = %v", member, err)
		}
	}
	if err := store.Create(ctx, "capacity", ""); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < MaxMembers; index++ {
		if err := store.Join(ctx, "capacity", Member{ID: fmt.Sprintf("m-%02d", index)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Join(ctx, "capacity", Member{ID: "overflow"}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("unbounded roster = %v", err)
	}
	if members, err := store.Roster(ctx, "capacity"); err != nil || len(members) != MaxMembers {
		t.Fatalf("failed join was not rolled back: %d, %v", len(members), err)
	}
}
