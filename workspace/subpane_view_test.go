package workspace

import (
	"testing"
	"time"
)

func TestSubPaneViewReturnsPlainData(t *testing.T) {
	sp := NewSubPane("test", PaneAgent, "agent-1")
	sp.Messages.AppendMessage(Message{Role: "assistant", Content: "hello", Time: time.Now()})
	view := sp.View()
	if view.Messages == nil {
		t.Fatal("expected Messages pointer to be non-nil")
	}
	if view.Messages != sp.Messages {
		t.Fatal("expected Messages pointer to equal sp.Messages")
	}
	if view.Header.Title != "agent-1" {
		t.Fatalf("expected title 'agent-1', got %q", view.Header.Title)
	}
	if view.Header.Type != PaneAgent {
		t.Fatalf("expected type PaneAgent, got %q", view.Header.Type)
	}
}

func TestSubPaneViewHeaderFields(t *testing.T) {
	sp := NewSubPane("id-1", PaneThinking, "Thinking")
	sp.TokenCount = 42000
	sp.Done = true
	sp.EndTime = sp.StartTime.Add(5 * time.Second)

	view := sp.View()
	if view.Header.Title != "Thinking" {
		t.Fatalf("expected title 'Thinking', got %q", view.Header.Title)
	}
	if view.Header.Type != PaneThinking {
		t.Fatalf("expected type PaneThinking, got %q", view.Header.Type)
	}
	if !view.Header.Done {
		t.Fatal("expected Done to be true")
	}
	if view.Header.Elapsed < 4*time.Second || view.Header.Elapsed > 6*time.Second {
		t.Fatalf("expected Elapsed ~5s, got %v", view.Header.Elapsed)
	}
	if view.Header.TokenCount != 42000 {
		t.Fatalf("expected TokenCount 42000, got %d", view.Header.TokenCount)
	}
}

func TestSubPaneViewSummaryWhenDone(t *testing.T) {
	sp := NewSubPane("a1", PaneAgent, "my-agent")
	sp.TokenCount = 5200
	sp.Collapse()

	view := sp.View()
	if view.Summary == nil {
		t.Fatal("expected Summary to be non-nil when Done")
	}
	if view.Summary.Text == "" {
		t.Fatal("expected Summary.Text to be non-empty")
	}
	if !contains(view.Summary.Text, "for") {
		t.Fatalf("expected Summary.Text to contain 'for', got %q", view.Summary.Text)
	}
	if !contains(view.Summary.Text, "s") {
		t.Fatalf("expected Summary.Text to contain 's', got %q", view.Summary.Text)
	}
	if !contains(view.Summary.Text, "my-agent") {
		t.Fatalf("expected Summary.Text to contain 'my-agent', got %q", view.Summary.Text)
	}
	if view.Summary.TokenCount != 5200 {
		t.Fatalf("expected Summary.TokenCount 5200, got %d", view.Summary.TokenCount)
	}
}

// TestSubPaneViewSummaryHumanDuration verifies the collapsed summary uses a
// compact human duration ("42s", "2m 10s", "1h 02m 03s") rather than decimal
// seconds, so the demoted thinking line reads "Thought for 2m 10s".
func TestSubPaneViewSummaryHumanDuration(t *testing.T) {
	tests := []struct {
		name    string
		elapsed time.Duration
		expect  string
	}{
		{"seconds", 42 * time.Second, "for 42s"},
		{"minsecs", 2*time.Minute + 10*time.Second, "for 2m 10s"},
		{"hourminsecs", time.Hour + 3*time.Minute + 5*time.Second, "for 1h 03m 05s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := NewSubPane("think-dur", PaneThinking, "Thinking")
			sp.Done = true
			sp.StartTime = time.Now().Add(-tt.elapsed)
			sp.EndTime = time.Now()
			view := sp.View()
			if !contains(view.Summary.Text, tt.expect) {
				t.Fatalf("expected Summary.Text to contain %q, got %q", tt.expect, view.Summary.Text)
			}
			if !contains(view.Summary.Text, "Thought") {
				t.Fatalf("expected summary to say 'Thought', got %q", view.Summary.Text)
			}
		})
	}
}

