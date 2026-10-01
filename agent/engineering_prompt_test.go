package agent

import (
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/codexio"
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

func TestProjectGuidanceIsReadByAgentInsteadOfPreloaded(t *testing.T) {
	dir := t.TempDir()
	const content = "repository guidance must be read with tools"
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	for name, prompt := range map[string]string{
		"solo":               BuildSystemPromptForModel(false, dir, "other-model"),
		"orchestrator":       BuildSystemPromptForModel(true, dir, "other-model"),
		"gpt main":           BuildSystemPromptForModel(true, dir, "gpt-6-sol"),
		"gpt fallback":       BuildSystemPromptForModel(true, dir, "gpt-unknown"),
		"researcher":         subPromptForGuidanceTest(SubAgentSpec{Category: CatResearcher}, dir),
		"gpt researcher":     subPromptForGuidanceTest(SubAgentSpec{Category: CatResearcher, Model: "gpt-6-sol"}, dir),
		"gpt fallback child": subPromptForGuidanceTest(SubAgentSpec{Category: CatResearcher, Model: "gpt-unknown"}, dir),
	} {
		t.Run(name, func(t *testing.T) {
			if strings.Count(prompt, projectGuidancePrompt) != 1 || !strings.Contains(prompt, "answering a question about a repository") ||
				!strings.Contains(prompt, "AGENTS.md") || !strings.Contains(prompt, "CLAUDE.md") ||
				!strings.Contains(prompt, "not automatically loaded") || !strings.Contains(prompt, "governing files") {
				t.Fatal("prompt does not tell agent to inspect scoped project guidance")
			}
			if strings.Contains(prompt, content) || strings.Contains(prompt, "included with the developer message") || strings.Contains(prompt, "don't need to be re-read") {
				t.Fatal("prompt claimed the project file was supplied or read it automatically")
			}
		})
	}
	defaultPrompt, source := codexio.Instructions("gpt-unknown")
	if source != "default" || !strings.Contains(defaultPrompt, "not supplied automatically") || strings.Contains(defaultPrompt, "included with the developer message") {
		t.Fatal("GPT fallback still asserts that project files were injected")
	}
}

func subPromptForGuidanceTest(spec SubAgentSpec, dir string) string {
	system, _ := BuildSubAgentPromptInDir(spec, "", dir)
	return system
}
