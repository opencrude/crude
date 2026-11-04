package catalog

import (
	"reflect"
	"testing"
)

func TestPackageManagerRecipesPreserveCatalogIdentities(t *testing.T) {
	c, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		pkg, command string
		want         PackageManagerRecipe
	}{
		{"ansible-language-server", "ansible-language-server", PackageManagerRecipe{
			Kind: "npm", Manager: "npm", Registry: "npm registry", Runtime: "Node.js",
			Name: "@ansible/ansible-language-server", Version: "26.6.0",
			ProgramKind: "npm", Program: "ansible-language-server", Qualifiers: map[string]string{},
		}},
		{"perlnavigator", "perlnavigator", PackageManagerRecipe{
			Kind: "npm", Manager: "npm", Registry: "npm registry", Runtime: "Node.js",
			Name: "perlnavigator-server", Version: "0.8.20",
			ProgramKind: "node", Program: "node_modules/perlnavigator-server/out/server.js", Qualifiers: map[string]string{},
		}},
		{"python-lsp-server", "pylsp", PackageManagerRecipe{
			Kind: "pypi", Manager: "pip", Registry: "PyPI", Runtime: "Python 3",
			Name: "python-lsp-server", Version: "1.15.0",
			ProgramKind: "pypi", Program: "pylsp", Qualifiers: map[string]string{"extra": "all"},
		}},
		{"cue", "cue", PackageManagerRecipe{
			Kind: "golang", Manager: "go", Registry: "Go module proxy", Runtime: "Go",
			Name: "cuelang.org/go", Version: "v0.17.1", Subpath: "cmd/cue",
			ProgramKind: "golang", Program: "cue", Qualifiers: map[string]string{},
		}},
		{"beancount-language-server", "beancount-language-server", PackageManagerRecipe{
			Kind: "cargo", Manager: "cargo", Registry: "Cargo registry", Runtime: "Rust",
			Name: "beancount-language-server", Version: "1.9.2",
			ProgramKind: "cargo", Program: "beancount-language-server",
			Qualifiers: map[string]string{"repository_url": "https://github.com/polarmutex/beancount-language-server"},
		}},
	} {
		t.Run(tc.pkg, func(t *testing.T) {
			p, ok := c.Package(tc.pkg)
			if !ok {
				t.Fatal("catalog package is missing")
			}
			got, ok := p.PackageManagerFor(tc.command)
			if !ok || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("recipe = %+v, %v; want %+v", got, ok, tc.want)
			}
			got.Qualifiers["mutated"] = "value"
			again, ok := p.PackageManagerFor(tc.command)
			if !ok || !reflect.DeepEqual(again, tc.want) {
				t.Fatal("returned recipe aliases its catalog data")
			}
		})
	}
}

func TestPackageManagerRecipesRejectUnsafeOrAmbiguousMetadata(t *testing.T) {
	for _, tc := range []struct{ source, bin string }{
		{"pkg:npm/../../escape@1.0", "npm:server"},
		{"pkg:npm/%40scope/name@1.0?extra=unknown", "npm:server"},
		{"pkg:pypi/name@1.0?extra=a&extra=b", "pypi:server"},
		{"pkg:pypi/name@1.0?extra=all%3Btouch", "pypi:server"},
		{"pkg:cargo/name@1.0?repository_url=https://user:pass@example.test/repo", "cargo:server"},
		{"pkg:cargo/name@1.0?repository_url=https%3A%2F%2Fexample.test%2Frepo%3Ftoken%3Dsecret", "cargo:server"},
		{"pkg:golang/example.test/mod@v1.0.0#../escape", "golang:server"},
		{"pkg:npm/name@1.0", "node:../../escape"},
		{"pkg:npm/name@1.0", "npm:--danger"},
		{"pkg:npm/name@1.0", "cargo:server"},
		{"pkg:npm/name", "npm:server"},
		{"pkg:unknown/name@1.0", "unknown:server"},
	} {
		p := Package{Source: Source{ID: tc.source}, Bin: map[string]string{"server": tc.bin}}
		if recipe, ok := p.PackageManagerFor("server"); ok {
			t.Fatalf("unsafe metadata accepted: %q %q => %+v", tc.source, tc.bin, recipe)
		}
	}
}

func TestEmbeddedPackageManagerProgramsHaveDescriptions(t *testing.T) {
	c, err := Builtin()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{"npm": 0, "pypi": 0, "golang": 0, "cargo": 0}
	for name := range c.packages {
		p, ok := c.Package(name)
		if !ok {
			t.Fatalf("missing package %s", name)
		}
		kind := p.Source.Type()
		if _, supported := seen[kind]; !supported {
			continue
		}
		for command := range p.Bin {
			if _, ok := p.PackageManagerFor(command); !ok {
				t.Errorf("%s: no metadata for %s from %s", name, command, p.Source.ID)
			}
			seen[kind]++
		}
	}
	for kind, count := range seen {
		if count == 0 {
			t.Errorf("catalog has no %s programs", kind)
		}
	}
}
