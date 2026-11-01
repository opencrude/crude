package chatprojection

import (
	"fmt"
	"math/rand"
	"slices"
	"strings"
	"testing"
	"time"

	"crude/types"
)

// incrementalTranscript mutates a transcript the way live turns do: appended
// and replaced tool rows, streamed text, thinking, user boundaries, removals
// of transient status rows and parallel batches.
type incrementalTranscript struct {
	rng      *rand.Rand
	messages []types.ChatMessage
	next     int
	round    int
}

func (s *incrementalTranscript) id() string {
	s.next++
	return fmt.Sprintf("m%d", s.next)
}

func (s *incrementalTranscript) step() (changed []types.ChatMessage, removed []string, structural bool) {
	tools := []string{"read_file", "grep_files", "execute_command", "edit_file", "check_async_command", "web_fetch"}
	switch op := s.rng.Intn(10); {
	case op == 0:
		s.round++
		message := types.ChatMessage{InternalID: s.id(), Role: types.RoleUser, Content: "next request"}
		s.messages = append(s.messages, message)
		return []types.ChatMessage{message}, nil, true
	case op <= 3:
		if s.rng.Intn(3) == 0 {
			s.round++
		}
		status := types.ToolStatusSuccess
		if s.rng.Intn(4) == 0 {
			status = types.ToolStatusCalling
		}
		content := "ok"
		if s.rng.Intn(6) == 0 {
			content = "Error: failed"
		}
		message := types.ChatMessage{InternalID: s.id(), Role: types.RoleTool, ToolName: tools[s.rng.Intn(len(tools))],
			ToolStatus: status, Content: content, ToolRoundID: fmt.Sprint("round-", s.round), ToolArgs: "x"}
		s.messages = append(s.messages, message)
		return []types.ChatMessage{message}, nil, true
	case op == 4:
		message := types.ChatMessage{InternalID: s.id(), Role: types.RoleThinking, Content: "reasoning", ThinkingDuration: time.Second}
		s.messages = append(s.messages, message)
		return []types.ChatMessage{message}, nil, true
	case op == 5:
		message := types.ChatMessage{InternalID: s.id(), Role: types.RoleAssistant, Content: "partial"}
		s.messages = append(s.messages, message)
		return []types.ChatMessage{message}, nil, true
	case op == 6:
		message := types.ChatMessage{InternalID: s.id(), Role: types.RoleStatus, LifecyclePhase: "waiting_provider"}
		s.messages = append(s.messages, message)
		return []types.ChatMessage{message}, nil, true
	case op == 7 && len(s.messages) > 0:
		// Remove transient status rows wherever they are.
		kept := s.messages[:0]
		for _, message := range s.messages {
			if message.Role == types.RoleStatus {
				removed = append(removed, message.InternalID)
				continue
			}
			kept = append(kept, message)
		}
		s.messages = kept
		return nil, removed, len(removed) > 0
	case op == 8:
		message := types.ChatMessage{InternalID: s.id(), Role: types.RoleTool, ToolName: "multi_tool_use.parallel", ToolStatus: types.ToolStatusSuccess,
			ToolInput: `{"tool_uses":[{"recipient_name":"functions.read_file","parameters":{"path":"a"}},{"recipient_name":"functions.grep_files","parameters":{"pattern":"b"}}]}`,
			Content:   `{"results":[{"recipient_name":"functions.read_file","result":"a"},{"recipient_name":"functions.grep_files","result":"b"}]}`}
		s.messages = append(s.messages, message)
		return []types.ChatMessage{message}, nil, true
	default:
		if len(s.messages) == 0 {
			return nil, nil, false
		}
		// Settle or stream into a recent row, sometimes an older one.
		index := len(s.messages) - 1 - s.rng.Intn(min(len(s.messages), 8))
		if s.rng.Intn(10) == 0 {
			index = s.rng.Intn(len(s.messages))
		}
		message := &s.messages[index]
		switch message.Role {
		case types.RoleTool:
			message.ToolStatus = types.ToolStatusSuccess
			message.Content += " more"
		case types.RoleAssistant, types.RoleThinking:
			message.Content += " more"
		default:
			return nil, nil, false
		}
		return []types.ChatMessage{*message}, nil, false
	}
}

func activitySignature(r *Renderer) string {
	var b strings.Builder
	for _, item := range r.vl.Items() {
		b.WriteString(item.ID)
		if group, ok := item.Data.(*activityGroup); ok {
			b.WriteString("[")
			for _, member := range group.messages {
				b.WriteString(member.InternalID + ",")
			}
			b.WriteString("|")
			for _, member := range group.visible {
				b.WriteString(member.InternalID + ",")
			}
			b.WriteString("]" + group.label)
		} else if message, ok := item.Data.(*types.ChatMessage); ok {
			b.WriteString("{" + message.Content + "}")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Incremental projection must equal a fresh complete build after every live
// mutation, through both the complete export and the exact-delta path.
func TestIncrementalActivityItemsMatchCompleteRebuild(t *testing.T) {
	for seed := int64(1); seed <= 40; seed++ {
		state := &incrementalTranscript{rng: rand.New(rand.NewSource(seed))}
		full := newActivityRenderer()
		delta := newActivityRenderer()
		version := uint64(1)
		full.SyncMessages(nil, version, 1)
		delta.SyncMessages(nil, version, 1)
		for step := range 300 {
			changed, removed, structural := state.step()
			if changed == nil && removed == nil {
				continue
			}
			version++
			full.SyncMessages(slices.Clone(state.messages), version, 1)
			var order []string
			if structural {
				order = make([]string, len(state.messages))
				for i, message := range state.messages {
					order[i] = message.InternalID
				}
			}
			if !delta.SyncChanges(changed, removed, order, version, 1) {
				t.Fatalf("seed %d step %d: exact delta was refused", seed, step)
			}
			fresh := newActivityRenderer()
			fresh.SyncMessages(slices.Clone(state.messages), version, 1)
			want := activitySignature(fresh)
			if got := activitySignature(full); got != want {
				t.Fatalf("seed %d step %d: incremental complete sync diverged\ngot:\n%s\nwant:\n%s", seed, step, got, want)
			}
			if got := activitySignature(delta); got != want {
				t.Fatalf("seed %d step %d: delta sync diverged\ngot:\n%s\nwant:\n%s", seed, step, got, want)
			}
		}
	}
}

// A new row without its position, or a duplicate identity, cannot be applied
// exactly and must leave the projection untouched for a complete sync.
func TestSyncChangesRefusesInexactDelta(t *testing.T) {
	r := newActivityRenderer()
	r.SyncMessages([]types.ChatMessage{{InternalID: "a", Role: types.RoleUser, Content: "hi"}}, 1, 1)
	before := activitySignature(r)
	if r.SyncChanges([]types.ChatMessage{{InternalID: "b", Role: types.RoleAssistant, Content: "new"}}, nil, nil, 2, 1) {
		t.Fatal("accepted a new row without ordered membership")
	}
	if r.SyncChanges([]types.ChatMessage{{InternalID: "a", Role: types.RoleUser, Content: "x"}}, nil, nil, 2, 2) {
		t.Fatal("accepted a delta from another source generation")
	}
	if got := activitySignature(r); got != before || r.SourceVersion() != 1 {
		t.Fatalf("refused delta mutated the projection: %s", got)
	}
}
