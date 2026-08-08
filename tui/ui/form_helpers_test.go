package ui

import (
	"testing"

	"crude/types"
)

func TestTextFieldStateCommitsInitialValue(t *testing.T) {
	f := TextFieldState("tab-size", "Tab size", "4", "Number of spaces.")
	if f.Type != FieldText {
		t.Fatalf("type=%v want FieldText", f.Type)
	}
	if f.Value.String() != "4" || f.Initial.String() != "4" {
		t.Fatalf("value=%q initial=%q want both 4", f.Value.String(), f.Initial.String())
	}
	if f.Description != "Number of spaces." {
		t.Fatalf("description=%q", f.Description)
	}
	if f.Dirty {
		t.Fatal("new field must not start dirty")
	}
}

func TestPasswordFieldStateIsMaskedText(t *testing.T) {
	f := PasswordFieldState("api-key", "API key", "secret", "Stored locally.")
	if f.Type != FieldPassword {
		t.Fatalf("type=%v want FieldPassword", f.Type)
	}
	if f.Value.String() != "secret" {
		t.Fatalf("value=%q", f.Value.String())
	}
}

func TestCycleFieldStateBuildsOptionsFromValues(t *testing.T) {
	f := CycleFieldState("syntax", "Syntax theme", []string{"catppuccin", "dracula"}, "dracula", "Color grammar.")
	if f.Type != FieldCycle {
		t.Fatalf("type=%v want FieldCycle", f.Type)
	}
	if len(f.Options) != 2 || f.Options[0].ID != "catppuccin" || f.Options[1].ID != "dracula" {
		t.Fatalf("options=%+v", f.Options)
	}
	if f.Value.String() != "dracula" {
		t.Fatalf("value=%q want dracula", f.Value.String())
	}
}

func TestBoolFieldStateMapsOnOff(t *testing.T) {
	on := BoolFieldState("word-wrap", "Word wrap", true, "Wrap long lines.")
	off := BoolFieldState("word-wrap", "Word wrap", false, "Wrap long lines.")
	if on.Type != FieldCycle || off.Type != FieldCycle {
		t.Fatalf("bool fields must be FieldCycle, got %v/%v", on.Type, off.Type)
	}
	if on.Value.String() != "on" || off.Value.String() != "off" {
		t.Fatalf("values=%q/%q want on/off", on.Value.String(), off.Value.String())
	}
	if len(on.Options) != 2 || on.Options[0].ID != "on" || on.Options[1].ID != "off" {
		t.Fatalf("options=%+v want [on off]", on.Options)
	}
	if !BoolFieldValue(on) || BoolFieldValue(off) {
		t.Fatal("BoolFieldValue must read back the constructor value")
	}
}

func TestSelectFieldStateKeepsOptions(t *testing.T) {
	options := []FormOption{{ID: "groq", Label: "Groq", Detail: "Fast inference"}}
	f := SelectFieldState("provider", "Provider", "groq", options, "The service used.")
	if f.Type != FieldSelect {
		t.Fatalf("type=%v want FieldSelect", f.Type)
	}
	if len(f.Options) != 1 || f.Options[0].Detail != "Fast inference" {
		t.Fatalf("options=%+v", f.Options)
	}
}

func TestSeparatorFieldStateIsNotFocusable(t *testing.T) {
	f := SeparatorFieldState("connection", "Connection")
	if f.Type != FieldSeparator {
		t.Fatalf("type=%v want FieldSeparator", f.Type)
	}
	if f.Label != "Connection" {
		t.Fatalf("label=%q", f.Label)
	}
}

func TestModelFieldStateUsesFetchPlaceholder(t *testing.T) {
	f := ModelFieldState("model", "Model", "", nil, "Default model.")
	if f.Type != FieldSelect {
		t.Fatalf("type=%v want FieldSelect", f.Type)
	}
	if f.Placeholder != "Press Enter to fetch models" {
		t.Fatalf("placeholder=%q", f.Placeholder)
	}
}

func TestThinkingFieldStateSharesCanonicalLevels(t *testing.T) {
	f := ThinkingFieldState("thinking-effort", "low", "Reasoning budget.")
	if f.Type != FieldCycle {
		t.Fatalf("type=%v want FieldCycle", f.Type)
	}
	if f.Label != "Thinking level" {
		t.Fatalf("label=%q want canonical Thinking level", f.Label)
	}
	if len(f.Options) != len(ThinkingLevels) {
		t.Fatalf("options=%d want %d canonical levels", len(f.Options), len(ThinkingLevels))
	}
	if f.Placeholder != "Provider default" {
		t.Fatalf("placeholder=%q", f.Placeholder)
	}
}

func TestProviderOptionsPreservesPresetMetadata(t *testing.T) {
	options := ProviderOptions([]types.ProviderPreset{{ID: "groq", Name: "Groq", HelpText: "Fast inference"}})
	if len(options) != 1 || options[0].ID != "groq" || options[0].Label != "Groq" || options[0].Detail != "Fast inference" {
		t.Fatalf("options=%+v", options)
	}
}

func TestModelOptionsFallsBackToIDAndFormatsContext(t *testing.T) {
	options := ModelOptions([]types.ModelEntry{
		{ID: "llama-3.3-70b", Name: "Llama 3.3 70B", ContextWindow: 131072},
		{ID: "mystery-model"},
	})
	if len(options) != 2 {
		t.Fatalf("options=%d want 2", len(options))
	}
	if options[0].Label != "Llama 3.3 70B" || options[0].Detail == "" {
		t.Fatalf("option[0]=%+v want name label + context detail", options[0])
	}
	if options[1].Label != "mystery-model" || options[1].Detail != "" {
		t.Fatalf("option[1]=%+v want ID fallback, no detail", options[1])
	}
}
