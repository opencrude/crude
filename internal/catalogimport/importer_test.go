package main

import (
	"slices"
	"strings"
	"testing"

	"crude/insight/catalog"
)

const helixFixture = `
[language-server.alpha-ls]
command = "alpha-ls"
args = ["--stdio"]
config = { alpha = { strict = true } }

[language-server.beta-ls]
command = "beta"

[[language]]
name = "alpha"
language-id = "alpha-lang"
file-types = ["alp", "Alpfile", { glob = "alpha.conf" }, { glob = "dir/*.alp" }]
roots = ["alpha.toml", "../escape"]
language-servers = ["alpha-ls", "missing-ls"]

[[language]]
name = "go"
file-types = ["go"]
language-servers = ["beta-ls"]

[[language]]
name = "plain"
file-types = ["txt"]
comment-tokens = ["#", ";"]
block-comment-tokens = [{ start = "(*", end = "*)" }]
grammar = "plaintext"
formatter = { command = "fmt-plain", args = ["--file", "{file}"] }
`

const masonFixture = `[
 {"name":"alpha-ls","description":"Alpha\n","licenses":["MIT"],"languages":["Alpha"],"categories":["LSP"],
  "source":{"id":"pkg:github/acme/alpha-ls@v1.0.0","asset":[
    {"target":["linux_x64_gnu","linux_x64"],"file":"alpha-{{version}}.tar.gz","bin":"alpha-ls"},
    {"target":"win_x64","file":["a.zip","b.zip"]}]},
  "bin":{"alpha-ls":"{{source.asset.bin}}"}},
 {"name":"beta","licenses":["Apache-2.0"],"source":{"id":"pkg:npm/beta@2.0.0"},"bin":{"beta":"npm:beta"}}
]`

const authoredFixture = `{
 "note": "fixture",
 "languages": {"go": {"note": "fixture root", "roots": ["go.mod"], "workspace_roots": []}},
 "servers": {"beta-ls": {"note": "fixture scope", "diagnostic_scope": "project",
   "hints": {"environment": {"THREADS": "{threads}"}}}}
}`

func fixturePins() Pins {
	var pins Pins
	pins.Helix.Commit, pins.Helix.SHA256, pins.Helix.License = "abc", "00", "MPL-2.0"
	pins.Mason.Release, pins.Mason.SHA256, pins.Mason.License = "r1", "11", "Apache-2.0"
	return pins
}

func TestImportFixture(t *testing.T) {
	doc, report, err := Import(fixturePins(), []byte(helixFixture), []byte(masonFixture), []byte(authoredFixture))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := catalog.Encode(doc)
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Load(nil, encoded)
	if err != nil {
		t.Fatal(err)
	}
	alpha, ok := c.Language("alpha")
	if !ok {
		t.Fatal("alpha missing")
	}
	if alpha.LSPLanguageID != "alpha-lang" || !slices.Equal(alpha.Extensions, []string{".alp"}) ||
		!slices.Equal(alpha.Servers, []string{"alpha-ls"}) || !slices.Equal(alpha.Roots, []string{"alpha.toml"}) {
		t.Fatalf("alpha: %+v", alpha)
	}
	if !slices.Contains(alpha.Filenames, "alpha.conf") || !slices.Contains(alpha.Filenames, "*.Alpfile") {
		t.Fatalf("alpha file names: %v", alpha.Filenames)
	}
	// A language without a server stays for the editor data it carries.
	plain, ok := c.Language("plain")
	if !ok || len(plain.Servers) != 0 || !slices.Equal(plain.CommentTokens, []string{"#", ";"}) ||
		plain.BlockComment == nil || plain.BlockComment.Start != "(*" || plain.Grammar != "plaintext" ||
		plain.Formatter == nil || !slices.Equal(plain.Formatter.ArgsFor("/p/a.txt"), []string{"--file", "/p/a.txt"}) {
		t.Fatalf("plain: %+v", plain)
	}
	server, _ := c.Server("alpha-ls")
	if server.Package != "alpha-ls" || server.InitializationOptions["alpha"] == nil {
		t.Fatalf("alpha server: %+v", server)
	}
	pkg, _ := c.Package("alpha-ls")
	if len(pkg.Source.Assets) != 1 || pkg.Provenance[0].Revision != "r1" {
		t.Fatalf("alpha package: %+v", pkg)
	}
	asset, ok := pkg.AssetFor([]string{"linux_x64"})
	if !ok || asset.File != "alpha-v1.0.0.tar.gz" {
		t.Fatalf("asset: %+v", asset)
	}
	beta, _ := c.Server("beta-ls")
	if beta.DiagnosticScope != "project" || beta.Package != "beta" || beta.Note != "fixture scope" {
		t.Fatalf("authored server: %+v", beta)
	}
	golang, _ := c.Language("go")
	if !slices.Equal(golang.Roots, []string{"go.mod"}) || !slices.Equal(golang.Linguist, []string{"Go"}) {
		t.Fatalf("authored language: %+v", golang)
	}
	for _, want := range []string{"missing-ls", "../escape", "dir/*.alp", "several files"} {
		if !slices.ContainsFunc(report.Skipped, func(line string) bool { return strings.Contains(line, want) }) {
			t.Errorf("report does not mention %q: %v", want, report.Skipped)
		}
	}
}

func TestImportRejectsBadOverlay(t *testing.T) {
	cases := map[string]string{
		"unknown language": `{"languages":{"nope":{"note":"x"}}}`,
		"missing note":     `{"languages":{"go":{}}}`,
		"unknown field":    `{"languages":{"go":{"note":"x","colour":"red"}}}`,
		"server no launch": `{"servers":{"nope":{"note":"x"}}}`,
	}
	for name, overlay := range cases {
		if _, _, err := Import(fixturePins(), []byte(helixFixture), []byte(masonFixture), []byte(overlay)); err == nil {
			t.Errorf("%s: overlay was accepted", name)
		}
	}
}
