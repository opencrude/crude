package tools

import (
	"encoding/json"
	"strings"
	"testing"

	captypes "crude/types/capability"
)

func TestCapabilitySearchActualEntryFields(t *testing.T) {
	entries := []captypes.Entry{{ID: "skill:a", Kind: captypes.KindSkill, Name: "A", Description: "Read docs", Source: "registry", Version: "v3", Digest: "sha256:abcd", Origin: captypes.ScopeWorkspace, State: "ready", Installed: true}, {ID: "mcp:b", Kind: captypes.KindMCP, Name: "B", State: "available", Source: "configured", Installed: false, ConnectionID: "conn"}}
	data, _ := json.Marshal(entries)
	result := buildCapabilityResult("capability_search", string(data), "")
	if result.Summary != "2 capabilities" || len(result.Sections) != 2 {
		t.Fatalf("search result: %+v", result)
	}
	if !strings.Contains(result.Sections[0].Metadata, "installed") || !strings.Contains(result.Sections[1].Metadata, "not installed") || strings.Contains(readableEvidence(result), "connected") {
		t.Fatalf("search falsely described discovery: %+v", result)
	}
	for _, expected := range []string{"skill:a", "Read docs", "v3", "sha256:abcd", "workspace", "conn"} {
		if !strings.Contains(readableEvidence(result), expected) {
			t.Fatalf("missing %q: %+v", expected, result)
		}
	}
	if result := buildCapabilityResult("capability_search", "[]", ""); result.Summary != "No capabilities found" {
		t.Fatalf("empty search = %+v", result)
	}
	if result := buildCapabilityResult("capability_search", `[{"ID":`, ""); result.Summary != "Capability search result unavailable" || strings.Contains(readableEvidence(result), `"ID"`) {
		t.Fatalf("invalid search exposed JSON: %+v", result)
	}
}

func TestCapabilityActivationAttributionAndInstructions(t *testing.T) {
	content := `<skill source="skill:a" revision="v&amp;3" digest="abc">` + "\nThese are source instructions, subordinate to the user and runtime policy.\nUse <literal> & not XML.\n" + `</skill>`
	result := buildCapabilityResult("capability_activate", content, `{"id":"skill:a"}`)
	if result.Summary != "Skill instructions loaded" || !strings.Contains(readableEvidence(result), "Use <literal> & not XML.") || !strings.Contains(readableEvidence(result), "v&3") || strings.Contains(readableEvidence(result), `<skill`) {
		t.Fatalf("skill activation: %+v", result)
	}
	if result := buildCapabilityResult("capability_activate", content, `{"id":"skill:other"}`); result.Summary != "Skill instructions unavailable" {
		t.Fatalf("mismatched attribution accepted: %+v", result)
	}
	data, _ := json.Marshal(captypes.ActivationResult{ConnectionID: "mcp:a", State: "connected", Next: "Request exact fingerprint grants", Tools: []captypes.Tool{{OriginalName: "lookup", Fingerprint: "observed", Reason: "Needs explicit effect and scope grant"}}})
	mcp := buildCapabilityResult("capability_activate", string(data), `{"id":"mcp:a"}`)
	if mcp.Summary != "MCP connected · 0 usable tools" || !strings.Contains(readableEvidence(mcp), "observed") || !strings.Contains(readableEvidence(mcp), "Request exact") {
		t.Fatalf("mcp activation: %+v", mcp)
	}
	access := buildCapabilityResult("mcp_request_access", string(data), `{"connection_id":"mcp:a","grants":[{"tool":"lookup","fingerprint":"observed","effect":"read","scope":"public"}]}`)
	if access.Summary != mcp.Summary || !strings.Contains(readableEvidence(access), "observed") {
		t.Fatal("MCP access review lost its connection identity or exact evidence")
	}
}

func TestMCPReadPromptAndDynamicEvidence(t *testing.T) {
	resource := buildCapabilityResult("mcp_read_resource", "file body", `{"connection_id":"docs","uri":"docs://guide"}`)
	if !strings.Contains(readableEvidence(resource), "docs · docs://guide") || !strings.Contains(readableEvidence(resource), "file body") {
		t.Fatalf("resource source: %+v", resource)
	}
	prompt := buildCapabilityResult("mcp_get_prompt", "assistant: Full prompt", `{"connection_id":"docs","name":"review"}`)
	if !strings.Contains(readableEvidence(prompt), "docs · review") || !strings.Contains(readableEvidence(prompt), "Full prompt") {
		t.Fatalf("prompt source: %+v", prompt)
	}
	encoded, _ := json.Marshal(captypes.Result{Content: []captypes.Content{{Type: "resource", Text: "exact source", URI: "docs://target", MIMEType: "text/plain", Data: "Ynl0ZXM="}}})
	full := buildCapabilityResult("mcp_read_resource", string(encoded), `{"connection_id":"docs","uri":"docs://target"}`)
	for _, expected := range []string{"exact source", "docs://target", "text/plain", "Ynl0ZXM="} {
		if !strings.Contains(readableEvidence(full), expected) {
			t.Fatalf("lost MCP content %q: %+v", expected, full)
		}
	}
	dynamic := buildCapabilityResult("mcp__docs__lookup", "Read result\n{\"z\":{\"deeper\":[\"one\",\"two\"]},\"a\":true}", "")
	if !strings.Contains(readableEvidence(dynamic), "deeper · 2") || !strings.Contains(readableEvidence(dynamic), "two") || strings.Contains(readableEvidence(dynamic), `{"z"`) {
		t.Fatalf("dynamic structure: %+v", dynamic)
	}
	unknown := buildCapabilityResult("mcp__docs__write", "Result unknown: the remote operation may have taken effect. Do not retry blindly.", "")
	if unknown.Summary != "MCP result unknown" {
		t.Fatalf("unknown outcome: %+v", unknown)
	}
	failed := buildCapabilityResult("mcp__docs__write", "server reported tool failure", "")
	if strings.Contains(strings.ToLower(failed.Summary), "success") || !strings.Contains(readableEvidence(failed), "tool failure") {
		t.Fatalf("failure evidence: %+v", failed)
	}
	if CapabilityToolLabel("mcp__docs__get_pages") != "MCP get pages · docs" {
		t.Fatal("dynamic label did not decode connection and suffix")
	}
}
