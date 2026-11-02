package catalog

import (
	"strings"
	"testing"
)

func downloadPackage(id string, asset Asset, bin string) Package {
	return Package{Name: "fixture-tool", Licenses: []string{"MIT"}, Source: Source{ID: id, Assets: []Asset{asset}},
		Bin: map[string]string{"fixture-tool": bin}}
}

func TestDownloadForResolvesReleaseFiles(t *testing.T) {
	linux := targetsFor("linux", "amd64")
	for _, tc := range []struct {
		name, id, file, assetBin, bin  string
		archive, wantBin, wantURL, why string
	}{
		{name: "tar.gz", id: "pkg:github/owner/repo@v1.2.0", file: "tool-{{version}}.tar.gz", assetBin: "tool-{{version}}/bin/fixture-tool", bin: "{{source.asset.bin}}",
			archive: "tar.gz", wantBin: "tool-v1.2.0/bin/fixture-tool", wantURL: "https://github.com/owner/repo/releases/download/v1.2.0/tool-v1.2.0.tar.gz"},
		{name: "strip prefix", id: "pkg:github/owner/repo@v1.2.0", file: `tool-{{ version | strip_prefix "v" }}.zip`, assetBin: "fixture-tool", bin: "{{source.asset.bin}}",
			archive: "zip", wantBin: "fixture-tool", wantURL: "https://github.com/owner/repo/releases/download/v1.2.0/tool-1.2.0.zip"},
		{name: "raw file", id: "pkg:github/owner/repo@2026-01-01", file: "fixture-tool-linux", bin: "{{source.asset.file}}",
			archive: "raw", wantBin: "fixture-tool-linux"},
		{name: "gz file", id: "pkg:github/owner/repo@1", file: "fixture-tool-linux.gz", bin: "{{source.asset.bin}}",
			archive: "gz", wantBin: "fixture-tool-linux"},
		{name: "archive member", id: "pkg:github/owner/repo@1", file: "tool.zip:libexec/", assetBin: "bin/x", bin: "{{source.asset.bin}}", why: "cannot unpack"},
		{name: "bzip2", id: "pkg:github/owner/repo@1", file: "tool.tar.bz2", assetBin: "x", bin: "{{source.asset.bin}}", why: "cannot unpack"},
		{name: "package manager", id: "pkg:npm/fixture-tool@1.0.0", file: "x", bin: "{{source.asset.bin}}", why: "language package manager"},
		{name: "build template", id: "pkg:github/owner/repo@1", file: "tool.zip", assetBin: "x", bin: "{{source.build.x}}", why: "does not support"},
		{name: "escaping path", id: "pkg:github/owner/repo@1", file: "tool.zip", assetBin: "../x", bin: "{{source.asset.bin}}", why: "no safe program path"},
		{name: "no exact release", id: "pkg:github/owner/repo", file: "tool.zip", assetBin: "x", bin: "{{source.asset.bin}}", why: "no exact release"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := downloadPackage(tc.id, Asset{Targets: []string{"linux_x64_gnu"}, File: tc.file, Bin: tc.assetBin}, tc.bin)
			got, why := p.DownloadFor("fixture-tool", linux)
			if tc.why != "" {
				if !strings.Contains(why, tc.why) || got.URL != "" {
					t.Fatalf("download = %+v, reason %q; want reason containing %q", got, why, tc.why)
				}
				return
			}
			if why != "" || got.Archive != tc.archive || got.Bin != tc.wantBin || (tc.wantURL != "" && got.URL != tc.wantURL) ||
				got.Source != "github.com/owner/repo" || got.Licenses[0] != "MIT" {
				t.Fatalf("download = %+v, reason %q", got, why)
			}
		})
	}
	p := downloadPackage("pkg:github/owner/repo@1", Asset{Targets: []string{"darwin_arm64"}, File: "x.zip", Bin: "x"}, "{{source.asset.bin}}")
	if _, why := p.DownloadFor("fixture-tool", linux); !strings.Contains(why, "this computer") {
		t.Fatalf("other platform reason = %q", why)
	}
	if _, why := p.DownloadFor("other", targetsFor("darwin", "arm64")); !strings.Contains(why, "does not provide") {
		t.Fatalf("unknown program reason = %q", why)
	}
}

// Every server in the embedded catalog either resolves to a safe HTTPS
// release file or says in plain words why Crude cannot install it.
func TestEmbeddedServersResolveOrExplain(t *testing.T) {
	c, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	installable := 0
	for _, target := range [][]string{targetsFor("linux", "amd64"), targetsFor("darwin", "arm64"), targetsFor("windows", "amd64")} {
		for _, language := range c.Languages() {
			for _, id := range language.Servers {
				server, _ := c.Server(id)
				pkg, ok := c.Package(server.Package)
				if !ok {
					continue
				}
				got, why := pkg.DownloadFor(server.Command, target)
				switch {
				case why == "" && (!strings.HasPrefix(got.URL, "https://github.com/") || got.Bin == "" || got.Archive == ""):
					t.Fatalf("%s: unsafe download %+v", server.ID, got)
				case why == "":
					installable++
				case !strings.HasSuffix(why, "."):
					t.Fatalf("%s: reason %q is not a sentence", server.ID, why)
				}
			}
		}
	}
	if installable == 0 {
		t.Fatal("no embedded server is installable")
	}
}
