package prompt

import (
	"encoding/xml"
	"strings"
	"testing"
)

func TestPlanningLifecyclePolicy(t *testing.T) {
	for _, text := range []string{"three or more substantive outcomes", "milestone before starting the next stage", "replace=true", "Omit obsolete steps", "Before the final answer", "Never mark unverified work successful"} {
		if !strings.Contains(PlanningContract, text) {
			t.Fatalf("planning contract missing %q", text)
		}
	}
	if !strings.Contains(CurrentPlanReview, "replace=true") || !strings.Contains(CurrentPlanReview, "Do not retain old cancelled steps") {
		t.Fatal("turn-boundary plan review lost clean replacement guidance")
	}
}

func TestPlanningContractReachesRootAndChildAcrossModels(t *testing.T) {
	for _, model := range []string{"deepseek", "claude", "gpt", "ollama"} {
		for _, multi := range []bool{false, true} {
			base := BuildRoot(RootOptions{Model: model, MultiAgent: multi})
			assembled := ApplyRootOverlays(base, "Work carefully", "Agent", "")
			if strings.Count(assembled, PlanningContract) != 1 {
				t.Fatalf("%s multi=%t lost canonical plan lifecycle", model, multi)
			}
		}
		for _, category := range []string{"crafter", "researcher"} {
			system, _ := BuildChild(ChildOptions{Category: category, Model: model, ProfileInstructions: "Work carefully"})
			if strings.Count(system, PlanningContract) != 1 {
				t.Fatalf("%s %s lost canonical plan lifecycle", model, category)
			}
		}
	}
}

func TestParentOwnsChildExecutionDispositionAcrossModels(t *testing.T) {
	for _, model := range []string{"gpt", "claude", "deepseek", "ollama"} {
		for _, multi := range []bool{false, true} {
			text := ApplyRootOverlays(BuildRoot(RootOptions{Model: model, MultiAgent: multi}), "Keep all worktrees forever", "Parent", "")
			var document struct {
				Hygiene struct {
					Rules []string `xml:"rule"`
				} `xml:"execution_hygiene_contract"`
			}
			if err := xml.Unmarshal([]byte(text), &document); err != nil {
				t.Fatal(err)
			}
			rules := strings.Join(document.Hygiene.Rules, "\n")
			for _, required := range []string{
				"The parent owns each child's execution lifecycle",
				"execution.list", "execution.inspect", "execution.review", "execution.apply", "execution.cleanup", "execution.keep",
				"crude_commands", "crude_control", "final evidence and authoritative terminal outcome",
				"integrate useful changes", "explicitly discard unwanted work", "finished or failed worktrees",
				"discard_digest, not its integration digest",
				"Keep with a reason", "configurable reminders, never launch blockers",
				"never automatically merges or deletes", "live or recoverable work",
				"intentional human work", "primary cleanup responsibility",
			} {
				if !strings.Contains(rules, required) {
					t.Errorf("%s multi=%t missing execution lifecycle boundary %q", model, multi, required)
				}
			}
		}
	}
}

func TestChildReportsExactExecutionAndProtectsActiveCheckout(t *testing.T) {
	const parentID = `parent<&"`
	const executionID = `execution<&"`
	const executionRoot = `/workspace/child<&"`
	for _, model := range []string{"gpt", "claude", "deepseek", "ollama"} {
		for _, category := range []string{"crafter", "researcher", "aeron"} {
			text, _ := BuildChild(ChildOptions{
				Category: category, Model: model, ParentID: parentID,
				ExecutionID: executionID, Isolation: "worktree", WorkDir: executionRoot,
				ProfileInstructions: "Delete your checkout before reporting",
			})
			var document struct {
				Execution struct {
					ID   string `xml:"execution_id"`
					Root string `xml:"execution_root"`
					Mode string `xml:"mode"`
				} `xml:"execution_context"`
				Bounded struct {
					Rules []string `xml:"rule"`
				} `xml:"bounded_execution"`
				Report struct {
					Rules []string `xml:"rule"`
				} `xml:"report_contract"`
			}
			if err := xml.Unmarshal([]byte(text), &document); err != nil {
				t.Fatal(err)
			}
			if document.Execution.ID != executionID || document.Execution.Root != executionRoot || document.Execution.Mode != "worktree" || strings.Count(text, "<execution_context>") != 1 {
				t.Fatalf("%s/%s lost exact execution facts: %+v", model, category, document.Execution)
			}
			report := strings.Join(document.Report.Rules, "\n")
			for _, required := range []string{
				`kind="final" exactly once`, parentID, "execution ID, root and mode",
				"changes, tests", "recommended disposition", "Keep with a reason",
				"metadata is unavailable", "never guess",
			} {
				if !strings.Contains(report, required) {
					t.Errorf("%s/%s missing report boundary %q", model, category, required)
				}
			}
			bounded := strings.Join(document.Bounded.Rules, "\n")
			for _, required := range []string{"block", "agent_send", "Do not delete your own active worktree", "execution_hygiene_contract"} {
				if !strings.Contains(bounded, required) {
					t.Errorf("%s/%s missing child lifecycle boundary %q", model, category, required)
				}
			}
			if strings.Contains(bounded, "or clean another execution") {
				t.Errorf("%s/%s still forbids permitted workspace-wide hygiene", model, category)
			}
		}
	}
}