func TestSubPaneViewSummaryTokenFormatting(t *testing.T) {
	tests := []struct {
		name   string
		tokens int
		expect string
	}{
		{"under1000", 42, "↓42"},
		{"exact1K", 1000, "↓1K"},
		{"over1K", 1500, "↓1.5K"},
		{"exact1M", 1000000, "↓1M"},
		{"over1M", 2500000, "↓2.5M"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sp := NewSubPane("x", PaneAgent, "ag")
			sp.TokenCount = tt.tokens
			sp.Collapse()
			view := sp.View()
			if !contains(view.Summary.Text, tt.expect) {
				t.Fatalf("expected Summary.Text to contain %q, got %q", tt.expect, view.Summary.Text)
			}
		})
	}
}

func TestSubPaneViewToolLinesForAgentPane(t *testing.T) {
	sp := NewSubPane("agent-1", PaneAgent, "my-agent")
	sp.Messages.AppendMessage(Message{
		Role:             "tool",
		ToolName:         "read_file",
		ToolArgs:         "main.go",
		ToolStatus:       "success",
		ToolPreviewPhase: "done",
		Time:             time.Now(),
	})
	sp.Messages.AppendMessage(Message{
		Role:             "tool",
		ToolName:         "write_file",
		ToolArgs:         "out.go",
		ToolStatus:       "streaming",
		ToolPreviewPhase: "streaming",
		Time:             time.Now(),
	})

	view := sp.View()
	if len(view.ToolLines) != 2 {
		t.Fatalf("expected 2 tool lines, got %d", len(view.ToolLines))
	}
	if view.ToolLines[0].Label != "read_file main.go" {
		t.Fatalf("expected label 'read_file main.go', got %q", view.ToolLines[0].Label)
	}
	if view.ToolLines[0].Phase != "done" {
		t.Fatalf("expected phase 'done', got %q", view.ToolLines[0].Phase)
	}
	if view.ToolLines[0].Status != "success" {
		t.Fatalf("expected status 'success', got %q", view.ToolLines[0].Status)
	}
	if view.ToolLines[1].Label != "write_file out.go" {
		t.Fatalf("expected label 'write_file out.go', got %q", view.ToolLines[1].Label)
	}
	if view.ToolLines[1].Phase != "streaming" {
		t.Fatalf("expected phase 'streaming', got %q", view.ToolLines[1].Phase)
	}
}

func TestSubPaneViewToolLinesNilForThinking(t *testing.T) {
	sp := NewSubPane("think-1", PaneThinking, "Thinking")
	sp.Messages.AppendMessage(Message{
		Role:       "tool",
		ToolName:   "read_file",
		ToolStatus: "success",
		Time:       time.Now(),
	})

	view := sp.View()
	if view.ToolLines != nil {
		t.Fatalf("expected ToolLines to be nil for PaneThinking, got %v", view.ToolLines)
	}
}

func TestSubPaneViewThinkingFlag(t *testing.T) {
	sp := NewSubPane("think-1", PaneThinking, "Thinking")
	sp.Messages.AppendMessage(Message{
		Role:           "thinking",
		Content:        "pondering...",
		ThinkingActive: true,
		Time:           time.Now(),
	})

	view := sp.View()
	if !view.Header.Thinking {
		t.Fatal("expected Header.Thinking to be true when last message has ThinkingActive")
	}
}

func TestSubPaneViewThinkingFlagFalse(t *testing.T) {
	sp := NewSubPane("think-1", PaneThinking, "Thinking")
	sp.Messages.AppendMessage(Message{
		Role:           "thinking",
		Content:        "done pondering",
		ThinkingActive: false,
		Time:           time.Now(),
	})

	view := sp.View()
	if view.Header.Thinking {
		t.Fatal("expected Header.Thinking to be false when last message has ThinkingActive=false")
	}
}

func TestSubPaneViewMessagesPointerIsCanonical(t *testing.T) {
	sp := NewSubPane("a1", PaneAgent, "agent")
	conv := sp.Messages
	view := sp.View()
	if view.Messages != conv {
		t.Fatal("expected View().Messages to be the same pointer as sp.Messages")
	}
}

func TestSubPaneViewSummaryNilWhenActive(t *testing.T) {
	sp := NewSubPane("a1", PaneAgent, "agent")
	view := sp.View()
	if view.Summary != nil {
		t.Fatal("expected Summary to be nil when pane is not Done")
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
