package main

import (
	"bytes"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func putCohortFile(t *testing.T, root, name, text string) {
	t.Helper()
	filename := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func cohortFixture(t *testing.T, reverse bool) string {
	t.Helper()
	root := t.TempDir()
	files := [][2]string{
		{"go.mod", "module fixture\n\ngo 1.25\nreplace example.com/fork => ./third_party/fork\n"},
		{"go.sum", "dependency checksums\n"},
		{"command/run.go", "package command\nfunc Run() string { return \"original semantics\" }\n"},
		{"command/run_linux.go", "//go:build linux\n\npackage command\n"},
		{"command/run_windows.go", "//go:build windows\n\npackage command\n"},
		{"command/custom.go", "//go:build custom_profile\n\npackage command\n"},
		{"command/run_test.go", "package command\n"},
		{"command/wire_gen.go", "package command\n// generated production codec\n"},
		{"coreprotocol/schema_gen.go", "package coreprotocol\nconst Schema = \"wire\"\n"},
		{"coreprotocol/wire_types_gen_test.go", "package coreprotocol\n"},
		{"third_party/fork/go.mod", "module example.com/fork\n"},
		{"third_party/fork/go.sum", "fork checksums\n"},
		{"third_party/fork/fork.go", "package fork\n"},
		{"third_party/fork/fork_windows.go", "package fork\n"},
		{"third_party/fork/impl.s", "native source\n"},
		{"third_party/fork/fork_test.go", "package fork\n"},
		{"third_party/pathwalk/walk.go", "package pathwalk\n"},
		{"third_party/unused/ignored.go", "not a production dependency"},
		{"deps/GHOSTTY_PIN", "GHOSTTY_COMMIT=pin\n"},
		{"deps/GHOSTTY_PIN.ps1", "$GHOSTTY_COMMIT = 'pin'\n"},
		{"deps/libghostty-vt/huge.go", "excluded native outputs"},
		{"prompt/instructions.go", "package prompt\nconst Instructions = \"policy\"\n"},
		{"prompt/policy.tmpl", "model instructions\n"},
		{"docs/policy.tmpl", "not runtime instructions\n"},
		{"data/embed_windows.go", "//go:build windows\n\npackage data\nimport _ \"embed\"\n//go:embed\t\"runtime data.json\"\nvar data string\n"},
		{"data/runtime data.json", "{\"runtime\":true}"},
		{"data/other.json", "not embedded"},
		{"buildinfo/cohort_gen.go", "ignored own output"},
	}
	if reverse {
		slices.Reverse(files)
	}
	for _, file := range files {
		putCohortFile(t, root, file[0], file[1])
	}
	for _, dir := range []string{".git", ".claude", ".cache", "build", "dist", "artifacts", "tmp", "testdata", "vendor", "node_modules"} {
		putCohortFile(t, root, dir+"/ignored.go", "not source")
	}
	return root
}

func mustCohort(t *testing.T, root string) string {
	t.Helper()
	digest, err := cohortDigest(root)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != 32 {
		t.Fatalf("not a full SHA256: %q (%v)", digest, err)
	}
	return digest
}

func TestCohortCanonicalCorpus(t *testing.T) {
	first, second := cohortFixture(t, false), cohortFixture(t, true)
	original := mustCohort(t, first)
	if original != mustCohort(t, second) {
		t.Fatal("digest depends on checkout location or file creation order")
	}
	names, err := cohortFiles(first)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"command/custom.go", "command/run.go", "command/run_linux.go", "command/run_windows.go", "command/wire_gen.go",
		"coreprotocol/schema_gen.go", "data/embed_windows.go", "data/runtime data.json",
		"deps/GHOSTTY_PIN", "deps/GHOSTTY_PIN.ps1", "go.mod", "go.sum", "prompt/instructions.go", "prompt/policy.tmpl",
		"third_party/fork/fork.go", "third_party/fork/fork_windows.go", "third_party/fork/go.mod", "third_party/fork/go.sum", "third_party/fork/impl.s", "third_party/pathwalk/walk.go",
	}
	if !reflect.DeepEqual(names, want) {
		t.Fatalf("wrong corpus:\n got %q\nwant %q", names, want)
	}
	for _, goos := range []string{"linux", "windows", "darwin"} {
		t.Setenv("GOOS", goos)
		t.Setenv("GOARCH", "arm64")
		t.Setenv("GOFLAGS", "-tags=custom_profile")
		if mustCohort(t, first) != original || mustCohort(t, second) != original {
			t.Fatalf("host-dependent corpus on %s", goos)
		}
	}
}

func TestCohortSourceChanges(t *testing.T) {
	for _, name := range []string{
		"command/run.go", "command/run_windows.go", "command/custom.go", "command/wire_gen.go", "coreprotocol/schema_gen.go",
		"go.mod", "go.sum", "third_party/fork/fork.go", "third_party/fork/go.mod", "third_party/fork/go.sum", "third_party/fork/impl.s",
		"data/runtime data.json", "prompt/instructions.go", "prompt/policy.tmpl", "deps/GHOSTTY_PIN",
	} {
		t.Run(name, func(t *testing.T) {
			root := cohortFixture(t, false)
			before := mustCohort(t, root)
			filename := filepath.Join(root, filepath.FromSlash(name))
			data, err := os.ReadFile(filename)
			if err != nil {
				t.Fatal(err)
			}
			putCohortFile(t, root, name, string(data)+"\n// changed semantic input\n")
			if mustCohort(t, root) == before {
				t.Fatal("production-source change did not change cohort")
			}
		})
	}
}

func TestCohortExcludedChanges(t *testing.T) {
	root := cohortFixture(t, false)
	before := mustCohort(t, root)
	for _, name := range []string{"command/run_test.go", "coreprotocol/wire_types_gen_test.go", "third_party/fork/fork_test.go", "buildinfo/cohort_gen.go", "testdata/new.go", "third_party/unused/ignored.go", "dist/new.go", "deps/libghostty-vt/huge.go", "data/other.json", "docs/policy.tmpl", ".claude/new.go"} {
		putCohortFile(t, root, name, "ignored edit")
	}
	if mustCohort(t, root) != before {
		t.Fatal("tests, unrelated data, build output, or own generated output changed cohort")
	}
}

func TestCohortCheckIsNonMutating(t *testing.T) {
	root := cohortFixture(t, false)
	if err := writeCohort(root); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(root, cohortOutput))
	if err != nil {
		t.Fatal(err)
	}
	if err := writeCohort(root); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(filepath.Join(root, cohortOutput))
	if err != nil || !bytes.Equal(first, second) {
		t.Fatalf("repeated generation changed output: %v", err)
	}
	t.Chdir(root)
	if err := runMode(true); err != nil {
		t.Fatal(err)
	}
	putCohortFile(t, root, "command/run.go", "package command\nfunc Run() string { return \"changed semantics\" }\n")
	before := snapshotCohortFixture(t, root)
	if err := runMode(true); err == nil || !strings.Contains(err.Error(), "stale") {
		t.Fatalf("semantic-only edit must reject freshness, got %v", err)
	}
	if after := snapshotCohortFixture(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("-check rewrote or removed files (including generated codecs)")
	}
	putCohortFile(t, root, cohortOutput, "package buildinfo\nconst ProtocolBuild = \"forged\"\n")
	if err := runMode(true); err == nil {
		t.Fatal("incorrect generated constant accepted")
	}
}

