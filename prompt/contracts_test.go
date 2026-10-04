package prompt

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEngineeringContractInEveryExecutionMode(t *testing.T) {
	prompts := map[string]string{
		"solo":         BuildRoot(RootOptions{WorkDir: t.TempDir()}),
		"orchestrator": BuildRoot(RootOptions{MultiAgent: true, WorkDir: t.TempDir()}),
	}
	for _, category := range []string{"researcher", "crafter", "aeron"} {
		child, _ := BuildChild(ChildOptions{Category: category, WorkDir: t.TempDir()})
		prompts[category] = child
	}
	for name, text := range prompts {
		t.Run(name, func(t *testing.T) {
			if strings.Count(text, EngineeringContract) != 1 {
				t.Fatal("prompt must include exactly one canonical engineering contract")
			}
			if strings.Count(text, RuntimeCapabilities) != 1 {
				t.Fatal("prompt must include exactly one runtime capability contract")
			}
			if !strings.Contains(text, "never call an edit tool with identical old and new content") ||
				strings.Contains(text, "Prefer incremental edits over whole-file rewrites") {
				t.Fatal("all agents must skip no-op edits without forcing incremental patches")
			}
			var document struct {
				Engineering struct {
					Rules []string `xml:"rule"`
				} `xml:"engineering_contract"`
			}
			if err := xml.Unmarshal([]byte(text), &document); err != nil {
				t.Fatalf("prompt is not valid XML: %v", err)
			}
			rules := strings.Join(document.Engineering.Rules, "\n")
			for _, required := range []string{"user-visible outcome and acceptance criteria", "authoritative data and state", "not merely the smallest local patch", "reuse a component only when its semantics fit", "independently of the proposed implementation", "what must stay absent", "When corrected or interrupted", "not the investigation superficial", "Batch related reads and coherent edits", "Stay within the requested or delegated scope", "state any unverified behavior"} {
				if !strings.Contains(rules, required) {
					t.Errorf("engineering contract missing %q", required)
				}
			}
			for _, required := range []string{"workspace IDE runtime", "Skills and MCP", "delivered automatically", "active attached run", "Never use it for routine progress", "immutable reports"} {
				if !strings.Contains(text, required) {
					t.Errorf("runtime capability contract missing %q", required)
				}
			}
		})
	}
	for _, mode := range []string{"solo", "orchestrator"} {
		for _, want := range []string{"<activity_communication>", "brief, truthful one-line progress update", "automatically groups routine settled tool calls", "End with a normal response"} {
			if !strings.Contains(prompts[mode], want) {
				t.Errorf("%s prompt missing activity guidance %q", mode, want)
			}
		}
	}
	for _, category := range []string{"researcher", "crafter", "aeron"} {
		if strings.Contains(prompts[category], "<activity_communication>") {
			t.Errorf("%s leaf prompt must report to parent", category)
		}
	}
	if !strings.Contains(prompts["researcher"], "Do not edit files.") || strings.Contains(prompts["aeron"], "<planning_contract>") {
		t.Fatal("child category boundaries changed")
	}
}

func TestWholeProjectGuidanceContractInEveryExecutionMode(t *testing.T) {
	dir := t.TempDir()
	const content = "fixture repository policy body"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	prompts := map[string]string{
		"solo":         BuildRoot(RootOptions{WorkDir: dir, Model: "other-model"}),
		"orchestrator": BuildRoot(RootOptions{MultiAgent: true, WorkDir: dir, Model: "gpt-6-sol"}),
	}
	for _, category := range []string{"researcher", "crafter", "aeron"} {
		child, _ := BuildChild(ChildOptions{Category: category, WorkDir: dir, Model: "gpt-6-sol"})
		prompts[category] = child
	}
	for name, text := range prompts {
		t.Run(name, func(t *testing.T) {
			if strings.Count(text, ProjectGuidanceContract) != 1 || !strings.Contains(text, "Before the first project-specific model request") ||
				!strings.Contains(text, "AGENTS.md") || !strings.Contains(text, "CLAUDE.md") || !strings.Contains(text, "SOUL.md") ||
				!strings.Contains(text, "bounded complete-source chunks") || !strings.Contains(text, "not mandatory prompt cargo") {
				t.Fatal("prompt does not require complete scoped project guidance before work")
			}
			if strings.Contains(text, content) {
				t.Fatal("pure prompt assembly performed filesystem I/O")
			}
			for _, required := range []string{"source hashes", "Reading supplied content satisfies", "Avoid redundant discovery", "searches for additional scopes", "exact-source reads", "guidance-file audits or edits remain available"} {
				if !strings.Contains(text, required) {
					t.Errorf("guidance intake contract missing %q", required)
				}
			}
		})
	}
}

