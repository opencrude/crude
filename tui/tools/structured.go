package tools

import (
	"fmt"

	toolcontract "crude/tools"
)

// ResultSection preserves semantic boundaries before terminal geometry is
// applied. Compact previews select sections and wrap their bodies; they never
// shorten the source strings used by expanded disclosure or clipboard copy.
type ResultSection struct {
	Title, Metadata, Body string
	Details               []ResultField
	DetailOnly            bool
}

type ResultField struct{ Label, Value string }

type StructuredResult struct {
	Summary  string
	Unit     string
	Sections []ResultSection
}

// IsStructuredTool is the shared routing contract for related knowledge,
// collaboration and capability tools, including connected MCP operations.
func IsStructuredTool(name string) bool {
	return toolcontract.RetainsStructuredToolInput(name)
}

func resultCount(count int, singular, plural string) string {
	if count == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", count, plural)
}

func BuildStructuredResult(name, content, input string) StructuredResult {
	switch name {
	case "agent_list", "agent_inspect", "agent_start", "agent_send", "agent_respond", "agent_control",
		"publish_report", "artifact_read", "agent_wait", "board_read":
		return buildCollaborationResult(name, content, input)
	case "remember", "recall_memory", "update_memory", "forget_memory", "memory_feedback", "search_history":
		return buildMemoryResult(name, content, input)
	default:
		return buildCapabilityResult(name, content, input)
	}
}