func snapshotCohortFixture(t *testing.T, root string) map[string]string {
	t.Helper()
	result := map[string]string{}
	err := filepath.WalkDir(root, func(name string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(name)
		if err == nil {
			info, statErr := entry.Info()
			if statErr != nil {
				return statErr
			}
			result[name] = string(data) + info.ModTime().String()
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCohortEmbedsAndFailures(t *testing.T) {
	t.Run("directory and all", func(t *testing.T) {
		root := cohortFixture(t, false)
		putCohortFile(t, root, "data/embed.go", "package data\nimport _ \"embed\"\n//go:embed assets all:private\nvar resources embed.FS\n")
		for _, name := range []string{"assets/visible", "assets/.hidden", "assets/_hidden", "private/.hidden", "private/_hidden"} {
			putCohortFile(t, root, "data/"+name, name)
		}
		names, err := cohortFiles(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"data/assets/visible", "data/private/.hidden", "data/private/_hidden"} {
			if !slices.Contains(names, name) {
				t.Errorf("embedded input absent: %s", name)
			}
		}
		for _, name := range []string{"data/assets/.hidden", "data/assets/_hidden"} {
			if slices.Contains(names, name) {
				t.Errorf("hidden input incorrectly embedded: %s", name)
			}
		}
	})
	t.Run("missing embed", func(t *testing.T) {
		root := cohortFixture(t, false)
		putCohortFile(t, root, "data/missing.go", "package data\nimport _ \"embed\"\n//go:embed missing.json\nvar missing string\n")
		if _, err := cohortDigest(root); err == nil {
			t.Fatal("missing embedded input accepted")
		}
	})
	t.Run("external replacement", func(t *testing.T) {
		root := cohortFixture(t, false)
		putCohortFile(t, root, "go.mod", "module fixture\nreplace example.com/fork => ../outside\n")
		if _, err := cohortDigest(root); err == nil {
			t.Fatal("external unportable replacement accepted")
		}
	})
	t.Run("symlink source", func(t *testing.T) {
		root := cohortFixture(t, false)
		if err := os.Symlink(filepath.Join(root, "command/run.go"), filepath.Join(root, "linked.go")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, err := cohortDigest(root); err == nil {
			t.Fatal("symlinked input accepted")
		}
	})
}
