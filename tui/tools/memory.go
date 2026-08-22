package tools

import (
	"encoding/json"
	"strings"

	"crude/tui/rendering"
)

// MemoryRowKind classifies a memory-tool result line for semantic coloring.
type MemoryRowKind int

const (
	MemoryRowDefault MemoryRowKind = iota
	MemoryRowTitle                  // a bold heading (e.g. a memory's title)
	MemoryRowMeta                   // muted metadata (kind / importance / when)
	MemoryRowBody                   // the content / summary text
)

// MemoryLine is one structured memory-tool result row.
type MemoryLine struct {
	Text string
	Kind MemoryRowKind
}

// IsMemorySearchTool reports whether a tool returns a structured memory JSON
// payload that should be pretty-projected rather than shown as raw JSON.
func IsMemorySearchTool(name string) bool {
	switch name {
	case "recall_memory", "search_history":
		return true
	}
	return false
}

// MemoryResultSummary returns a compact title-line summary for the memory
// tool family. It parses the JSON payload and counts entries, so the header
// reads "Searched memory — 3 memories" instead of dumping raw JSON.
func MemoryResultSummary(tool, content string) string {
	switch tool {
	case "recall_memory":
		var payload struct {
			Results []map[string]any `json:"results"`
			Total   int              `json:"total"`
		}
		if err := json.Unmarshal([]byte(content), &payload); err != nil {
			return ""
		}
		if payload.Total == 0 {
			return "Recalled — no matches"
		}
		return "Recalled — " + rendering.FormatCount(payload.Total) + " memories"
	case "search_history":
		var payload struct {
			Turns    []map[string]any `json:"turns"`
			Sessions []map[string]any `json:"sessions"`
		}
		if err := json.Unmarshal([]byte(content), &payload); err != nil {
			return ""
		}
		n := len(payload.Turns) + len(payload.Sessions)
		if n == 0 {
			return "Searched history — no matches"
		}
		return "Searched history — " + rendering.FormatCount(n) + " results"
	case "remember":
		var payload map[string]any
		if err := json.Unmarshal([]byte(content), &payload); err != nil {
			return ""
		}
		if title, _ := payload["title"].(string); title != "" {
			return "Remembered — " + rendering.TruncateLine(title, 40)
		}
		return "Remembered"
	case "update_memory":
		var payload map[string]any
		if err := json.Unmarshal([]byte(content), &payload); err != nil {
			return ""
		}
		if id, _ := payload["id"].(string); id != "" {
			return "Updated memory — " + rendering.TruncateLine(id, 40)
		}
		return "Updated memory"
	case "forget_memory":
		var payload map[string]any
		if err := json.Unmarshal([]byte(content), &payload); err != nil {
			return ""
		}
		if id, _ := payload["id"].(string); id != "" {
			return "Forgot memory — " + rendering.TruncateLine(id, 40)
		}
		return "Forgot memory"
	}
	return ""
}

// BuildMemoryResultRows parses a memory-tool JSON payload into structured
// rows (let contentWidth be the column width for truncation). Empty or
// non-JSON input returns nil so the caller falls back to the plain body path.
// The chatprojection layer maps MemoryLine.Kind to concrete RowSpan roles.
func BuildMemoryResultRows(tool, content string, contentWidth int) []MemoryLine {
	switch tool {
	case "recall_memory":
		return buildRecallRows(content, contentWidth)
	case "search_history":
		return buildHistoryRows(content, contentWidth)
	}
	return nil
}

func buildRecallRows(content string, contentWidth int) []MemoryLine {
	var payload struct {
		Results []struct {
			ID         string `json:"id"`
			Kind       string `json:"kind"`
			Title      string `json:"title"`
			Content    string `json:"content"`
			Importance int    `json:"importance"`
			Pinned     bool   `json:"pinned"`
			Status     string `json:"status"`
			Source     string `json:"source"`
			When       string `json:"last_updated"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil || len(payload.Results) == 0 {
		return nil
	}
	var out []MemoryLine
	for _, r := range payload.Results {
		meta := []string{}
		if r.Kind != "" {
			meta = append(meta, r.Kind)
		}
		if r.Importance > 0 {
			meta = append(meta, "importance "+rendering.FormatCount(r.Importance))
		}
		if r.Pinned {
			meta = append(meta, "pinned")
		}
		if r.When != "" {
			meta = append(meta, r.When)
		}
		if len(meta) > 0 {
			out = append(out, MemoryLine{Text: truncateLineToWidth(strings.Join(meta, " · "), contentWidth), Kind: MemoryRowMeta})
		}
		if r.Title != "" {
			out = append(out, MemoryLine{Text: truncateLineToWidth(r.Title, contentWidth), Kind: MemoryRowTitle})
		}
		if r.Content != "" {
			out = append(out, MemoryLine{Text: truncateLineToWidth(r.Content, contentWidth), Kind: MemoryRowBody})
		}
	}
	return out
}

func buildHistoryRows(content string, contentWidth int) []MemoryLine {
	var payload struct {
		Turns    []struct {
			SessionID string   `json:"session_id"`
			Turn      int      `json:"turn"`
			When      string   `json:"when"`
			User      string   `json:"user"`
			Assistant string   `json:"assistant"`
			Tools     []string `json:"tools"`
			Files     []string `json:"files"`
		} `json:"turns"`
		Sessions []struct {
			SessionID string   `json:"session_id"`
			Title     string   `json:"title"`
			Summary   string   `json:"summary"`
			Outcome   string   `json:"outcome"`
			KeyFiles  []string `json:"key_files"`
			When      string   `json:"when"`
		} `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return nil
	}
	var out []MemoryLine
	for _, s := range payload.Sessions {
		meta := []string{"session"}
		if s.When != "" {
			meta = append(meta, s.When)
		}
		out = append(out, MemoryLine{Text: truncateLineToWidth(strings.Join(meta, " · "), contentWidth), Kind: MemoryRowMeta})
		if s.Title != "" {
			out = append(out, MemoryLine{Text: truncateLineToWidth(s.Title, contentWidth), Kind: MemoryRowTitle})
		}
		if s.Summary != "" {
			out = append(out, MemoryLine{Text: truncateLineToWidth(s.Summary, contentWidth), Kind: MemoryRowBody})
		}
		if s.Outcome != "" {
			out = append(out, MemoryLine{Text: truncateLineToWidth(s.Outcome, contentWidth), Kind: MemoryRowBody})
		}
		if len(s.KeyFiles) > 0 {
			out = append(out, MemoryLine{Text: truncateLineToWidth("Files: "+strings.Join(s.KeyFiles, ", "), contentWidth), Kind: MemoryRowMeta})
		}
	}
	for _, t := range payload.Turns {
		meta := []string{"turn"}
		if t.When != "" {
			meta = append(meta, t.When)
		}
		if len(t.Tools) > 0 {
			meta = append(meta, "tools: "+strings.Join(t.Tools, ", "))
		}
		out = append(out, MemoryLine{Text: truncateLineToWidth(strings.Join(meta, " · "), contentWidth), Kind: MemoryRowMeta})
		if t.User != "" {
			out = append(out, MemoryLine{Text: truncateLineToWidth(t.User, contentWidth), Kind: MemoryRowBody})
		}
		if t.Assistant != "" {
			out = append(out, MemoryLine{Text: truncateLineToWidth(t.Assistant, contentWidth), Kind: MemoryRowBody})
		}
		if len(t.Files) > 0 {
			out = append(out, MemoryLine{Text: truncateLineToWidth("Files: "+strings.Join(t.Files, ", "), contentWidth), Kind: MemoryRowMeta})
		}
	}
	return out
}
