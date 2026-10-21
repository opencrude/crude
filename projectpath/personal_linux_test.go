package projectpath

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func linuxProfile(t *testing.T) (string, string) {
	t.Helper()
	home := mkdir(t, filepath.Join(t.TempDir(), "home"))
	config := mkdir(t, filepath.Join(t.TempDir(), "config"))
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", config)
	return home, filepath.Join(config, "user-dirs.dirs")
}

func TestLinuxRelocatedLocalizedPersonalFolders(t *testing.T) {
	home, config := linuxProfile(t)
	remote := mkdir(t, filepath.Join(t.TempDir(), "Bureau déplacé"))
	write(t, config, `# localized and relocated
XDG_DESKTOP_DIR="`+remote+`"
XDG_DOCUMENTS_DIR="${HOME}/文書"
XDG_DOWNLOAD_DIR="$HOME/Téléchargements" # comments allowed
XDG_MUSIC_DIR="$HOME/Musique"
XDG_PICTURES_DIR="$HOME/Images"
XDG_VIDEOS_DIR="$HOME/Vidéos"
XDG_TEMPLATES_DIR="$HOME"
`)
	assertKind(t, home, KindHome)
	for _, path := range []string{remote, filepath.Join(home, "文書"), filepath.Join(home, "Téléchargements"), filepath.Join(home, "Musique"), filepath.Join(home, "Images"), filepath.Join(home, "Vidéos")} {
		mkdir(t, path)
		assertKind(t, path, KindPersonal)
		assertKind(t, mkdir(t, filepath.Join(path, "child")), "")
	}
	for _, name := range []string{"Desktop", "Documents", "Downloads", "Music", "Pictures", "Videos"} {
		assertKind(t, mkdir(t, filepath.Join(home, name)), "")
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(remote, alias); err != nil {
		t.Fatal(err)
	}
	assertKind(t, alias, KindPersonal)
	homeAlias := filepath.Join(t.TempDir(), "home-alias")
	if err := os.Symlink(home, homeAlias); err != nil {
		t.Fatal(err)
	}
	assertKind(t, homeAlias, KindHome)
}

func TestLinuxAbsentOptionalPersonalFolders(t *testing.T) {
	home, config := linuxProfile(t)
	assertKind(t, mkdir(t, filepath.Join(home, "empty-project")), "")
	assertKind(t, mkdir(t, filepath.Join(home, "Desktop")), KindPersonal)
	write(t, config, `XDG_DOCUMENTS_DIR="$HOME/not-created"`)
	assertKind(t, mkdir(t, filepath.Join(home, "non-git-project")), "")
}

func TestLinuxConfiguredFolderSymlink(t *testing.T) {
	home, config := linuxProfile(t)
	target := mkdir(t, filepath.Join(t.TempDir(), "ActualDocuments"))
	if err := os.Symlink(target, filepath.Join(home, "Documents")); err != nil {
		t.Fatal(err)
	}
	write(t, config, `XDG_DOCUMENTS_DIR="$HOME/Documents"`)
	assertKind(t, target, KindPersonal)
	assertKind(t, filepath.Join(home, "Documents"), KindPersonal)
	assertKind(t, mkdir(t, filepath.Join(target, "project")), "")
}

func TestLinuxXDGInputBoundsAndNoShell(t *testing.T) {
	home, config := linuxProfile(t)
	project := mkdir(t, filepath.Join(home, "project"))
	desktop := mkdir(t, filepath.Join(home, "Desktop"))
	marker := filepath.Join(home, "shell-must-not-run")
	for _, content := range []string{
		strings.Repeat("#", 65537),
		`XDG_DESKTOP_DIR="$(touch ` + marker + `)"`,
		"XDG_DESKTOP_DIR=\"`touch " + marker + "`\"",
		`XDG_DESKTOP_DIR="$UNKNOWN/path"`,
		`XDG_DESKTOP_DIR="relative"`,
		`XDG_DESKTOP_DIR="/unterminated`,
		`XDG_DESKTOP_DIR`,
	} {
		write(t, config, content)
		for path, kind := range map[string]string{project: "", desktop: KindPersonal} {
			got := assertKind(t, path, kind)
			if !strings.Contains(got.Warning, "XDG personal directories") {
				t.Errorf("missing warning for malformed XDG input %q: %+v", content[:min(100, len(content))], got)
			}
		}
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("shell content executed: %v", err)
	}
}

func TestLinuxXDGSpecialFileIsRejected(t *testing.T) {
	home, config := linuxProfile(t)
	if err := unix.Mkfifo(config, 0600); err != nil {
		t.Fatal(err)
	}
	for path, kind := range map[string]string{
		mkdir(t, filepath.Join(home, "project")): "",
		mkdir(t, filepath.Join(home, "Desktop")): KindPersonal,
	} {
		if got := assertKind(t, path, kind); !strings.Contains(got.Warning, "not a regular file") {
			t.Fatalf("FIFO config not reported: %+v", got)
		}
	}
}

func TestLinuxXDGPartialCandidatesAndWarnings(t *testing.T) {
	home, config := linuxProfile(t)
	desktop := mkdir(t, filepath.Join(t.TempDir(), "relocated-desktop"))
	downloads := mkdir(t, filepath.Join(t.TempDir(), "relocated-downloads"))
	write(t, config, `XDG_DESKTOP_DIR="`+desktop+`"
XDG_DOCUMENTS_DIR="$UNKNOWN/documents"
XDG_DOWNLOAD_DIR="`+downloads+`"
XDG_MUSIC_DIR="relative"
`)
	for path, kind := range map[string]string{
		desktop: KindPersonal, downloads: KindPersonal,
		mkdir(t, filepath.Join(home, "Documents")):        KindPersonal,
		mkdir(t, filepath.Join(home, "Music")):            KindPersonal,
		mkdir(t, filepath.Join(home, "Desktop")):          "",
		mkdir(t, filepath.Join(downloads, "project")):     "",
		mkdir(t, filepath.Join(home, "ordinary-project")): "",
	} {
		got := assertKind(t, path, kind)
		if !strings.Contains(got.Warning, "line 2") || !strings.Contains(got.Warning, "line 4") {
			t.Errorf("missing partial-configuration diagnostics: %+v", got)
		}
	}
	// Independent strong classifications must survive broken optional metadata.
	assertKind(t, home, KindHome)
	assertKind(t, string(filepath.Separator), KindRoot)
	if _, err := Inspect(filepath.Join(home, "missing")); err == nil {
		t.Fatal("malformed metadata bypassed directory validation")
	}
	paths, err := personalDirectories(home)
	if err == nil || len(paths) != 8 {
		t.Fatalf("enumeration discarded partial candidates: %v, %v", paths, err)
	}
}

func TestLinuxXDGUnavailableConfigRetainsDefaults(t *testing.T) {
	home, config := linuxProfile(t)
	// An invalid config parent makes opening metadata fail independent of UID or
	// permissions, unlike a chmod-only fixture when tests run as root.
	write(t, config, "not a directory")
	t.Setenv("XDG_CONFIG_HOME", config)
	for path, kind := range map[string]string{
		mkdir(t, filepath.Join(home, "project")):   "",
		mkdir(t, filepath.Join(home, "Documents")): KindPersonal,
	} {
		if got := assertKind(t, path, kind); !strings.Contains(got.Warning, "read XDG personal directories") {
			t.Errorf("unavailable config not reported: %+v", got)
		}
	}
}

func TestLinuxMissingHomeRetainsAbsolutePersonalCandidates(t *testing.T) {
	_, config := linuxProfile(t)
	t.Setenv("HOME", "")
	desktop := mkdir(t, filepath.Join(t.TempDir(), "relocated-desktop"))
	write(t, config, `XDG_DESKTOP_DIR="`+desktop+`"
XDG_DOCUMENTS_DIR="$HOME/Documents"
`)
	for path, kind := range map[string]string{
		desktop: KindPersonal,
		mkdir(t, filepath.Join(desktop, "project")): "",
	} {
		got := assertKind(t, path, kind)
		if !strings.Contains(got.Warning, "locate personal home") || !strings.Contains(got.Warning, "line 2") {
			t.Errorf("missing home or expansion diagnostic: %+v", got)
		}
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	if got := assertKind(t, desktop, ""); !strings.Contains(got.Warning, "locate personal home") {
		t.Errorf("missing home silently ignored: %+v", got)
	}
	assertKind(t, string(filepath.Separator), KindRoot)
}

func TestLinuxXDGQuotedLiteralCharacters(t *testing.T) {
	home, config := linuxProfile(t)
	path := mkdir(t, filepath.Join(home, `dollar$quote"back\tick`+"`"))
	escaped := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`").Replace(path)
	write(t, config, `XDG_DESKTOP_DIR="`+escaped+`"`)
	assertKind(t, path, KindPersonal)
}

func TestLinuxMountedFilesystemRoot(t *testing.T) {
	// proc is a real mount on Linux hosts/containers, independent of the
	// implementation's mount-ID/device checks. Do not mount anything in tests.
	if _, err := os.Stat("/proc/self/mountinfo"); err != nil {
		t.Skip("procfs unavailable")
	}
	assertKind(t, "/proc", KindRoot)
}
