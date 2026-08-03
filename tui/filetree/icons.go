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

	"crude/tui/theme"
)

// ---------------------------------------------------------------------------
// IconResult
// ---------------------------------------------------------------------------

// IconResult holds the resolved icon string and its suggested color.
// The Color is an image/color.Color (typically from lipgloss.Color("…"))
// that callers can pass to lipgloss.NewStyle().Foreground(icon.Color).
type IconResult struct {
	// Icon is the icon character(s) — either a NerdFont glyph or
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

// ApplyTheme applies theme colors to the IconResolver.
// Non-nil icon colors from the theme override the current resolver colors.
func (r *IconResolver) ApplyTheme(colors *theme.TreeColors) {
	if colors == nil {
		return
	}
	if colors.IconDir != nil {
		r.DirColor = colors.IconDir
	}
	if colors.IconFile != nil {
		r.FileColor = colors.IconFile
	}
	if colors.IconSymlink != nil {
		r.SymlinkColor = colors.IconSymlink
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
			//  (nf-md-folder_open)
			return IconResult{Icon: "\uf07c", Color: r.DirColor}
		}
		//  (nf-md-folder)
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
		//  (nf-oct-file_symlink_file)
		return IconResult{Icon: "\uf499", Color: r.SymlinkColor}
	}
	return IconResult{Icon: "↗", Color: r.SymlinkColor}
}

// fileIcon returns the icon for a regular file based on its name.
func (r *IconResolver) fileIcon(name string) IconResult {
	// Check exact-name matches first (Dockerfile, Makefile, README, etc.)
	if result, ok := exactNameIcons[name]; ok {
		icon := result
		if !r.UseNerdFont {
			icon = fallbackFileIcon
		}
		return IconResult{Icon: icon, Color: r.FileColor}
	}

	// Check exact-name prefix patterns (docker-compose*.yml, go.mod, etc.).
	// This handles cases like "docker-compose.yml", "go.mod" where the name
	// itself (not just the extension) carries meaning.
	if result, ok := matchExactNamePrefix(name); ok {
		icon := result
		if !r.UseNerdFont {
			icon = fallbackFileIcon
		}
		return IconResult{Icon: icon, Color: r.FileColor}
	}

	// Check by extension.
	ext := strings.ToLower(fileExtension(name))
	if result, ok := extensionIcons[ext]; ok {
		icon := result
		if !r.UseNerdFont {
			icon = fallbackFileIcon
		}
		return IconResult{Icon: icon, Color: r.FileColor}
	}

	// Check special filenames that start with a dot (.gitignore, .env, etc.).
	if result, ok := dotfileNameIcons[name]; ok {
		icon := result
		if !r.UseNerdFont {
			icon = fallbackFileIcon
		}
		return IconResult{Icon: icon, Color: r.FileColor}
	}

	// Default file icon.
	if r.UseNerdFont {
		//  (nf-fa-file)
		return IconResult{Icon: "\uf15b", Color: r.FileColor}
	}
	return IconResult{Icon: fallbackFileIcon, Color: r.FileColor}
}

// fallbackFileIcon is the Unicode fallback for any file (U+25CF ● BLACK CIRCLE).
// Chosen over the 📄 emoji because emoji may not render in all Windows terminals,
// and is outside the BMP which older terminals cannot display.
const fallbackFileIcon = "●"

// DetectNerdFont checks the terminal environment for NerdFont support.
//
// Detection strategy:
//  1. Check TERM_PROGRAM env var — terminals like "iTerm2", "kitty", "WezTerm",
//     "tmux", "Ghostty", "Warp" typically support NerdFonts with proper config.
//  2. Check TERMINFO/TERM for common NerdFont-aware terminals.
//  3. Check for explicit NerdFont env vars (NERD_FONT, NERD_ICONS, etc.).
//  4. Default to true on modern terminals (macOS, most Linux terminals).
//
// Returns true if NerdFonts are likely supported.
func DetectNerdFont() bool {
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
	// On Windows, be conservative — most users don't have NerdFonts installed
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

	// Default to true — most modern terminals support NerdFonts.
	return true
}

// ---------------------------------------------------------------------------
// Icon color constants
// ---------------------------------------------------------------------------

var (
	// iconColorDir is the suggested color for directory icons.
	// Defaults from theme.DefaultDark().Tree — ApplyTheme overrides at runtime.
	iconColorDir color.Color = theme.DefaultDark().Tree.Colors.IconDir

	// iconColorFile is the suggested color for regular file icons.
	iconColorFile color.Color = theme.DefaultDark().Tree.Colors.IconFile

	// iconColorSymlink is the suggested color for symlink icons.
	iconColorSymlink color.Color = theme.DefaultDark().Tree.Colors.IconSymlink
)

