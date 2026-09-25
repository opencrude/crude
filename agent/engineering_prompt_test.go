package agent

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestEngineeringContractInEveryExecutionMode(t *testing.T) {
	prompts := map[string]string{
		"solo":         BuildSystemPromptInDir(false, t.TempDir()),
		"orchestrator": BuildSystemPromptInDir(true, t.TempDir()),
	}
	for _, category := range []string{CatResearcher, CatCrafter, CatAeron} {
		prompt, _ := BuildSubAgentPromptInDir(SubAgentSpec{Category: category}, "", t.TempDir())
		prompts[category] = prompt
	}
	for name, prompt := range prompts {
		t.Run(name, func(t *testing.T) {
			if strings.Count(prompt, engineeringContractPrompt) != 1 {
				t.Fatal("prompt must include exactly one canonical engineering contract")
			}
			if !strings.Contains(prompt, "never call edit_file with identical old_string and new_string") ||
				strings.Contains(prompt, "Prefer incremental edits over whole-file rewrites") {
				t.Fatal("all agents must skip no-op edits without forcing incremental patches")
			}
			var document struct {
				Engineering struct {
					Rules []string `xml:"rule"`
				} `xml:"engineering_contract"`
			}
			if err := xml.Unmarshal([]byte(prompt), &document); err != nil {
				t.Fatalf("prompt is not valid XML: %v", err)
			}
			rules := strings.Join(document.Engineering.Rules, "\n")
			for _, required := range []string{
				"user-visible outcome and acceptance criteria",
				"authoritative data and state",
				"not merely the smallest local patch",
				"reuse a component only when its semantics fit",
				"independently of the proposed implementation",
				"what must stay absent",
				"When corrected or interrupted",
				"not the investigation superficial",
				"Batch related reads and coherent edits",
				"Stay within the requested or delegated scope",
				"state any unverified behavior",
			} {
				if !strings.Contains(rules, required) {
					t.Errorf("engineering contract missing %q", required)
				}
			}
		})
	}
	for _, mode := range []string{"solo", "orchestrator"} {
		prompt := prompts[mode]
		for _, want := range []string{"<activity_communication>", "brief, truthful one-line progress update", "automatically groups routine settled tool calls", "End with a normal response"} {
			if !strings.Contains(prompt, want) {
				t.Errorf("%s prompt missing activity guidance %q", mode, want)
			}
		}
	}
	for _, category := range []string{CatResearcher, CatCrafter, CatAeron} {
		if strings.Contains(prompts[category], "<activity_communication>") {
			t.Errorf("%s leaf prompt must report to parent, not send user-facing progress", category)
		}
	}
	if !strings.Contains(prompts[CatResearcher], "Do not edit files.") {
		t.Fatal("shared contract must not remove the researcher's read-only boundary")
	}
	if strings.Contains(prompts[CatAeron], "<planning_contract>") {
		t.Fatal("shared contract must not advertise unavailable planning tools")
	}
}
