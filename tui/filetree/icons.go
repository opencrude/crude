// Package filetree provides the file tree sidebar widget.
//
// This file implements the icon system with NerdFont file-type icons
// and Unicode fallback auto-detection. The IconResolver detects
// whether the terminal supports NerdFont glyphs and selects the
// appropriate icon set.
package filetree

import (
	"image/color"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	devicons "github.com/epilande/go-devicons"

	"crude/tui/theme"
)

// ---------------------------------------------------------------------------
// IconResult
// ---------------------------------------------------------------------------

// IconResult holds the resolved icon string and its suggested color.
// The Color is an image/color.Color (typically from lipgloss.Color("…"))
// that callers can pass to lipgloss.NewStyle().Foreground(icon.Color).
type IconResult struct {
	// Icon is the icon character(s): either a NerdFont glyph or
	// a Unicode/ASCII fallback.
	Icon string

	// Color is the suggested color for the icon.
	// It may be nil, in which case the caller should use a default.
	Color color.Color
}

// ---------------------------------------------------------------------------
// IconResolver
// ---------------------------------------------------------------------------

// IconResolver resolves file and directory names to icon strings.
// It supports NerdFont ("nf") icons and a Unicode/ASCII fallback set.
//
// NerdFont detection is automatic by default and can be overridden
// via the UseNerdFont field or the DetectNerdFont function.
type IconResolver struct {
	// UseNerdFont controls whether NerdFont codepoints are emitted.
	// When false, the Unicode fallback set is used instead.
	UseNerdFont bool

	// Glyphs is the expand/collapse glyph table used by the non-NerdFont
	// directory fallback. Defaults to theme.DefaultGlyphs(); themes may
	// override via SetGlyphs.
	Glyphs theme.Glyphs

	// DirColor is the color for directory icons.
	// Defaults to iconColorDir if nil/not set.
	DirColor color.Color

	// FileColor is the color for file icons.
	// Defaults to iconColorFile if nil/not set.
	FileColor color.Color

	// SymlinkColor is the color for symlink icons.
	// Defaults to iconColorSymlink if nil/not set.
	SymlinkColor color.Color
}

// NewIconResolver creates an IconResolver with auto-detection.
// NerdFont support is detected from the terminal environment;
// callers can override by setting r.UseNerdFont after construction.
func NewIconResolver() *IconResolver {
	return &IconResolver{
		UseNerdFont:  DetectNerdFont(),
		Glyphs:       theme.DefaultGlyphs(),
		DirColor:     iconColorDir,
		FileColor:    iconColorFile,
		SymlinkColor: iconColorSymlink,
	}
}

// SetGlyphs overrides the expand/collapse glyph tokens used by the
// non-NerdFont directory fallback. An empty value restores the defaults.
func (r *IconResolver) SetGlyphs(g theme.Glyphs) {
	r.Glyphs = g
}

// expandedGlyph returns the expand/collapse glyph for a directory state.
func (r *IconResolver) expandedGlyph(expanded bool) string {
	g := r.Glyphs
	if g.TreeExpanded == "" {
		g.TreeExpanded = "▾"
	}
	if g.TreeCollapsed == "" {
		g.TreeCollapsed = "▸"
	}
	if expanded {
		return g.TreeExpanded
	}
	return g.TreeCollapsed
}

// ApplyTheme applies the active theme's icon colors and Nerd Font preference.
// An explicit preference wins; otherwise terminal auto-detection remains the
// authority so one workspace setting does not invent platform-specific policy.
func (r *IconResolver) ApplyTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	colors := &th.Tree.Colors
	if colors.IconDir != nil {
		r.DirColor = colors.IconDir
	}
	if colors.IconFile != nil {
		r.FileColor = colors.IconFile
	}
	if colors.IconSymlink != nil {
		r.SymlinkColor = colors.IconSymlink
	}
	if th.Tree.Config.UseNerdFont != nil {
		r.UseNerdFont = *th.Tree.Config.UseNerdFont
	} else {
		r.UseNerdFont = DetectNerdFont()
	}
}

// ResolveIcon returns the IconResult for the given file entry.
//
//	name:      the file or directory name (e.g. "main.go", "Dockerfile")
//	isDir:     true if the entry is a directory
//	isSymlink: true if the entry is a symbolic link
//	isExpanded: for directories, true if the directory is expanded/open
func (r *IconResolver) ResolveIcon(name string, isDir, isSymlink, isExpanded bool) IconResult {
	if isDir {
		return r.dirIcon(isExpanded)
	}
	if isSymlink {
		return r.symlinkIcon()
	}
	return r.fileIcon(name)
}

// dirIcon returns the icon for a directory entry.
func (r *IconResolver) dirIcon(expanded bool) IconResult {
	if r.UseNerdFont {
		if expanded {
			// nf-fa-folder_open
			return IconResult{Icon: "\uf07c", Color: r.DirColor}
		}
		// nf-fa-folder
		return IconResult{Icon: "\uf07b", Color: r.DirColor}
	}
	if expanded {
		return IconResult{Icon: r.expandedGlyph(true), Color: r.DirColor}
	}
	return IconResult{Icon: r.expandedGlyph(false), Color: r.DirColor}
}

