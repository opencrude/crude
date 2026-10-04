package chatprojection

import (
	"crude/tui/cell"
	"crude/tui/types"
	"fmt"
	"strings"
	"testing"
)

func TestActivitySingletonRoundsKeepUsefulRecentContext(t *testing.T) {
	r := newActivityRenderer()
	var messages []types.ChatMessage
	for i := range 9 {
		messages = append(messages, types.ChatMessage{InternalID: fmt.Sprint(i), ToolRoundID: fmt.Sprint(i), Role: types.RoleTool, ToolName: "read_file", ToolArgs: fmt.Sprintf("file-%d.go", i), ToolStatus: types.ToolStatusSuccess})
	}
	r.SyncMessages(messages, 1)
	rows, _, _ := r.ExportMessageRows(100)
	text := strings.Join(rows[0].Lines, "\n")
	for i := range 9 {
		if strings.Contains(text, fmt.Sprintf("file-%d.go", i)) != (i >= 3) {
			t.Fatalf("compact tree must retain six recent leaves, not just two singleton requests:\n%s", text)
		}
	}
	if !strings.Contains(text, "3 hidden") {
		t.Fatalf("hidden evidence has no count: %s", text)
	}
	messages = append(messages, types.ChatMessage{InternalID: "pending", Role: types.RoleUser, PendingSteering: true, Content: "also check tests"})
	r.SyncMessages(messages, 2)
	rows, _, _ = r.ExportMessageRows(100)
	if strings.Join(rows[0].Lines, "\n") != text {
		t.Fatal("pending note changed retained context before it was consumed")
	}
	r.ToggleDisclosure("0")
	rows, _, _ = r.ExportMessageRows(100)
	if !strings.Contains(strings.Join(rows[0].Lines, "\n"), "file-0.go") {
		t.Fatal("explicitly opened evidence was not pinned")
	}
}

func TestActivityRetentionNeverSplitsRequestRound(t *testing.T) {
	r := newActivityRenderer()
	var messages []types.ChatMessage
	for i := range 12 {
		round := "old"
		if i >= 3 && i < 10 {
			round = "batch"
		} else if i >= 10 {
			round = fmt.Sprint(i)
		}
		messages = append(messages, types.ChatMessage{InternalID: fmt.Sprint(i), ToolRoundID: round, Role: types.RoleTool, ToolName: "read_file", ToolArgs: fmt.Sprintf("file-%02d.go", i), ToolStatus: types.ToolStatusSuccess})
	}
	r.SyncMessages(messages, 1)
	rows, _, _ := r.ExportMessageRows(100)
	text := strings.Join(rows[0].Lines, "\n")
	for i := range 12 {
		if strings.Contains(text, fmt.Sprintf("file-%02d.go", i)) != (i >= 3) {
			t.Fatalf("leaf budget split a request round:\n%s", text)
		}
	}
}

func TestNewWorkDoesNotImmediatelyHideCompletedLeaves(t *testing.T) {
	r := newActivityRenderer()
	msgs := []types.ChatMessage{{InternalID: "one", Role: "tool", ToolName: "read_file", ToolArgs: "first.go", ToolStatus: "success"}, {InternalID: "two", Role: "tool", ToolName: "read_file", ToolArgs: "second.go", ToolStatus: "calling"}}
	r.SyncMessages(msgs, 1)
	rows, _, _ := r.ExportMessageRows(100)
	text := strings.Join(rows[0].Lines, "\n")
	if !strings.Contains(text, "first.go") || !strings.Contains(text, "second.go") {
		t.Fatalf("new leaf erased readable context: %s", text)
	}
}

func TestCancelledContentRemainsReadableWithoutStrikethrough(t *testing.T) {
	r := newActivityRenderer()
	r.SyncMessages([]types.ChatMessage{{InternalID: "a", Role: "assistant", Content: "Completed investigation and useful partial findings", Incomplete: true, IncompleteReason: "cancelled"}, {InternalID: "t", Role: "tool", ToolName: "read_file", ToolArgs: "source.go", ToolStatus: types.ToolStatusCancelled}}, 1)
	rows, _, _ := r.ExportMessageRows(100)
	for _, row := range rows {
		for _, line := range row.Native {
			for _, span := range line.Spans {
				if span.Attr&cell.AttrStrikethrough != 0 {
					t.Fatalf("stopped content crossed out: %s", line.PlainText())
				}
			}
		}
	}
}

func TestActivityRetainsTwoRequestRoundsUntilThirdArrives(t *testing.T) {
	r := newActivityRenderer()
	msgs := []types.ChatMessage{{InternalID: "one", ToolRoundID: "request-1", Role: "tool", ToolName: "read_file", ToolArgs: "first.go", ToolStatus: "success"}, {InternalID: "two", ToolRoundID: "request-2", Role: "tool", ToolName: "read_file", ToolArgs: "second.go", ToolStatus: "calling"}}
	for i := range 4 {
		msgs = append(msgs, types.ChatMessage{InternalID: fmt.Sprintf("sibling-%d", i), ToolRoundID: "request-2", Role: types.RoleTool, ToolName: "read_file", ToolStatus: types.ToolStatusSuccess})
	}
	r.SyncMessages(msgs, 1)
	rows, _, _ := r.ExportMessageRows(100)
	if !strings.Contains(strings.Join(rows[0].Lines, "\n"), "first.go") {
		t.Fatal("previous round folded before a third request")
	}
	msgs[1].ToolStatus = "success"
	msgs = append(msgs, types.ChatMessage{InternalID: "three", ToolRoundID: "request-3", Role: "tool", ToolName: "read_file", ToolArgs: "third.go", ToolStatus: "calling"})
	r.SyncMessages(msgs, 2)
	rows, _, _ = r.ExportMessageRows(100)
	text := strings.Join(rows[0].Lines, "\n")
	if strings.Contains(text, "first.go") || !strings.Contains(text, "second.go") || !strings.Contains(text, "third.go") {
		t.Fatalf("incorrect retained rounds: %s", text)
	}
	msgs = append(msgs, types.ChatMessage{InternalID: "four", ToolRoundID: "request-3", Role: "tool", ToolName: "read_file", ToolArgs: "fourth.go", ToolStatus: "calling"})
	r.SyncMessages(msgs, 3)
	rows, _, _ = r.ExportMessageRows(100)
	if !strings.Contains(strings.Join(rows[0].Lines, "\n"), "second.go") {
		t.Fatal("a sibling tool retired the previous request")
	}
	for _, frame := range []int{400, 800, 400000} {
		r.SetSpinFrame(frame)
		rows, _, _ = r.ExportMessageRows(100)
		if !strings.Contains(strings.Join(rows[0].Lines, "\n"), "second.go") {
			t.Fatal("tick changed retention")
		}
	}
}

func TestStoppedBoundaryIsTextNotCompactionTable(t *testing.T) {
	r := newActivityRenderer()
	r.SyncMessages([]types.ChatMessage{{InternalID: "stop", Role: types.RoleStatus, LifecyclePhase: "stopped", LifecycleOperationID: "stop"}}, 1)
	rows, _, _ := r.ExportMessageRows(100)
	if len(rows) != 1 || rows[0].ContextBoundary || !strings.Contains(strings.Join(rows[0].Lines, "\n"), "Stopped by you") {
		t.Fatalf("stop notice misclassified as compaction chrome: %+v", rows)
	}
}
