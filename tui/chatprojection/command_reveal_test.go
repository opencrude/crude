package chatprojection

import (
	"fmt"
	"strings"
	"testing"

	"crude/tui/ui"
	"crude/types/conversation"
)

func commandRevealFixture(n int) ([]conversation.ChatMessage, string) {
	var messages []conversation.ChatMessage
	for i := range n {
		start, _ := commandIdentityFixture()
		start.InternalID = fmt.Sprintf("command-%d", i)
		start.ToolOutcome.ID = fmt.Sprintf("process:%d:1000000000", i+1)
		start.Content = strings.ReplaceAll(start.Content, "build", fmt.Sprintf("build-%d", i))
		messages = append(messages, conversation.ChatMessage{InternalID: fmt.Sprintf("note-%d", i), Role: conversation.RoleAssistant, Content: "Progress before this command."}, start)
	}
	return messages, fmt.Sprintf("process:%d:1000000000", n/2+1)
}

func TestRevealCommandExpandsAndAnchorsBeforeFirstPaint(t *testing.T) {
	messages, execution := commandRevealFixture(100)
	r := newActivityRenderer()
	scroll := ui.NewScrollModel()
	scroll.SetViewport(15)
	r.BindScroll(scroll)
	r.SyncMessages(messages, 1, 0)
	r.ExportMessageRows(100)
	scroll.SetContent(r.ContentHeight())
	scroll.StickToBottom()
	id, ok := r.RevealCommand(execution)
	if !ok || id != "command-50" {
		t.Fatalf("wrong command: %q %v", id, ok)
	}
	rows, _, _ := r.ExportMessageRows(100)
	line := 0
	for _, row := range rows {
		if row.ID == id {
			if line < scroll.Offset() || line >= scroll.Offset()+scroll.Viewport() {
				t.Fatalf("expanded command at %d is outside [%d,%d)", line, scroll.Offset(), scroll.Offset()+scroll.Viewport())
			}
			if !r.DisclosureExpanded(id) || !strings.Contains(strings.Join(row.Lines, "\n"), "build-50") {
				t.Fatal("reveal did not expand the original command")
			}
			break
		}
		line += max(1, len(row.Lines))
	}
	before := scroll.Offset()
	r.RevealCommand(execution)
	r.ExportMessageRows(100)
	if !r.DisclosureExpanded(id) || scroll.Offset() != before {
		t.Fatal("a repeated reveal collapsed or moved the command")
	}
	if _, ok := r.RevealCommand("process:9999:1000000000"); ok {
		t.Fatal("a missing process matched another command")
	}
}

func TestRevealCommandShowsHiddenLeafWithoutRebuildingOtherGroups(t *testing.T) {
	messages, execution := commandRevealFixture(30)
	// Remove communication boundaries to group the completed calls.
	var grouped []conversation.ChatMessage
	for _, msg := range messages {
		if msg.Role == conversation.RoleTool {
			msg.Content = strings.ReplaceAll(msg.Content, `"status":"running"`, `"status":"completed"`)
			grouped = append(grouped, msg)
		}
	}
	grouped = append(grouped, conversation.ChatMessage{InternalID: "next-turn", Role: conversation.RoleUser, Content: "A new turn"})
	r := newActivityRenderer()
	scroll := ui.NewScrollModel()
	scroll.SetViewport(15)
	r.BindScroll(scroll)
	r.SyncMessages(grouped, 1, 0)
	r.ExportMessageRows(100)
	parent := r.activityParents["command-15"]
	if parent == "" {
		t.Fatal("fixture did not group the command")
	}
	group := r.vl.Item(r.vl.byID[parent]).Data.(*activityGroup)
	if len(group.visible) != 0 {
		t.Fatal("old group did not hide its leaves")
	}
	canonical := &r.activityCanon[0]
	r.SetSize(60, 20) // an offscreen width reflow must not lose the jump
	r.RevealCommand(execution)
	rows, _, _ := r.ExportMessageRows(60)
	if &r.activityCanon[0] != canonical || r.vl.Item(r.vl.byID[parent]).Data != group {
		t.Fatal("a leaf reveal rebuilt transcript grouping")
	}
	if len(group.visible) != 1 || group.visible[0].InternalID != "command-15" {
		t.Fatal("a reveal exposed unrelated hidden calls")
	}
	found := false
	for _, row := range rows {
		for _, id := range row.LeafToggles {
			found = found || id == "command-15"
		}
	}
	if !found {
		t.Fatal("the expanded leaf was absent from the first export")
	}
}

func BenchmarkDenseCommandReveal(b *testing.B) {
	messages, execution := commandRevealFixture(2000)
	r := newActivityRenderer()
	r.MaterializeBudget = -1 // hydrate completely before measuring clicks
	r.SyncMessages(messages, 1, 0)
	r.ExportMessageRows(100)
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		id, _ := r.RevealCommand(execution)
		r.ExportMessageRows(100)
		r.ToggleDisclosure(id)
		r.ExportMessageRows(100)
	}
}
