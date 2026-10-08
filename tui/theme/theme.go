package theme

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

type Theme struct {
	Name    string
	Colors  Colors
	Spacing Spacing
	Metrics Metrics
	Glyphs  Glyphs
	Type    Typography
	Styles  Styles
	Tree    Tree
}

type Tree struct {
	Colors TreeColors
	Styles TreeStyles
	Config TreeConfig
}

type Colors struct {
	Bg, Fg, Muted, Accent, Accent2     color.Color
	Success, Error, Warning, Border    color.Color
	Selection, SubPaneBg, IncompleteBg color.Color
	// TextSelection is the distinct high-visibility fill for text ranges, not list rows.
	TextSelection, TextSelectionFg color.Color
	// Hover is the background color for interactive elements (file tree
	// rows, tabs, command palette items, settings rows, etc.) when the
	// mouse cursor is over them but they aren't selected. Slightly dimmer
	// than Selection so the keyboard cursor still reads as the strongest
	// emphasis layer. Used uniformly across all hover-capable widgets so
	// the app feels like one coherent GUI rather than a collection of
	// panels that each have their own idea of what "hover" looks like.
	Hover color.Color
	// Popup chrome colors. All popups (context menu, find/replace,
	// save dialog, text prompt, file picker, diagnostic) use a
	// unified design system — see widgets.PopupChromeConfig.
	// - PopupBg: the body fill of every popup (typically == Code.Bg)
	// - PopupBorder: the border color when unfocused
	// - PopupBorderFocused: the border color when the popup has focus
	// - PopupTitle: title text color (unfocused)
	// - PopupTitleFocused: title text color (focused)
	// - PopupDivider: subtle separator line color
	// - PopupSubtleBg: hover/selection background for items in a list
	// - PopupButtonBg: filled button background (primary action)
	// - PopupButtonFg: filled button foreground
	PopupBg, PopupBorder, PopupBorderFocused, PopupTitle, PopupTitleFocused color.Color
	PopupDivider, PopupSubtleBg                                             color.Color
	PopupButtonBg, PopupButtonFg                                            color.Color
	Code                                                                    CodeColors
	Spinner                                                                 SpinnerColors
	Diff                                                                    DiffColors
	Tree                                                                    TreeColors
}

type CodeColors struct {
	Keyword, String, Number, Comment     color.Color
	Function, Type, Operator, Bg, Border color.Color
	Property                             color.Color
}

type SpinnerColors struct {
	Grounding, Planning, Executing, Default, Done, Error color.Color
}

type DiffColors struct {
	File, Hunk, Context, Add, Delete color.Color
	// AddBg/DeleteBg tint the full row width behind additions/deletions so
	// diffs read as full-width colored bands instead of colored glyphs.
	AddBg, DeleteBg color.Color
}

type TreeColors struct {
	DirFg, FileFg, SymlinkFg color.Color
	SelectedBg, SelectedFg   color.Color
	CursorBg, CursorFg       color.Color
	// Hover is the background colour for the row under the mouse
	// cursor. Falls below SelectedBg and CursorBg in priority — see
	// filetree.render.formatLine for the ordering rules.
	Hover                                    color.Color
	IndentLine                               color.Color
	GitModified, GitAdded, GitDeleted        color.Color
	GitUntracked, GitConflicted              color.Color
	IconDir, IconFile, IconSymlink, BorderFg color.Color
	DiagError, DiagWarning, DiagInfo         color.Color
}

type TreeStyles struct {
	DirStyle, FileStyle                lipgloss.Style
	SelectedStyle, CursorStyle         lipgloss.Style
	GitModifiedStyle, GitAddedStyle    lipgloss.Style
	GitDeletedStyle, GitUntrackedStyle lipgloss.Style
}

type TreeConfig struct {
	IndentWidth     int
	ShowIcons       bool
	ShowGitStatus   bool
	ShowDiagnostics bool
	ShowHidden      bool
	DefaultWidth    int
	MinWidth        int
	MaxWidth        int
	UseNerdFont     *bool // nil = auto-detect
}

// Insets is a four-sided padding in cells. Declared in theme (not ui) because
// theme cannot import ui (ui imports theme); ui aliases this type as ui.Insets
// so component code keeps its existing spelling.
type Insets struct {
	Top, Right, Bottom, Left int
}

type Spacing struct {
	PadLR        int
	FieldGap     int
	ButtonGap    int
	DialogPad    int
	FormIndent   int
	LabelColGap  int
	FormTitlePad int
	// Frame is the blank inset around the WHOLE shell, outside every piece
	// of chrome (header, status bar, activity rail, tab bar, content). It is
	// the runtime form of the user-configurable shell frame paddings in
	// Settings → Appearance; the viewmodel folds it into the shell geometry
	// instead of hardcoding the legacy one-row top inset.
	Frame Insets
}

type WindowStyles struct {
	Border          lipgloss.Style
	Title           lipgloss.Style
	TitleFocused    lipgloss.Style
	TitleBg         lipgloss.Style
	TitleBgFocused  lipgloss.Style
	BodyBg          lipgloss.Style
	CloseBtn        lipgloss.Style
	CloseBtnFocused lipgloss.Style
	BorderFocused   lipgloss.Style
}