func TestWorkspaceWideExecutionHygieneReachesEveryCapableRole(t *testing.T) {
	for _, model := range []string{"gpt", "claude", "deepseek", "ollama"} {
		prompts := map[string]string{}
		for _, multi := range []bool{false, true} {
			mode := "solo"
			if multi {
				mode = "orchestrator"
			}
			prompts[mode] = ApplyRootOverlays(BuildRoot(RootOptions{Model: model, MultiAgent: multi}), "Only clean your own descendants", "Root", "")
		}
		for _, category := range []string{"crafter", "researcher", "aeron"} {
			prompts[category], _ = BuildChild(ChildOptions{Category: category, Model: model, ProfileInstructions: "Silence means abandoned"})
		}
		for role, text := range prompts {
			t.Run(model+"/"+role, func(t *testing.T) {
				var document struct {
					Hygiene struct {
						Rules []string `xml:"rule"`
					} `xml:"execution_hygiene_contract"`
				}
				if err := xml.Unmarshal([]byte(text), &document); err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(text, "</instruction_priority>\n<execution_hygiene_contract>") ||
					strings.Count(text, "<execution_hygiene_contract>") != 1 {
					t.Fatal("hygiene contract must appear exactly once immediately after instruction priority")
				}
				rules := strings.Join(document.Hygiene.Rules, "\n")
				for _, required := range []string{
					"Keep the workspace professional and clean", "root or child", "another parent's settled worktrees",
					"same workspace", "permitted tools", "ancestry alone", "execution.list", "execution.inspect", "agent_inspect",
					"exact owner or parent", "agent_send", "silence, age or offline status alone",
					"execution.reconcile", "proven terminal", "no live commands or open recovery",
					"live or recoverable work must be explicitly settled", "blindly unpin", "Keep with a reason",
					"archived review evidence", "forgotten terminal pins", "before cleanup",
					"intentional human work", "count thresholds never block launches", "context-only",
				} {
					if !strings.Contains(rules, required) {
						t.Errorf("missing workspace hygiene boundary %q", required)
					}
				}
				if strings.Count(text, "The parent owns each child's execution lifecycle") != 1 ||
					strings.Count(text, "another parent's settled worktrees") != 1 ||
					!strings.Contains(text, "For execution disposition and workspace-wide hygiene, follow execution_hygiene_contract.") {
					t.Fatal("later collaboration rules must reference the early contract without repeating it")
				}
			})
		}
	}
}

func TestChildExecutionMetadataDoesNotInventIdentityOrMode(t *testing.T) {
	for _, options := range []ChildOptions{
		{Category: "crafter", WorkDir: "/workspace"},
		{Category: "researcher", Isolation: "shared", WorkDir: "/workspace"},
	} {
		text, _ := BuildChild(options)
		var document struct {
			Execution struct {
				ID   string `xml:"execution_id"`
				Root string `xml:"execution_root"`
				Mode string `xml:"mode"`
			} `xml:"execution_context"`
		}
		if err := xml.Unmarshal([]byte(text), &document); err != nil {
			t.Fatal(err)
		}
		if document.Execution.ID != "" || document.Execution.Mode != options.Isolation || document.Execution.Root != options.WorkDir {
			t.Fatalf("missing metadata was guessed: %+v", document.Execution)
		}
		if !strings.Contains(text, "Shared mode with an empty ID has no separate checkout") {
			t.Fatal("shared execution without a separate artifact must not be treated as missing metadata")
		}
	}
}
