// Package indentation defines source-independent editor indentation choices.
// It contains no language rules, file writer or terminal-width implementation.
package indentation

import "fmt"

const (
	Spaces   = "space"
	Tabs     = "tab"
	MaxWidth = 32
)

// Choices is a sparse layer. Zero/empty inherits. SizeIsTab is EditorConfig's
// indent_size=tab and is resolved only after the complete tab-width precedence.
type Choices struct {
	Style     string `json:"style,omitempty"`
	Size      int    `json:"size,omitempty"`
	TabWidth  int    `json:"tab_width,omitempty"`
	SizeIsTab bool   `json:"size_is_tab,omitempty"`
}

func (c Choices) Validate() error {
	if c.Style != "" && c.Style != Spaces && c.Style != Tabs {
		return fmt.Errorf("choose tabs, spaces or inherited indentation")
	}
	if c.Size < 0 || c.Size > MaxWidth || c.TabWidth < 0 || c.TabWidth > MaxWidth || c.SizeIsTab && c.Size != 0 {
		return fmt.Errorf("indent and tab widths must be 1 to 32, or 0 to inherit")
	}
	return nil
}
func Overlay(low, high Choices) Choices {
	if high.Style != "" {
		low.Style = high.Style
	}
	if high.Size > 0 || high.SizeIsTab {
		low.Size, low.SizeIsTab = high.Size, high.SizeIsTab
	}
	if high.TabWidth > 0 {
		low.TabWidth = high.TabWidth
	}
	return low
}

// Resolve keeps indentation width distinct from the width of a tab character.
func Resolve(detected, settings, project, file Choices) Choices {
	value := Overlay(Overlay(Overlay(detected, settings), project), file)
	if value.TabWidth == 0 {
		value.TabWidth = 4
	}
	if value.SizeIsTab || value.Size == 0 {
		value.Size = value.TabWidth
	}
	value.SizeIsTab = false
	if value.Style == "" {
		value.Style = Spaces
	}
	return value
}
func (c Choices) Label() string {
	kind := "Spaces"
	if c.Style == Tabs {
		kind = "Tabs"
	}
	return fmt.Sprintf("%s: %d", kind, c.Size)
}
