package logger

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// TurnLogger writes per-turn LLM request/response dump files.
// Each turn is a numbered markdown file in the session's turns/ directory.
type TurnLogger struct {
	dir     string
	enabled bool
	mu      sync.Mutex
	counter int
}

// TurnToolCall represents a tool call in the LLM response.
type TurnToolCall struct {
	ID        string
	Name      string
	Arguments string
}

// TurnRecord captures one LLM API call for turn logging.
type TurnRecord struct {
	Time             time.Time
	Model            string
	LatencyMS        int64
	PromptTokens     int
	CompletionTokens int
	CachedTokens     int
	FinishReason     string
	RequestBody      []byte // Raw JSON request body sent to API
	ResponseContent  string // Text content in response
	ReasoningContent string // Reasoning/thinking content
	ToolCalls        []TurnToolCall
}

// SetEnabled enables or disables turn logging.
func (t *TurnLogger) SetEnabled(on bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.enabled = on
}

// Write creates a numbered turn file with the full request/response dump.
// Returns the filename (e.g. "001.md") or "" if disabled.
func (t *TurnLogger) Write(rec TurnRecord) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.enabled {
		return ""
	}
	t.counter++

	filename := fmt.Sprintf("%03d.md", t.counter)
	path := filepath.Join(t.dir, filename)

	content := formatTurn(rec, t.counter)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return ""
	}
	return filename
}

// Close is a no-op; kept for interface consistency.
func (t *TurnLogger) Close() {}

func formatTurn(rec TurnRecord, num int) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# Turn %03d\n\n", num)
	fmt.Fprintf(&b, "time: %s\n", rec.Time.Format(time.RFC3339))
	fmt.Fprintf(&b, "model: %s\n", rec.Model)
	fmt.Fprintf(&b, "latency: %dms\n", rec.LatencyMS)
	fmt.Fprintf(&b, "tokens: prompt=%d completion=%d", rec.PromptTokens, rec.CompletionTokens)
	if rec.CachedTokens > 0 {
		fmt.Fprintf(&b, " cached=%d", rec.CachedTokens)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "finish: %s\n\n", rec.FinishReason)

	// REQUEST section: full JSON dump
	b.WriteString("## REQUEST\n\n")
	if len(rec.RequestBody) > 0 {
		var pretty bytes.Buffer
		if json.Indent(&pretty, rec.RequestBody, "", "  ") == nil {
			b.WriteString("```json\n")
			b.WriteString(pretty.String())
			b.WriteString("\n```\n\n")
		} else {
			b.WriteString("```\n")
			b.WriteString(string(rec.RequestBody))
			b.WriteString("\n```\n\n")
		}
	}

	// RESPONSE section: reconstructed from stream
	b.WriteString("## RESPONSE\n\n")
	if rec.ResponseContent != "" {
		b.WriteString("content:\n")
		b.WriteString(rec.ResponseContent)
		b.WriteString("\n\n")
	}
	if rec.ReasoningContent != "" {
		b.WriteString("reasoning:\n")
		b.WriteString(rec.ReasoningContent)
		b.WriteString("\n\n")
	}
	fmt.Fprintf(&b, "tool_calls: %d\n\n", len(rec.ToolCalls))
	for _, tc := range rec.ToolCalls {
		fmt.Fprintf(&b, "### %s: %s\n\n", tc.ID, tc.Name)
		b.WriteString("```json\n")
		b.WriteString(tc.Arguments)
		b.WriteString("\n```\n\n")
	}

	return b.String()
}