type Styles struct {
	Header, Separator, DimLabel, AccentLabel            lipgloss.Style
	Body, Button, ButtonFocus, ButtonDim, DialogBg      lipgloss.Style
	CodeBlock, ToolActive, ToolDone, ToolError          lipgloss.Style
	ThinkingLine, UserLabel, AsstLabel, IncompleteLabel lipgloss.Style
	ErrorText, StatusBar, InputPrompt, InputText        lipgloss.Style
	Highlight, DangerText, RetryText, AwaitText         lipgloss.Style
	Window                                              WindowStyles
}

func DefaultDark() *Theme {
	c := Colors{
		Bg:              lipgloss.Color("#0d1117"),
		Fg:              lipgloss.Color("#e6edf3"),
		Muted:           lipgloss.Color("#8b949e"),
		Accent:          lipgloss.Color("#f0883e"),
		Accent2:         lipgloss.Color("#a5d6ff"),
		Success:         lipgloss.Color("#7ee787"),
		Error:           lipgloss.Color("#ff7b72"),
		Warning:         lipgloss.Color("#d29922"),
		Border:          lipgloss.Color("#30363d"),
		Selection:       lipgloss.Color("#1e2a3a"),
		TextSelection:   lipgloss.Color("#707070"),
		TextSelectionFg: lipgloss.Color("#ffffff"),
		Hover:           lipgloss.Color("#21262d"),
		SubPaneBg:       lipgloss.Color("#1c2333"),
		IncompleteBg:    lipgloss.Color("#3a1a1a"),
		// Popup chrome — unified design across all popups.
		// Code.Bg provides the "card on editor" feel used by the
		// diagnostic hover popup. PopupBorder is intentionally
		// dim so the title color carries the visual weight.
		PopupBg:            lipgloss.Color("#161b22"),
		PopupBorder:        lipgloss.Color("#3a4250"),
		PopupBorderFocused: lipgloss.Color("#a5d6ff"),
		PopupTitle:         lipgloss.Color("#8b949e"),
		PopupTitleFocused:  lipgloss.Color("#a5d6ff"),
		PopupDivider:       lipgloss.Color("#2a313c"),
		PopupSubtleBg:      lipgloss.Color("#1e2a3a"),
		PopupButtonBg:      lipgloss.Color("#a5d6ff"),
		PopupButtonFg:      lipgloss.Color("#0d1117"),
		Code: CodeColors{
			Keyword:  lipgloss.Color("#ff7b72"),
			String:   lipgloss.Color("#a5d6ff"),
			Number:   lipgloss.Color("#79c0ff"),
			Comment:  lipgloss.Color("#8b949e"),
			Function: lipgloss.Color("#d2a8ff"),
			Type:     lipgloss.Color("#79c0ff"),
			Operator: lipgloss.Color("#ff7b72"),
			Bg:       lipgloss.Color("#161b22"),
			Border:   lipgloss.Color("#30363d"),
			Property: lipgloss.Color("#79c0ff"),
		},
		Spinner: SpinnerColors{
			Grounding: lipgloss.Color("#f0883e"),
			Planning:  lipgloss.Color("#a5d6ff"),
			Executing: lipgloss.Color("#7ee787"),
			Default:   lipgloss.Color("#8b949e"),
			Done:      lipgloss.Color("#7ee787"),
			Error:     lipgloss.Color("#ff7b72"),
		},
		Diff: DiffColors{
			File:    lipgloss.Color("#a0a8b7"),
			Hunk:    lipgloss.Color("#636d83"),
			Context: lipgloss.Color("#8b949e"),
			Add:     lipgloss.Color("#7ee787"),
			Delete:  lipgloss.Color("#ff7b72"),
			// Subtle dark bands so additions/deletions read as full-width
			// stripes while text stays readable on the default background.
			AddBg:    lipgloss.Color("#12261e"),
			DeleteBg: lipgloss.Color("#2d1517"),
		},
		Tree: TreeColors{
			DirFg:         lipgloss.Color("#79c0ff"),
			FileFg:        lipgloss.Color("#e6edf3"),
			SymlinkFg:     lipgloss.Color("#a0a8b7"),
			SelectedBg:    lipgloss.Color("#264040"),
			SelectedFg:    lipgloss.Color("#e6edf3"),
			CursorBg:      lipgloss.Color("#2d4f4f"),
			CursorFg:      lipgloss.Color("#e6edf3"),
			Hover:         lipgloss.Color("#21262d"),
			IndentLine:    lipgloss.Color("#30363d"),
			GitModified:   lipgloss.Color("#d29922"),
			GitAdded:      lipgloss.Color("#7ee787"),
			GitDeleted:    lipgloss.Color("#ff7b72"),
			GitUntracked:  lipgloss.Color("#636d83"),
			GitConflicted: lipgloss.Color("#ff7b72"),
			IconDir:       lipgloss.Color("#f0883e"),
			IconFile:      lipgloss.Color("#8b949e"),
			IconSymlink:   lipgloss.Color("#79c0ff"),
			BorderFg:      lipgloss.Color("#30363d"),
			DiagError:     lipgloss.Color("#ff7b72"),
			DiagWarning:   lipgloss.Color("#d29922"),
			DiagInfo:      lipgloss.Color("#79c0ff"),
		},
	}

	return named("dark", newTheme(c))
}

func named(name string, th *Theme) *Theme {
	th.Name = name
	return th
}
