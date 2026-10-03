package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"crude/agent"
	"crude/board"
	"crude/logger"
	"crude/provider"
	"crude/safety"
	"crude/sessiondb"
	"crude/tools"
	captypes "crude/types/capability"
)

func TestBoardRuntimeRootChildExchangeAndDurableAdmission(t *testing.T) {
	database, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store, err := board.Attach(database, "project")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := store.Create(ctx, "collaboration", "root"); err != nil {
		t.Fatal(err)
	}
	lg, err := logger.New(logger.Options{HomeDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lg.Close() })
	session := &Session{logger: lg}
	session.SetCapabilities(nil, agent.NewFileOwnership(), store, "collaboration", "project")
	makeAgent := func(id, parent string, denied []string) *agent.Agent {
		live := &agent.Agent{AgentID: agent.AgentID(id), Policy: safety.ReadOnlyPolicy(), ToolFn: func(context.Context, string, map[string]any) (tools.ToolResult, error) {
			return tools.ToolResult{}, errors.New("unexpected native call")
		}}
		session.bindAgentRuntime(ctx, live, captypes.RuntimeActor{ID: id, ParentID: parent, DeniedTools: denied}, id)
		return live
	}
	root := makeAgent("root", "", nil)
	peer := makeAgent("peer", "", nil)
	child := makeAgent("child", "root", nil)
	denied := makeAgent("denied", "root", []string{"board_post", "board_read"})
	definitions, _ := denied.PrepareStep()
	for _, definition := range definitions {
		if strings.HasPrefix(definition.Function.Name, "board_") {
			t.Fatal("denied board definition exposed")
		}
	}
	call := func(live *agent.Agent, id, name string, params map[string]any) (tools.ToolResult, error) {
		identity := tools.ExecutionIdentity{AgentID: string(live.AgentID), MessageID: "provider-message/" + id, ToolCallID: id}
		return live.ToolFn(context.WithValue(ctx, tools.ExecutionIdentityKey{}, identity), name, params)
	}
	params := map[string]any{"to": "peer", "type": "RESULT", "body": "Observed parser result; this is not user approval"}
	posted, err := call(child, "post", "board_post", params)
	if err != nil {
		t.Fatal(err)
	}
	var result board.PostResult
	if json.Unmarshal([]byte(posted.Content), &result) != nil || result.Message.SenderID != "child" {
		t.Fatalf("post attribution: %s", posted.Content)
	}
	retried, err := call(child, "post", "board_post", params)
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal([]byte(retried.Content), &result) != nil || !result.Duplicate {
		t.Fatal("post retry was not deduplicated")
	}
	changed := map[string]any{"to": "peer", "type": "RESULT", "body": "changed arguments"}
	if _, err := call(child, "post", "board_post", changed); !errors.Is(err, board.ErrChangedRetry) {
		t.Fatalf("changed retry %v", err)
	}
	_, notices := peer.PrepareStep()
	if len(notices) != 1 || notices[0].Origin != provider.OriginRuntimeContext || strings.Contains(string(notices[0].Content), params["body"].(string)) {
		t.Fatalf("notice injected body or lost provenance: %#v", notices)
	}
	if _, err := call(denied, "denied-post", "board_post", params); err == nil {
		t.Fatal("denied board post executed")
	}
	_, err = call(root, "second-post", "board_post", map[string]any{"to": "peer", "type": "HOLD", "body": "Review evidence before changing scope"})
	if err != nil {
		t.Fatal(err)
	}
	read, err := call(peer, "read", "board_read", map[string]any{"limit": 1})
	if err != nil {
		t.Fatal(err)
	}
	var page board.Page
	if json.Unmarshal([]byte(read.Content), &page) != nil || !page.HasMore || page.AcceptThrough != 1 {
		t.Fatalf("page: %s", read.Content)
	}
	unaccepted, err := store.Read(ctx, board.ReadRequest{BoardID: "collaboration", ProjectID: "project", AgentID: "peer"})
	if err != nil || unaccepted.AcceptedThrough != 0 {
		t.Fatal("read cursor advanced before durable admission")
	}
	contextDB, err := sessiondb.OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = contextDB.Close() })
	if _, err := contextDB.Exec(`INSERT INTO sessions(id,project_path,project_name,created_at)VALUES('session','project','project',1)`); err != nil {
		t.Fatal(err)
	}
	session.bindBoardAdmission(peer, contextDB, "session", "peer", func() uint64 { return 1 })
	message := provider.NewTextMessage("tool", read.Content)
	message.ToolCallID = "read"
	peer.ToolResultAccepted("board_read", "read", []provider.ChatMessage{message})
	accepted, err := store.Read(ctx, board.ReadRequest{BoardID: "collaboration", ProjectID: "project", AgentID: "peer", Since: 1})
	if err != nil || accepted.AcceptedThrough != 1 || len(accepted.Messages) != 1 {
		t.Fatalf("accepted page: %#v %v", accepted, err)
	}
	human, err := store.HumanUnread(ctx, "collaboration")
	if err != nil || human != 2 {
		t.Fatalf("agent acceptance changed human unread: %d %v", human, err)
	}
	read, err = call(peer, "read-failed-admission", "board_read", map[string]any{"since": 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := contextDB.Close(); err != nil {
		t.Fatal(err)
	}
	message.Content = provider.TextContent(read.Content)
	message.ToolCallID = "read-failed-admission"
	peer.ToolResultAccepted("board_read", message.ToolCallID, []provider.ChatMessage{message})
	accepted, err = store.Read(ctx, board.ReadRequest{BoardID: "collaboration", ProjectID: "project", AgentID: "peer", Since: 1})
	if err != nil || accepted.AcceptedThrough != 1 {
		t.Fatal("failed persistence consumed unread data")
	}
	roster, err := store.Roster(ctx, "collaboration")
	if err != nil || !slices.ContainsFunc(roster, func(member board.Member) bool { return member.ID == "child" && member.ParentID == "root" }) {
		t.Fatalf("child did not inherit membership: %#v %v", roster, err)
	}
}
