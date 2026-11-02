package toolstore

import (
	"archive/tar"
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallRejectsUnsafeArchives(t *testing.T) {
	bin := exe("bin/tool", "tool")
	zeros := strings.Repeat("\x00", 2<<20)
	many := []entry{bin}
	for i := range 20 {
		many = append(many, file("data/"+strings.Repeat("f", i+1), "x"))
	}
	cases := []struct {
		name, archive string
		data          func(t *testing.T) []byte
		maxEntries    int
		want          string
	}{
		{"zip slip", ArchiveZip, func(t *testing.T) []byte {
			return zipBytes(t, []entry{bin, file("../../evil.txt", "evil")})
		}, 0, "outside the tool folder"},
		{"zip absolute path", ArchiveZip, func(t *testing.T) []byte {
			return zipBytes(t, []entry{bin, file("/tmp/evil.txt", "evil")})
		}, 0, "is absolute"},
		{"zip backslash path", ArchiveZip, func(t *testing.T) []byte {
			return zipBytes(t, []entry{bin, file(`..\evil.txt`, "evil")})
		}, 0, "not permitted"},
		{"zip link escape", ArchiveZip, func(t *testing.T) []byte {
			return zipBytes(t, []entry{bin, symlink("lnk", "../../../outside")})
		}, 0, "outside the tool folder"},
		{"zip bomb", ArchiveZip, func(t *testing.T) []byte {
			return zipBytes(t, []entry{bin, file("zeros.bin", zeros)})
		}, 0, "expands past its budget"},
		{"zip entry count", ArchiveZip, func(t *testing.T) []byte { return zipBytes(t, many) }, 10, "expands past its budget"},
		{"tar slip", ArchiveTarGz, func(t *testing.T) []byte {
			return gzBytes(t, tarBytes(t, []entry{bin, file("a/../../evil.txt", "evil")}))
		}, 0, "outside the tool folder"},
		{"tar absolute link", ArchiveTarGz, func(t *testing.T) []byte {
			return gzBytes(t, tarBytes(t, []entry{bin, symlink("passwd", "/etc/passwd")}))
		}, 0, "absolute or empty target"},
		{"tar link escape", ArchiveTarGz, func(t *testing.T) []byte {
			return gzBytes(t, tarBytes(t, []entry{bin, symlink("deep/lnk", "../../outside")}))
		}, 0, "outside the tool folder"},
		{"tar link chain escape", ArchiveTarGz, func(t *testing.T) []byte {
			// Each target is inside the tree when read as text, but
			// s/u resolves to the root, so e resolves above it.
			return gzBytes(t, tarBytes(t, []entry{bin, dir("s/"), symlink("s/u", ".."), symlink("e", "s/u/../..")}))
		}, 0, "outside the tool folder"},
		{"tar write through link", ArchiveTarGz, func(t *testing.T) []byte {
			return gzBytes(t, tarBytes(t, []entry{bin, dir("real/"), symlink("lnk", "real"), file("lnk/x", "x")}))
		}, 0, "both a link and a folder"},
		{"tar character device", ArchiveTarGz, func(t *testing.T) []byte {
			return gzBytes(t, tarBytes(t, []entry{bin, {name: "dev/null", mode: 0o666, typ: tar.TypeChar}}))
		}, 0, "device, pipe or other special file"},
		{"tar fifo", ArchiveTarGz, func(t *testing.T) []byte {
			return gzBytes(t, tarBytes(t, []entry{bin, {name: "pipe", mode: 0o666, typ: tar.TypeFifo}}))
		}, 0, "device, pipe or other special file"},
		{"tar duplicate file", ArchiveTarGz, func(t *testing.T) []byte {
			return gzBytes(t, tarBytes(t, []entry{bin, file("a.txt", "1"), file("a.txt", "2")}))
		}, 0, "more than once"},
		{"tar hard link to missing file", ArchiveTarGz, func(t *testing.T) []byte {
			return gzBytes(t, tarBytes(t, []entry{bin, {name: "copy", typ: tar.TypeLink, link: "missing"}}))
		}, 0, "earlier file"},
		{"tar bomb", ArchiveTarGz, func(t *testing.T) []byte {
			return gzBytes(t, tarBytes(t, []entry{bin, file("zeros.bin", zeros)}))
		}, 0, "expands past its budget"},
		{"tar.xz bomb", ArchiveTarXz, func(t *testing.T) []byte {
			return xzBytes(t, tarBytes(t, []entry{bin, file("zeros.bin", zeros)}))
		}, 0, "expands past its budget"},
		{"tar entry count", ArchiveTarGz, func(t *testing.T) []byte { return gzBytes(t, tarBytes(t, many)) }, 10, "expands past its budget"},
		{"gz bomb", ArchiveGz, func(t *testing.T) []byte { return gzBytes(t, []byte(zeros)) }, 0, "expands past its budget"},
		{"missing executable", ArchiveTarGz, func(t *testing.T) []byte {
			return gzBytes(t, tarBytes(t, []entry{file("bin/other", "x")}))
		}, 0, "no regular file"},
		{"executable is a folder", ArchiveTarGz, func(t *testing.T) []byte {
			return gzBytes(t, tarBytes(t, []entry{dir("bin/tool/")}))
		}, 0, "no regular file"},
		{"executable is a link", ArchiveTarGz, func(t *testing.T) []byte {
			return gzBytes(t, tarBytes(t, []entry{file("real", "x"), symlink("bin/tool", "../real")}))
		}, 0, "no regular file"},
		{"not an archive", ArchiveZip, func(t *testing.T) []byte { return []byte("plain text") }, 0, "not a valid zip"},
		{"not xz", ArchiveTarXz, func(t *testing.T) []byte { return []byte("plain text") }, 0, "not xz data"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newStore(t)
			server := serve(t, map[string][]byte{"/a": tc.data(t)})
			r := request(server.URL+"/a", tc.archive, "bin/tool")
			if tc.archive == ArchiveGz {
				r.Bin = "tool"
			}
			r.MaxExtractedBytes = 64 << 10
			r.MaxDownloadBytes = 4 << 20
			r.MaxEntries = tc.maxEntries
			_, err := store.Install(context.Background(), r)
			requireErrorContains(t, err, tc.want)
			if tools, _, _ := store.List(); len(tools) != 0 {
				t.Fatalf("an unsafe archive was installed: %+v", tools)
			}
			requireEmptyStaging(t, store)
			// Nothing may appear next to the store root.
			siblings, err := os.ReadDir(filepath.Dir(store.root))
			if err != nil || len(siblings) != 1 {
				t.Fatalf("entries next to the store root = %v, %v", siblings, err)
			}
			history := events(t, store)
			if len(history) != 1 || history[0].Kind != EventInstallFailed {
				t.Fatalf("history = %+v", history)
			}
		})
	}
}

func TestInstallKeepsContainedLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating links on Windows needs a privilege that tests do not have")
	}
	data := gzBytes(t, tarBytes(t, []entry{
		{typ: tar.TypeXGlobalHeader, body: "commit"},
		exe("./pkg/bin/tool", "tool"),
		file("pkg/lib/libdemo.so.1", "library"),
		symlink("pkg/lib/libdemo.so", "libdemo.so.1"),
		symlink("pkg/bin/lib", "../lib"),
		{name: "pkg/lib/copy.so", typ: tar.TypeLink, link: "pkg/lib/libdemo.so.1"},
	}))
	store := newStore(t)
	server := serve(t, map[string][]byte{"/a": data})
	tool, err := store.Install(context.Background(), request(server.URL+"/a", ArchiveTarGz, "pkg/bin/tool"))
	if err != nil {
		t.Fatal(err)
	}
	content := filepath.Join(tool.Dir, contentDir, "pkg")
	if got := readFile(t, filepath.Join(content, "bin", "lib", "libdemo.so")); got != "library" {
		t.Fatalf("library through links = %q", got)
	}
	if got := readFile(t, filepath.Join(content, "lib", "copy.so")); got != "library" {
		t.Fatalf("hard link copy = %q", got)
	}
	if tool.Files != 3 || tool.Size != int64(len("tool")+2*len("library")) {
		t.Fatalf("tool counts = %d files, %d bytes", tool.Files, tool.Size)
	}
	// Removal follows no link and deletes only the tool folder.
	preview, err := store.PreviewRemove([]string{tool.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Remove(context.Background(), preview); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(tool.Dir); !isNotExist(err) {
		t.Fatalf("tool folder still exists: %v", err)
	}
}

func TestCleanEntryName(t *testing.T) {
	valid := map[string]string{"./a/b": "a/b", "a/": "a", "./": "", "a/b.c": "a/b.c"}
	for input, want := range valid {
		if got, err := cleanEntryName(input); err != nil || got != want {
			t.Errorf("cleanEntryName(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	for _, input := range []string{"../a", "a/../../b", "/a", "a//b", "a/./b", `a\b`, "c:/a", "a\x00b", "a/..", "a\nb"} {
		if _, err := cleanEntryName(input); err == nil {
			t.Errorf("cleanEntryName(%q) accepted an unsafe path", input)
		}
	}
}