// ---------------------------------------------------------------------------
// NerdFont icon maps
// ---------------------------------------------------------------------------

// exactNameIcons maps exact filenames to their NerdFont codepoints.
// These are checked before extension-based lookups.
var exactNameIcons = map[string]string{
	// Container/runtime files.
	"Dockerfile":      "\uf308",
	"Dockerfile.dev":  "\uf308",
	"Dockerfile.prod": "\uf308",
	"Dockerfile.test": "\uf308",
	"Makefile":        "\ue771",
	"makefile":        "\ue771",
	"GNUmakefile":     "\ue771",
	"Rakefile":        "\ue791",
	"Justfile":        "\ue771",
	"Taskfile.yml":    "\ue771",
	"Taskfile.yaml":   "\ue771",

	// License and documentation.
	"LICENSE":         "\uf09d",
	"LICENSE.md":      "\uf09d",
	"LICENSE.txt":     "\uf09d",
	"LICENSE_MIT":     "\uf09d",
	"LICENSE_APACHE":  "\uf09d",
	"LICENSE_GPL":     "\uf09d",
	"README":          "\uf48a",
	"README.md":       "\uf48a",
	"README.rst":      "\uf48a",
	"README.txt":      "\uf48a",
	"README.org":      "\uf48a",
	"CHANGELOG":       "\uf48a",
	"CHANGELOG.md":    "\uf48a",
	"CONTRIBUTING.md": "\uf48a",

	// Go module files.
	"go.mod": "\ue627",
	"go.sum": "\ue627",

	// Node.js files.
	"package.json":      "\ue718",
	"package-lock.json": "\uf023",
	"yarn.lock":         "\uf023",
	"pnpm-lock.yaml":    "\uf023",
	".npmrc":            "\ue718",
	".yarnrc":           "\ue718",
	"tsconfig.json":     "\ue628",
	".eslintrc":         "\ue74e",
	".eslintrc.json":    "\ue74e",
	".eslintrc.js":      "\ue74e",
	".prettierrc":       "\ue74e",
	".babelrc":          "\ue74e",

	// Rust files.
	"Cargo.toml":          "\ue7a8",
	"Cargo.lock":          "\uf023",
	"rust-toolchain.toml": "\ue7a8",
	"rust-toolchain":      "\ue7a8",
	"clippy.toml":         "\ue7a8",

	// Python files.
	"setup.py":         "\ue73c",
	"setup.cfg":        "\ue73c",
	"pyproject.toml":   "\ue73c",
	"requirements.txt": "\ue73c",
	"Pipfile":          "\ue73c",
	"Pipfile.lock":     "\uf023",
	"tox.ini":          "\ue73c",

	// Ruby files.
	"Gemfile":      "\ue791",
	"Gemfile.lock": "\uf023",

	// PHP files.
	"composer.json": "\ue73d",
	"composer.lock": "\uf023",

	// Shell/CI files.
	".shellcheckrc":  "\ue795",
	".gitattributes": "\uf402",
	".editorconfig":  "\uf402",
}

// matchExactNamePrefix handles names that have a dot-variant or suffix,
// like "docker-compose.yml", "go.mod", etc. that are not matched by
// exactNameIcons above. Returns the NerdFont icon and true if matched.
func matchExactNamePrefix(name string) (string, bool) {
	nameLower := strings.ToLower(name)
	switch {
	case strings.HasPrefix(nameLower, "docker-compose"):
		return "\uf308", true
	case nameLower == "cargo.toml":
		return "\ue7a8", true
	case strings.HasPrefix(nameLower, "go.mod"):
		return "\ue627", true
	case strings.HasPrefix(nameLower, "go.sum"):
		return "\ue627", true
	default:
		return "", false
	}
}

