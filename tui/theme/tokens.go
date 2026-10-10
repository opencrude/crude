package theme

// Glyphs are the shared terminal glyph tokens used by chrome and controls.
// Themes may override them; the factory supplies defaults so product code
// never hard-codes box/prompt/close glyphs when tokens are available.
type Glyphs struct {
	Prompt         string // unused; must stay empty. Never paint ❯.
	Search         string
	Replace        string
	Close          string
	CornerTL       string
	CornerTR       string
	CornerBL       string
	CornerBR       string
	BorderH        string
	BorderV        string
	ScrollbarTrack string
	ScrollbarThumb string
	TreeExpanded   string
	TreeCollapsed  string
	BreadcrumbSep  string
	SwitchOn       string // filled marker of an enabled switch control
	SwitchOff      string // hollow marker of a disabled switch control
	CycleLeft      string // previous-value affordance of a cycle control
	CycleRight     string // next-value affordance of a cycle control
	Modified       string // unsaved-change marker (rail, footer, field labels)
	FocusEdge      string // left edge marking the focused form row or selected card
}

// Typography captures semantic text emphasis that themes may vary without
// changing control structure. Terminal UIs primarily express this via bold /
// italic flags rather than font families.
type Typography struct {
	TitleBold       bool
	LabelBold       bool
	MutedItalic     bool
	PromptBold      bool
	CodeBlockItalic bool
}

// Metrics are layout-affecting control measurements. Changing these must
// invalidate layout; color-only theme changes must not.
type Metrics struct {
	TitleBarHeight  int
	BorderWidth     int
	ScrollbarWidth  int
	MinDialogWidth  int
	MinDialogHeight int
	ButtonPadX      int
	InputMinWidth   int
}

// DefaultGlyphs returns the shared reference glyph table.
func DefaultGlyphs() Glyphs {
	return Glyphs{
		Prompt:         "",
		Search:         "⌕",
		Replace:        "⇄",
		Close:          "✕",
		CornerTL:       "╭",
		CornerTR:       "╮",
		CornerBL:       "╰",
		CornerBR:       "╯",
		BorderH:        "─",
		BorderV:        "│",
		ScrollbarTrack: "│",
		ScrollbarThumb: "┃",
		TreeExpanded:   "▾",
		TreeCollapsed:  "▸",
		BreadcrumbSep:  "▸",
		SwitchOn:       "●",
		SwitchOff:      "○",
		CycleLeft:      "‹",
		CycleRight:     "›",
		Modified:       "●",
		FocusEdge:      "▎",
	}
}

// DefaultTypography returns the shared reference typography table.
func DefaultTypography() Typography {
	return Typography{
		TitleBold:       true,
		LabelBold:       false,
		MutedItalic:     true,
		PromptBold:      false,
		CodeBlockItalic: false,
	}
}

// DefaultMetrics returns the shared reference metric table.
func DefaultMetrics() Metrics {
	return Metrics{
		TitleBarHeight:  1,
		BorderWidth:     1,
		ScrollbarWidth:  1,
		MinDialogWidth:  32,
		MinDialogHeight: 8,
		ButtonPadX:      2,
		InputMinWidth:   10,
	}
}