func TestPlanningAndOperatingModeContracts(t *testing.T) {
	orchestrator := BuildRoot(RootOptions{MultiAgent: true, WorkDir: "/tmp/workspace"})
	for _, required := range []string{"update_plan", "get_plan", "omit unchanged objective/status/steps", "revision conflict", "not a notepad", "Statuses are declarative, not execution gates", "cancel, reopen, retry, or reorder", "On every new human message", "After compaction", "agent_start", "agent_wait", "never as routine polling"} {
		if !strings.Contains(orchestrator, required) {
			t.Fatalf("orchestrator prompt missing %q", required)
		}
	}
	for _, removed := range []string{"mark_step_complete", "mark_step_doing", "report_blocker", "max_steps", "wait=true", "wait=false"} {
		if strings.Contains(orchestrator, removed) {
			t.Fatalf("orchestrator prompt still carries %q", removed)
		}
	}
	solo := BuildRoot(RootOptions{WorkDir: `/tmp/<workspace>&one`})
	if !strings.Contains(solo, `<operating_mode type="solo">`) || strings.Contains(solo, `<operating_mode type="orchestrator">`) ||
		!strings.Contains(solo, `/tmp/&lt;workspace&gt;&amp;one`) {
		t.Fatalf("solo XML contract = %q", solo)
	}
	researcher, _ := BuildChild(ChildOptions{Category: "researcher", WorkDir: "/tmp/workspace"})
	aeron, _ := BuildChild(ChildOptions{Category: "aeron", WorkDir: "/tmp/workspace"})
	if !strings.Contains(researcher, "<planning_contract>") || strings.Contains(aeron, "<planning_contract>") {
		t.Fatal("child planning contract does not match tool availability")
	}
}

func TestRootPromptEnvironmentIsStableAndModelNeutral(t *testing.T) {
	t.Setenv("SHELL", "/bin/cache-prefix-one")
	first := BuildRoot(RootOptions{MultiAgent: true, Model: "gpt-6-sol"})
	t.Setenv("SHELL", "/bin/cache-prefix-two")
	second := BuildRoot(RootOptions{MultiAgent: true, Model: "claude-opus"})
	if first == second {
		t.Fatal("stable environment changes were not reflected")
	}
	for _, text := range []string{first, second} {
		for _, volatile := range []string{"<date>", "<git>", "<directory>"} {
			if strings.Contains(text, volatile) {
				t.Fatalf("cacheable prompt contains volatile field %q", volatile)
			}
		}
	}
	t.Setenv("SHELL", "/bin/cache-prefix-one")
	gpt := BuildRoot(RootOptions{MultiAgent: true, WorkDir: "/workspace", Model: "gpt-6-sol"})
	claude := BuildRoot(RootOptions{MultiAgent: true, WorkDir: "/workspace", Model: "claude-opus"})
	if gpt != claude {
		t.Fatal("model family forked the canonical behavioral prompt")
	}
}

func TestPromptAdvertisesExactCrossPlatformTempDirectoryAndBoundedOutputPolicy(t *testing.T) {
	tempDir := filepath.Join(t.TempDir(), "agent <temp>&logs")
	root := BuildRoot(RootOptions{WorkDir: t.TempDir(), TempDir: tempDir})
	child, _ := BuildChild(ChildOptions{Category: "crafter", WorkDir: t.TempDir(), TempDir: tempDir})
	for name, text := range map[string]string{"root": root, "child": child} {
		t.Run(name, func(t *testing.T) {
			if !strings.Contains(text, "<temporary_directory>") ||
				!strings.Contains(text, "CRUDE_TEMP_DIR") ||
				!strings.Contains(text, XMLText(tempDir)) ||
				!strings.Contains(text, "never drain a huge log into context") {
				t.Fatalf("prompt lacks temp/output contract: %s", text)
			}
		})
	}
}

func TestCollaborationEventMarksRareRunningAgentBroadcast(t *testing.T) {
	rendered := CollaborationEvent(CollaborationEventData{
		Origin: "peer", EventID: "event-1", ThreadID: "thread-1",
		SenderID: "agent-a", SenderName: "Asha", Kind: "peer_note",
		Body: "Stop before publishing stale output.", Broadcast: true,
	})
	for _, want := range []string{`broadcast="true"`, "active attached runs", "do not rebroadcast"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("broadcast event missing %q: %s", want, rendered)
		}
	}
}
