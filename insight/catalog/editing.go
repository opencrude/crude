package catalog

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/dlclark/regexp2"
)

// EditorPattern preserves a VS Code language-configuration regular expression.
// ECMAScript lookahead/backreferences must not be reinterpreted as RE2.
type EditorPattern struct {
	Pattern string `json:"pattern"`
	Flags   string `json:"flags,omitempty"`
}

func (p EditorPattern) Compile() (*regexp2.Regexp, error) {
	if p.Pattern == "" || len(p.Pattern) > 4096 || strings.ContainsAny(p.Pattern, "\r\n\x00") {
		return nil, fmt.Errorf("invalid editor pattern")
	}
	options := regexp2.RegexOptions(regexp2.ECMAScript)
	for _, flag := range p.Flags {
		switch flag {
		case 'i':
			options |= regexp2.IgnoreCase
		case 'm':
			options |= regexp2.Multiline
		case 's':
			options |= regexp2.Singleline
		case 'u': // regexp2 operates on Unicode runes
		default:
			return nil, fmt.Errorf("unsupported editor pattern flag %q", flag)
		}
	}
	re, err := regexp2.Compile(p.Pattern, options)
	if err == nil {
		re.MatchTimeout = 2 * time.Millisecond
	}
	return re, err
}

// LanguageEditing contains only declarative facts from a language configuration.
type LanguageEditing struct {
	Brackets         [][2]string       `json:"brackets"`
	OnEnterRules     []OnEnterRule     `json:"on_enter_rules,omitempty"`
	IndentationRules *IndentationRules `json:"indentation_rules,omitempty"`
}

type OnEnterRule struct {
	BeforeText       EditorPattern  `json:"before_text"`
	AfterText        *EditorPattern `json:"after_text,omitempty"`
	PreviousLineText *EditorPattern `json:"previous_line_text,omitempty"`
	Action           EnterAction    `json:"action"`
}

type EnterAction struct {
	Indent     string `json:"indent"`
	AppendText string `json:"append_text,omitempty"`
	RemoveText int    `json:"remove_text,omitempty"`
}

type IndentationRules struct {
	IncreaseIndentPattern EditorPattern  `json:"increase_indent_pattern"`
	DecreaseIndentPattern EditorPattern  `json:"decrease_indent_pattern"`
	IndentNextLinePattern *EditorPattern `json:"indent_next_line_pattern,omitempty"`
	UnIndentedLinePattern *EditorPattern `json:"un_indented_line_pattern,omitempty"`
}

func validateEditing(v *LanguageEditing) error {
	if v == nil {
		return nil
	}
	if len(v.Brackets) > 32 || len(v.OnEnterRules) > 64 {
		return fmt.Errorf("too many editor rules")
	}
	for _, pair := range v.Brackets {
		for _, token := range pair {
			if token == "" || len(token) > 32 || strings.ContainsAny(token, "\r\n\x00") {
				return fmt.Errorf("invalid editor bracket")
			}
		}
	}
	patterns := []EditorPattern{}
	add := func(p *EditorPattern) {
		if p != nil {
			patterns = append(patterns, *p)
		}
	}
	for _, rule := range v.OnEnterRules {
		patterns = append(patterns, rule.BeforeText)
		add(rule.AfterText)
		add(rule.PreviousLineText)
		switch rule.Action.Indent {
		case "none", "indent", "outdent", "indentOutdent":
		default:
			return fmt.Errorf("invalid Enter action %q", rule.Action.Indent)
		}
		if rule.Action.RemoveText < 0 || rule.Action.RemoveText > 32 || len(rule.Action.AppendText) > 256 || strings.ContainsAny(rule.Action.AppendText, "\r\n\x00") {
			return fmt.Errorf("invalid Enter text")
		}
	}
	if rules := v.IndentationRules; rules != nil {
		patterns = append(patterns, rules.IncreaseIndentPattern, rules.DecreaseIndentPattern)
		add(rules.IndentNextLinePattern)
		add(rules.UnIndentedLinePattern)
	}
	for _, p := range patterns {
		if _, err := p.Compile(); err != nil {
			return err
		}
	}
	return nil
}

func cloneEditing(v *LanguageEditing) *LanguageEditing {
	if v == nil {
		return nil
	}
	out := *v
	out.Brackets = slices.Clone(v.Brackets)
	out.OnEnterRules = slices.Clone(v.OnEnterRules)
	clone := func(p *EditorPattern) *EditorPattern {
		if p == nil {
			return nil
		}
		value := *p
		return &value
	}
	for i := range out.OnEnterRules {
		out.OnEnterRules[i].AfterText = clone(out.OnEnterRules[i].AfterText)
		out.OnEnterRules[i].PreviousLineText = clone(out.OnEnterRules[i].PreviousLineText)
	}
	if v.IndentationRules != nil {
		rules := *v.IndentationRules
		rules.IndentNextLinePattern = clone(rules.IndentNextLinePattern)
		rules.UnIndentedLinePattern = clone(rules.UnIndentedLinePattern)
		out.IndentationRules = &rules
	}
	return &out
}