// extensionIcons maps lowercase file extensions to their NerdFont codepoints.
var extensionIcons = map[string]string{
	// Go
	".go": "\ue627",

	// Rust
	".rs": "\ue7a8",

	// Python
	".py":    "\ue73c",
	".pyc":   "\ue73c",
	".pyo":   "\ue73c",
	".pyd":   "\ue73c",
	".ipynb": "\ue73c",

	// JavaScript / TypeScript
	".js":     "\ue74e",
	".mjs":    "\ue74e",
	".cjs":    "\ue74e",
	".jsx":    "\ue7ba",
	".ts":     "\ue628",
	".tsx":    "\ue7ba",
	".d.ts":   "\ue628",
	".vue":    "\ue7ba",
	".svelte": "\ue7ba",

	// Java
	".java":  "\ue738",
	".class": "\ue738",
	".jar":   "\ue738",

	// C / C++
	".c":   "\ue618",
	".cpp": "\ue61d",
	".cc":  "\ue61d",
	".cxx": "\ue61d",
	".h":   "\ue618",
	".hpp": "\ue61d",
	".hxx": "\ue61d",

	// Ruby
	".rb":  "\ue791",
	".erb": "\ue791",
	".rbs": "\ue791",

	// PHP
	".php":   "\ue73d",
	".phtml": "\ue73d",
	".php3":  "\ue73d",
	".php4":  "\ue73d",
	".php5":  "\ue73d",

	// Swift
	".swift": "\ue755",

	// Kotlin
	".kt":  "\ue634",
	".ktm": "\ue634",
	".kts": "\ue634",

	// Scala
	".scala": "\ue737",
	".sc":    "\ue737",

	// Lua
	".lua": "\ue624",

	// Shell
	".sh":   "\ue795",
	".bash": "\ue795",
	".zsh":  "\ue795",
	".fish": "\ue795",
	".ksh":  "\ue795",

	// SQL
	".sql": "\ue706",

	// Config / data files
	".json": "\ue602",
	".yaml": "\ue602",
	".yml":  "\ue602",
	".toml": "\ue602",
	".xml":  "\ue602",
	".csv":  "\ue64a",
	".tsv":  "\ue64a",

	// Documentation
	".md":       "\ue73e",
	".markdown": "\ue73e",
	".rst":      "\ue73e",
	".org":      "\ue73e",
	".txt":      "\uf0c9",

	// Lock files
	".lock": "\uf023",

	// Images
	".png":  "\uf1c5",
	".jpg":  "\uf1c5",
	".jpeg": "\uf1c5",
	".gif":  "\uf1c5",
	".svg":  "\uf1c5",
	".ico":  "\uf1c5",
	".bmp":  "\uf1c5",
	".webp": "\uf1c5",
	".avif": "\uf1c5",

	// Archives
	".zip": "\uf410",
	".tar": "\uf410",
	".gz":  "\uf410",
	".bz2": "\uf410",
	".xz":  "\uf410",
	".rar": "\uf410",
	".7z":  "\uf410",
	".tgz": "\uf410",
	".zst": "\uf410",

	// Binaries
	".exe":      "\uf471",
	".bin":      "\uf471",
	".so":       "\uf471",
	".dylib":    "\uf471",
	".wasm":     "\uf471",
	".appimage": "\uf471",
	".deb":      "\uf471",
	".rpm":      "\uf471",
}

// dotfileNameIcons maps dotfiles (like .gitignore, .env) to NerdFont codepoints.
var dotfileNameIcons = map[string]string{
	".gitignore":     "\uf402",
	".gitkeep":       "\uf402",
	".gitattributes": "\uf402",
	".gitmodules":    "\uf402",
	".gitconfig":     "\uf402",

	".env":         "\uf462",
	".env.example": "\uf462",
	".env.local":   "\uf462",
	".env.dev":     "\uf462",
	".env.prod":    "\uf462",
	".env.test":    "\uf462",

	".dockerignore":   "\uf308",
	".editorconfig":   "\uf402",
	".prettierrc":     "\ue74e",
	".eslintrc":       "\ue74e",
	".eslintignore":   "\ue74e",
	".prettierignore": "\ue74e",
	".stylelintrc":    "\ue74e",
	".shellcheckrc":   "\ue795",
	".npmrc":          "\ue718",
	".yarnrc":         "\ue718",
	".nvmrc":          "\ue718",
	".node-version":   "\ue718",

	".rustfmt.toml": "\ue7a8",
	".clippy.toml":  "\ue7a8",

	".python-version": "\ue73c",
	".flake8":         "\ue73c",
	".pylintrc":       "\ue73c",
}

// fileExtension returns the file extension from the given name,
// including the leading dot. For names like ".gitignore" or "Makefile"
// without a conventional extension, it returns the empty string.
func fileExtension(name string) string {
	// Skip dotfiles (names starting with a dot but not a dotfile like "..").
	if strings.HasPrefix(name, ".") {
		// Check if there's a second dot suggesting an extension to a dotfile.
		// e.g., ".env.local" → the "extension" part after the second dot
		// is ".local". For ".gitignore" there's no conventional extension.
		if idx := strings.LastIndex(name[1:], "."); idx >= 0 {
			return name[1:][idx:] // return ".local" from ".env.local"
		}
		return "" // ".gitignore" → no extension
	}
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[idx:]
	}
	return ""
}
