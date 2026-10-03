package agent

import (
	"strings"
	"testing"

	rootprompt "crude/prompt"
	"crude/tools"
)

func TestDelegationHasBoundedHandoffAndSingleVerificationOwner(t *testing.T) {
	main := rootprompt.BuildRoot(rootprompt.RootOptions{MultiAgent: true})
	child, _ := rootprompt.BuildChild(rootprompt.ChildOptions{Category: CatCrafter})
	for _, phrase := range []string{"stopping condition", "parent owns consolidated", "Completion notifications replace routine polling"} {
		if !strings.Contains(main, phrase) {
			t.Errorf("main lacks %q", phrase)
		}
	}
	for _, phrase := range []string{"blocked report immediately and stop", "Repeat a check only after", "parent owns integration"} {
		if !strings.Contains(child, phrase) {
			t.Errorf("child lacks %q", phrase)
		}
	}
	for _, tool := range tools.AgentTools {
		if tool.Function.Name != "write_report" {
			continue
		}
		text := tool.Function.Description + string(tool.Function.Parameters)
		for _, bad := range []string{"continue working", "Do NOT summarize", "verbatim file contents"} {
			if strings.Contains(text, bad) {
				t.Errorf("report tool contradicts bounded handoff: %s", bad)
			}
		}
		if !strings.Contains(text, "then stop") {
			t.Fatal("report tool lacks terminal handoff")
		}
	}
}
