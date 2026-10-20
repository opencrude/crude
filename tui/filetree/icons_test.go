package filetree

import (
	"path/filepath"
	"testing"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

// Expected glyphs are Nerd Fonts 3 names/codepoints, not values read back from
// the catalogue under test. These used to be a play arrow, generic file, Julia,
// EJS, React and a credit card, respectively.
func TestFileIconsUseNerdFontFileTypes(t *testing.T) {
	r := NewIconResolver()
	r.UseNerdFont = true
	for name, want := range map[string]string{
		"config.json": "\ue60b", "settings.JSON": "\ue60b", "config.jsonc": "\ue60b",
		"install.ps1": "\U000f0a0a", "module.psm1": "\U000f0a0a", "module.PSD1": "\U000f0a0a",
		"main.lua": "\ue620", "main.c": "\ue61e", "app.vue": "\ue6a0", "app.svelte": "\ue697",
		"style.css": "\ue749", "LICENSE": "\ue60a", "Dockerfile": "\U000f0868",
		"Makefile": "\ue779", "go.mod": "\ue627", "unknown.abcdef": "\uf15b",
	} {
		t.Run(name, func(t *testing.T) {
			got := r.ResolveIcon(name, false, false, false)
			if got.Icon != want || got.Color != r.FileColor {
				t.Fatalf("%s icon=%U, want %U with theme file color", name, []rune(got.Icon), []rune(want))
			}
			r.UseNerdFont = false
			if fallback := r.ResolveIcon(name, false, false, false).Icon; fallback != fallbackFileIcon {
				t.Fatalf("disabled Nerd Font emitted %q", fallback)
			}
			r.UseNerdFont = true
		})
	}
	if got := r.ResolveIcon("link.json", false, true, false).Icon; got != "\uf481" {
		t.Fatalf("symlink is not nf-oct-file_symlink_file: %q", got)
	}
}

func TestTreePaintsSupplementaryPlanePowerShellIcon(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "install.ps1")
	createFileForTest(t, path, "Write-Host 'hello'\n")
	model := NewFileTreeModel(dir)
	node := model.NodeByPath(path)
	if node == nil {
		t.Fatal("PowerShell file missing from tree")
	}
	icons := NewIconResolver()
	icons.UseNerdFont = true
	buf := surface.NewCellBuffer(40, 1)
	painter := NewTreeRowPainter(model, icons, PaintConfig{IndentWidth: 2, ShowIcons: true})
	painter(buf, 0, 0, 0, 40, ui.TreeItem{ID: path, Label: node.Name, Depth: node.Depth, Payload: node}, ui.TreeRowState{}, ui.NewTokenSet(theme.DefaultDark()))
	for x := 0; x < buf.Width(); x++ {
		if buf.Get(x, 0).Char == '\U000f0a0a' {
			return
		}
	}
	t.Fatal("file tree did not paint the actual PowerShell glyph")
}
