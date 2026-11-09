package tools

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFileHintNamesNearbyCandidates(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"lsp_manager.go", "lsp_types.go", "session.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	missing := filepath.Join(dir, "lsp.go")
	result := readFileHint(fmt.Errorf("file not found: %s: %w", missing, fs.ErrNotExist), missing)
	hint := result.AgentHint
	if !strings.Contains(hint, "lsp_manager.go") || strings.Index(hint, "lsp_manager.go") > strings.Index(hint, "session.go") {
		t.Fatalf("closest candidates not listed first: %s", hint)
	}
	deep := filepath.Join(dir, "nope", "deeper", "x.go")
	if hint := readFileHint(fmt.Errorf("missing: %w", fs.ErrNotExist), deep).AgentHint; !strings.Contains(hint, "session.go") {
		t.Fatalf("missing directories should fall back to the nearest existing one: %s", hint)
	}
}

func TestStructuredHintKeepsMachineReadableEvidenceAndTheSourceUnchanged(t *testing.T) {
	result := ToolResult{Content: `{"status":"needs_attention","recovery":{"id":"incident","generation":4}}`, AgentHint: "Inspect exact state."}
	parts := ResultContentParts(result)
	var body struct {
		Status   string `json:"status"`
		Recovery struct {
			ID         string `json:"id"`
			Generation int    `json:"generation"`
		} `json:"recovery"`
		Hint string `json:"agent_hint"`
	}
	if len(parts) != 1 || json.Unmarshal([]byte(parts[0].Text), &body) != nil || body.Status != "needs_attention" || body.Recovery.ID != "incident" || body.Recovery.Generation != 4 || body.Hint != "Hint: Inspect exact state." {
		t.Fatalf("hint corrupted machine-readable observation: %+v", parts)
	}
	if strings.Contains(result.Content, "agent_hint") || strings.Contains(result.Content, result.AgentHint) {
		t.Fatal("composition modified display evidence")
	}
}

// Native hints are model input, not display text, independent of whitespace.
func TestToolHintsHaveSeparateModelAndDisplayChannels(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.go")
	failure := readFileHint(fmt.Errorf("file not found: %s: %w", missing, fs.ErrNotExist), missing)
	for _, result := range []ToolResult{failure, noChangeEditResult("a", "a")} {
		if result.AgentHint == "" || strings.Contains(result.Content, "Hint:") {
			t.Fatalf("hint entered display evidence: %+v", result)
		}
		var model strings.Builder
		for _, part := range ResultContentParts(result) {
			model.WriteString(part.Text)
		}
		if !strings.Contains(model.String(), result.AgentHint) || !strings.Contains(model.String(), result.Content) {
			t.Fatal("model lost error or repair guidance")
		}
	}
}
