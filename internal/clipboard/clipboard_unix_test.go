//go:build !windows

package clipboard

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeTools puts shell scripts named after clipboard tools on PATH. Each
// script appends its name to calls.log, so a test sees which tools ran.
// PATH holds only these scripts, so they use shell builtins.
func fakeTools(t *testing.T, scripts map[string]string) (calls func() []string) {
	t.Helper()
	dir := t.TempDir()
	log := filepath.Join(dir, "calls.log")
	for name, body := range scripts {
		script := "#!/bin/sh\necho " + name + " >> '" + log + "'\n" + body + "\n"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
	for _, name := range []string{"WAYLAND_DISPLAY", "DISPLAY", "TERMUX_VERSION"} {
		t.Setenv(name, "")
	}
	return func() []string {
		data, _ := os.ReadFile(log)
		return strings.Fields(string(data))
	}
}

func requireLinux(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "darwin" {
		t.Skip("macOS always uses its pasteboard")
	}
}

// On KDE Plasma under Wayland, xclip reads XWayland's copy of the
// clipboard, which can still hold an older entry. A Wayland session reads
// only the Wayland clipboard, and its "nothing copied" answer is final.
func TestWaylandSessionNeverPastesXWaylandClipboard(t *testing.T) {
	requireLinux(t)
	for _, test := range []struct {
		name, paste, want string
		readable          bool
	}{
		{"current text", `printf 'current copy'`, "current copy", true},
		{"empty clipboard", `echo "Nothing is copied" >&2; exit 1`, "", true},
		{"image only", `echo 'Clipboard content is not available as requested type "text"' >&2; exit 1`, "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := fakeTools(t, map[string]string{
				"wl-paste": test.paste,
				"xclip":    `printf 'older XWayland entry'`,
			})
			t.Setenv("WAYLAND_DISPLAY", filepath.Join(t.TempDir(), "missing-wayland"))
			t.Setenv("DISPLAY", ":98765")
			got := Read(FmtText)
			if (got != nil) != test.readable || string(got) != test.want {
				t.Fatalf("Read = %q (readable %t), want %q", got, got != nil, test.want)
			}
			for _, call := range calls() {
				if call == "xclip" {
					t.Fatal("a Wayland session read the XWayland clipboard")
				}
			}
		})
	}
}

func TestWaylandSessionWithoutWlClipboardNamesWhatToInstall(t *testing.T) {
	requireLinux(t)
	calls := fakeTools(t, map[string]string{
		"xclip": `while read -r _; do :; done; printf 'older XWayland entry'`,
	})
	t.Setenv("WAYLAND_DISPLAY", filepath.Join(t.TempDir(), "missing-wayland"))
	t.Setenv("DISPLAY", ":98765")
	if got := Read(FmtText); got != nil {
		t.Fatalf("Read = %q; an XWayland read can paste an older entry", got)
	}
	if tool := MissingTool(); tool != "wl-clipboard" {
		t.Fatalf("MissingTool = %q, want wl-clipboard", tool)
	}
	// Writing through XWayland is forwarded to Wayland clients.
	if err := WriteText("copied"); err != nil {
		t.Fatalf("write through XWayland: %v", err)
	}
	if got := calls(); len(got) != 1 || got[0] != "xclip" {
		t.Fatalf("calls = %v, want one xclip write", got)
	}
}

func TestX11SessionReadsXclipAndTreatsAMissingTargetAsEmpty(t *testing.T) {
	requireLinux(t)
	fakeTools(t, map[string]string{
		"xclip":    `echo "Error: target STRING not available" >&2; exit 1`,
		"wl-paste": `printf 'not this session'`,
	})
	t.Setenv("DISPLAY", ":98765")
	if got := Read(FmtText); got == nil || len(got) != 0 {
		t.Fatalf("Read = %q (nil %t), want readable and empty", got, got == nil)
	}
	if tool := MissingTool(); tool != "" {
		t.Fatalf("MissingTool = %q with xclip installed", tool)
	}
}

func TestX11SessionUsesXselWhenXclipIsMissing(t *testing.T) {
	requireLinux(t)
	calls := fakeTools(t, map[string]string{
		"xsel": `if [ "$2" = "--output" ]; then printf 'from xsel'; else while read -r _; do :; done; fi`,
	})
	t.Setenv("DISPLAY", ":98765")
	if got := string(Read(FmtText)); got != "from xsel" {
		t.Fatalf("Read = %q", got)
	}
	if err := WriteText("copied"); err != nil {
		t.Fatal(err)
	}
	if got := calls(); len(got) != 2 {
		t.Fatalf("calls = %v", got)
	}
}

func TestWaylandImageReadTriesEachImageTypeAndNeverXclip(t *testing.T) {
	requireLinux(t)
	calls := fakeTools(t, map[string]string{
		"wl-paste": `case "$3" in image/jpeg) printf 'JPEGDATA';; *) echo 'Clipboard content is not available as requested type' >&2; exit 1;; esac`,
		"xclip":    `printf 'older image'`,
	})
	t.Setenv("WAYLAND_DISPLAY", filepath.Join(t.TempDir(), "missing-wayland"))
	t.Setenv("DISPLAY", ":98765")
	if got := string(Read(FmtImage)); got != "JPEGDATA" {
		t.Fatalf("Read image = %q", got)
	}
	for _, call := range calls() {
		if call == "xclip" {
			t.Fatal("a Wayland image read asked XWayland")
		}
	}
}

func TestNoDesktopSessionLeavesCopyToTheTerminal(t *testing.T) {
	requireLinux(t)
	fakeTools(t, map[string]string{"xclip": `printf 'unrelated'`})
	if got := Read(FmtText); got != nil {
		t.Fatalf("Read = %q without a desktop session", got)
	}
	if err := WriteText("copied"); err == nil {
		t.Fatal("a write without a desktop session must fail so the terminal receives the copy")
	}
	if tool := MissingTool(); tool != "" {
		t.Fatalf("MissingTool = %q without a desktop session", tool)
	}
}