// symlinkIcon returns the icon for a symbolic link.
func (r *IconResolver) symlinkIcon() IconResult {
	if r.UseNerdFont {
		// nf-oct-file_symlink_file
		return IconResult{Icon: "\uf481", Color: r.SymlinkColor}
	}
	return IconResult{Icon: "↗", Color: r.SymlinkColor}
}

// fileIcon delegates the filename catalogue to go-devicons (generated from
// nvim-web-devicons), without filesystem I/O or upstream hard-coded colors.
func (r *IconResolver) fileIcon(name string) IconResult {
	if !r.UseNerdFont {
		return IconResult{Icon: fallbackFileIcon, Color: r.FileColor}
	}
	// The upstream catalogue mixes exact names with lowercase names. Prefer the
	// normalized full name (LICENSE.md, Dockerfile) before extension fallback.
	icon := devicons.IconForInfo(iconFileInfo{name: strings.ToLower(name)}).Icon
	if icon == "\uf15b" {
		icon = devicons.IconForInfo(iconFileInfo{name: name}).Icon
	}
	return IconResult{Icon: icon, Color: r.FileColor}
}

// The resolver already has file identity; IconForPath would add a stat to paint.
// Only the basename is read by IconForInfo for these regular-file lookups.
type iconFileInfo struct{ name string }

func (i iconFileInfo) Name() string     { return i.name }
func (iconFileInfo) Size() int64        { return 0 }
func (iconFileInfo) Mode() os.FileMode  { return 0 }
func (iconFileInfo) ModTime() time.Time { return time.Time{} }
func (iconFileInfo) IsDir() bool        { return false }
func (iconFileInfo) Sys() any           { return nil }

// fallbackFileIcon is the Unicode fallback for any file (U+25CF ● BLACK CIRCLE).
// Chosen over the 📄 emoji because emoji may not render in all Windows terminals,
// and is outside the BMP which older terminals cannot display.
const fallbackFileIcon = "●"

// DetectNerdFont checks the terminal environment for NerdFont support.
//
// Detection strategy:
//  1. Check TERM_PROGRAM env var: terminals like "iTerm2", "kitty", "WezTerm",
//     "tmux", "Ghostty", "Warp" typically support NerdFonts with proper config.
//  2. Check TERMINFO/TERM for common NerdFont-aware terminals.
//  3. Check for explicit NerdFont env vars (NERD_FONT, NERD_ICONS, etc.).
//  4. Default to true on modern terminals (macOS, most Linux terminals).
//
// Returns true if NerdFonts are likely supported. The terminal environment is
// fixed for the process, so it is probed once; panels resolve it every paint
// and each environment read allocates on Windows.
func DetectNerdFont() bool { return detectNerdFont() }

var detectNerdFont = sync.OnceValue(probeNerdFont)

func probeNerdFont() bool {
	termProgram := os.Getenv("TERM_PROGRAM")

	// Known NerdFont-friendly terminal programs.
	switch termProgram {
	case "iTerm.app", "iTerm2", "kitty", "WezTerm", "Ghostty",
		"warp", "tmux", "alacritty", "foot", "contour", "rio":
		return true
	}

	// Check for NerdFont-specific environment variables set by some
	// terminal emulators or font configurations.
	if os.Getenv("NERD_FONT") == "1" ||
		os.Getenv("NERD_ICONS") == "1" ||
		os.Getenv("NERDFONT") == "1" {
		return true
	}

	// TERM_PROGRAM_VERSION exists in many modern terminals.
	if termProgram != "" {
		return true
	}

	// On macOS, the default Terminal.app doesn't support NerdFonts,
	// but most popular terminals do. Check if we're on a platform
	// where NerdFont support is the norm.
	if _, ok := os.LookupEnv("ITERM_SESSION_ID"); ok {
		return true
	}
	if _, ok := os.LookupEnv("WEZTERM_PANE"); ok {
		return true
	}
	if _, ok := os.LookupEnv("KITTY_PID"); ok {
		return true
	}
	if _, ok := os.LookupEnv("ALACRITTY_LOG"); ok {
		return true
	}
	if _, ok := os.LookupEnv("FOOT_TERMINFO"); ok {
		return true
	}

	// If TERM is set to something modern, assume NerdFont.
	// On Windows, be conservative; most users don't have NerdFonts installed
	// even if TERM indicates modern capabilities (e.g., Windows Terminal).
	term := os.Getenv("TERM")
	if strings.Contains(term, "truecolor") || strings.Contains(term, "256color") {
		return runtime.GOOS != "windows"
	}

	// On Windows, default to false to avoid rendering NerdFont codepoints
	// that appear as "?" in the terminal.
	if runtime.GOOS == "windows" {
		return false
	}

	// Default to true: most modern terminals support NerdFonts.
	return true
}

// ---------------------------------------------------------------------------
// Icon color constants
// ---------------------------------------------------------------------------

var (
	// iconColorDir is the suggested color for directory icons.
	// Defaults from theme.DefaultDark().Tree; ApplyTheme overrides at runtime.
	iconColorDir color.Color = theme.DefaultDark().Tree.Colors.IconDir

	// iconColorFile is the suggested color for regular file icons.
	iconColorFile color.Color = theme.DefaultDark().Tree.Colors.IconFile

	// iconColorSymlink is the suggested color for symlink icons.
	iconColorSymlink color.Color = theme.DefaultDark().Tree.Colors.IconSymlink
)
