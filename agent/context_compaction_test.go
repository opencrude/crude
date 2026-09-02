package agent

import (
	"strings"
	"testing"

	"crude/provider"
)

func TestCompactContextNeverSummarizesOnlyCurrentInstruction(t *testing.T) {
	current := provider.NewTextMessage("user", strings.Repeat("current instruction ", 10000))
	compacted := CompactContext([]provider.ChatMessage{current}, 4096, 70, "test")
	if len(compacted) != 1 || compacted[0].Content.String() != current.Content.String() {
		t.Fatalf("current instruction was replaced: %#v", compacted)
	}
}

func TestCompactContextZeroDisablesCompaction(t *testing.T) {
	messages := []provider.ChatMessage{
		provider.NewTextMessage("user", strings.Repeat("old context ", 5000)),
		provider.NewTextMessage("assistant", strings.Repeat("old answer ", 5000)),
	}
	compacted := CompactContext(messages, 4096, 0, "test")
	if len(compacted) != len(messages) || compacted[0].Content.String() != messages[0].Content.String() {
		t.Fatalf("disabled compaction changed context: %#v", compacted)
	}
}

func TestCompactContextAlwaysRebuildsFromFullArchive(t *testing.T) {
	var archive []provider.ChatMessage
	for i := 0; i < 30; i++ {
		archive = append(archive,
			provider.NewTextMessage("user", strings.Repeat("request ", 300)),
			provider.NewTextMessage("assistant", strings.Repeat("answer ", 300)),
		)
	}
	first := CompactContext(archive, 4096, 70, "test")
	if len(first) == 0 || !strings.Contains(first[0].Content.String(), "<context_checkpoint") {
		t.Fatalf("first checkpoint = %#v", first)
	}
	fullArchiveLater := append(append([]provider.ChatMessage(nil), archive...),
		provider.NewTextMessage("user", "new current request"),
		provider.NewTextMessage("assistant", "new answer"),
	)
	second := CompactContext(fullArchiveLater, 4096, 70, "test")
	if strings.Count(second[0].Content.String(), "<context_checkpoint") != 1 {
		t.Fatalf("checkpoint summarized an earlier checkpoint: %q", second[0].Content.String())
	}
}
