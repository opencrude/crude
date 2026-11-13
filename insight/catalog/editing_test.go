package catalog

import (
	"testing"
)

func TestEditingPackIsValidatedAndReturnedAsDetachedData(t *testing.T) {
	raw := `{"format":1,"languages":[{"id":"fixture","extensions":[".fixture"],"lsp_language_id":"fixture","editing":{"brackets":[["{","}"]],"on_enter_rules":[{"before_text":{"pattern":"^(?!skip).*open$","flags":"i"},"after_text":{"pattern":"^close"},"action":{"indent":"indent","append_text":"leader "}}],"indentation_rules":{"increase_indent_pattern":{"pattern":"open$"},"decrease_indent_pattern":{"pattern":"^close"},"indent_next_line_pattern":{"pattern":"once$"}}}}]}`
	c, err := Load(nil, []byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	first, _ := c.LanguageForPath("file.fixture", nil)
	first.Editing.Brackets[0][0] = "broken"
	first.Editing.OnEnterRules[0].AfterText.Pattern = "broken"
	first.Editing.IndentationRules.IndentNextLinePattern.Pattern = "broken"
	next, _ := c.LanguageForPath("file.fixture", nil)
	if next.Editing.Brackets[0][0] != "{" || next.Editing.OnEnterRules[0].AfterText.Pattern != "^close" || next.Editing.IndentationRules.IndentNextLinePattern.Pattern != "once$" {
		t.Fatal("consumer mutated catalog authority")
	}
	for _, mutate := range []func(*LanguageEditing){
		func(v *LanguageEditing) { v.Brackets[0][0] = "" },
		func(v *LanguageEditing) { v.OnEnterRules[0].BeforeText.Pattern = "[" },
		func(v *LanguageEditing) { v.OnEnterRules[0].Action.Indent = "unknown" },
		func(v *LanguageEditing) { v.OnEnterRules[0].Action.RemoveText = -1 },
		func(v *LanguageEditing) { v.OnEnterRules[0].BeforeText.Flags = "x" },
	} {
		record := LanguageRecord{ID: "fixture", Extensions: []string{".fixture"}, LSPLanguageID: "fixture", Editing: cloneEditing(next.Editing)}
		mutate(record.Editing)
		data, err := Encode(Document{Languages: []LanguageRecord{record}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Load(c, data); err == nil {
			t.Fatal("invalid editing pack replaced the catalog")
		}
		still, _ := c.Language("fixture")
		if still.Editing.Brackets[0][0] != "{" {
			t.Fatal("rejected pack damaged previous data")
		}
	}
}
