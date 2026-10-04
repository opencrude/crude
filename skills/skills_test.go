package skills_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/fileio"
	"crude/skills"
)

func TestLoadProgressiveDisclosureAndResources(t *testing.T) {
	directory := fixture(t, "pdf-processing", "name: pdf-processing\ndescription: |\n  Extract PDF text.\n  Use for PDF documents.\nlicense: Apache-2.0\nmetadata:\n  author: test\n  version: '1.0'\nallowed-tools: Bash(jq:*) Read Read\n", "# Extract\n\nRead references/guide.md when needed.\n")
	writeFile(t, filepath.Join(directory, "references", "guide.md"), []byte("Complete reference"), 0644)
	writeFile(t, filepath.Join(directory, "assets", "image.bin"), []byte{0, 255, 10}, 0644)
	metadata, err := skills.Inspect(directory)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.Name != "pdf-processing" || metadata.Instructions != "" || metadata.Digest != "" || len(metadata.Files) != 0 {
		t.Fatalf("inspection eagerly loaded instructions or resources: %+v", metadata)
	}
	if _, err := skills.ReadFile(metadata, "references/guide.md", 1024); !errors.Is(err, skills.ErrInvalid) {
		t.Fatalf("metadata-only read = %v, want activation required", err)
	}
	loaded := load(t, directory)
	if loaded.Instructions != "# Extract\n\nRead references/guide.md when needed.\n" || len(loaded.Digest) != 64 || loaded.License != "Apache-2.0" {
		t.Fatalf("unexpected activated skill: %+v", loaded)
	}
	if !reflect.DeepEqual(loaded.Files, []string{"SKILL.md", "assets/image.bin", "references/guide.md"}) {
		t.Fatalf("files are not deterministic: %v", loaded.Files)
	}
	if !reflect.DeepEqual(loaded.AllowedTools, []string{"Bash(jq:*)", "Read"}) || loaded.Metadata["version"] != "1.0" {
		t.Fatalf("metadata not preserved: %+v", loaded)
	}
	data, err := skills.ReadFile(loaded, "assets/image.bin", 3)
	if err != nil || !bytes.Equal(data, []byte{0, 255, 10}) {
		t.Fatalf("resource read = %v, %v", data, err)
	}
	if _, err := skills.ReadFile(loaded, "references/guide.md", 4); !errors.Is(err, skills.ErrLimit) {
		t.Fatalf("bounded read = %v, want size error without partial output", err)
	}
	writeFile(t, filepath.Join(directory, "references", "unindexed.md"), []byte("later authored work"), 0644)
	if _, err := skills.ReadFile(loaded, "references/unindexed.md", 1024); !errors.Is(err, skills.ErrUnsafePath) {
		t.Fatalf("unindexed resource read = %v", err)
	}
}

func TestInspectDoesNotTouchResources(t *testing.T) {
	directory := fixture(t, "inspect-only", "name: inspect-only\ndescription: Inspect without loading resources.\n", "# Instructions\n")
	makeSymlink(t, filepath.Join(t.TempDir(), "missing"), filepath.Join(directory, "references", "outside"))
	if _, err := skills.Inspect(directory); err != nil {
		t.Fatalf("metadata inspection read resources: %v", err)
	}
	if _, err := skills.Load(directory); !errors.Is(err, skills.ErrUnsafePath) {
		t.Fatalf("unsafe resource activation = %v", err)
	}
}

func TestLoadRejectsMalformedFrontmatter(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   error
	}{
		{name: "missing-name", header: "description: Good description.\n", want: skills.ErrInvalid},
		{name: "missing-description", header: "name: test-skill\n", want: skills.ErrInvalid},
		{name: "invalid-yaml", header: "name: [broken\ndescription: Good.\n", want: skills.ErrInvalid},
		{name: "mapping-required", header: "- name\n- test-skill\n", want: skills.ErrInvalid},
		{name: "numeric-name", header: "name: 123\ndescription: Good.\n", want: skills.ErrInvalid},
		{name: "boolean-description", header: "name: test-skill\ndescription: true\n", want: skills.ErrInvalid},
		{name: "empty-description", header: "name: test-skill\ndescription: '  '\n", want: skills.ErrInvalid},
		{name: "description-too-long", header: "name: test-skill\ndescription: " + strings.Repeat("x", 1025) + "\n", want: skills.ErrInvalid},
		{name: "uppercase-name", header: "name: Test-Skill\ndescription: Good.\n", want: skills.ErrInvalid},
		{name: "double-hyphen", header: "name: test--skill\ndescription: Good.\n", want: skills.ErrInvalid},
		{name: "trailing-hyphen", header: "name: test-skill-\ndescription: Good.\n", want: skills.ErrInvalid},
		{name: "leading-hyphen", header: "name: -test-skill\ndescription: Good.\n", want: skills.ErrInvalid},
		{name: "name-too-long", header: "name: " + strings.Repeat("x", 65) + "\ndescription: Good.\n", want: skills.ErrInvalid},
		{name: "directory-mismatch", header: "name: other-skill\ndescription: Good.\n", want: skills.ErrInvalid},
		{name: "duplicate-key", header: "name: test-skill\nname: test-skill\ndescription: Good.\n", want: skills.ErrInvalid},
		{name: "duplicate-metadata", header: "name: test-skill\ndescription: Good.\nmetadata:\n  version: '1'\n  version: '2'\n", want: skills.ErrInvalid},
		{name: "non-string-metadata", header: "name: test-skill\ndescription: Good.\nmetadata:\n  version: 1\n", want: skills.ErrInvalid},
		{name: "non-mapping-metadata", header: "name: test-skill\ndescription: Good.\nmetadata: []\n", want: skills.ErrInvalid},
		{name: "compatibility-too-long", header: "name: test-skill\ndescription: Good.\ncompatibility: " + strings.Repeat("x", 501) + "\n", want: skills.ErrInvalid},
		{name: "empty-compatibility", header: "name: test-skill\ndescription: Good.\ncompatibility: ''\n", want: skills.ErrInvalid},
		{name: "yaml-alias", header: "name: &alias test-skill\ndescription: *alias\n", want: skills.ErrUnsupported},
		{name: "yaml-object", header: "name: test-skill\ndescription: !!python/object test\n", want: skills.ErrInvalid},
		{name: "hooks", header: "name: test-skill\ndescription: Good.\nhooks:\n  load: echo injected\n", want: skills.ErrUnsupported},
		{name: "client-fields", header: "name: test-skill\ndescription: Good.\ndisable-model-invocation: true\n", want: skills.ErrUnsupported},
		{name: "dependencies-field", header: "name: test-skill\ndescription: Good.\ndependencies: python\n", want: skills.ErrUnsupported},
		{name: "tool-list", header: "name: test-skill\ndescription: Good.\nallowed-tools: [Read]\n", want: skills.ErrInvalid},
		{name: "shell-injection", header: "name: test-skill\ndescription: Good.\nallowed-tools: Bash($(touch /tmp/injected))\n", want: skills.ErrUnsupported},
		{name: "shell-separator", header: "name: test-skill\ndescription: Good.\nallowed-tools: Read;touch\n", want: skills.ErrUnsupported},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			directory := fixture(t, "test-skill", test.header, "# Body\n")
			if _, err := skills.Load(directory); !errors.Is(err, test.want) {
				t.Fatalf("Load = %v, want %v", err, test.want)
			}
		})
	}
}

func TestLoadTextEncodingAndUnicodeLimits(t *testing.T) {
	t.Run("unicode", func(t *testing.T) {
		directory := fixture(t, "分析-資料", "name: 分析-資料\ndescription: '"+strings.Repeat("界", 1024)+"'\n", "# 分析\n")
		if _, err := skills.Load(directory); err != nil {
			t.Fatalf("character limits treated as byte limits: %v", err)
		}
	})
	t.Run("bom-crlf", func(t *testing.T) {
		directory := fixture(t, "test-skill", "name: test-skill\ndescription: Good.\n", "Body\n")
		definition := readFile(t, filepath.Join(directory, "SKILL.md"))
		writeFile(t, filepath.Join(directory, "SKILL.md"), append([]byte("\ufeff"), bytes.ReplaceAll(definition, []byte("\n"), []byte("\r\n"))...), 0644)
		if loaded := load(t, directory); loaded.Instructions != "Body\r\n" {
			t.Fatalf("BOM/CRLF instructions = %q", loaded.Instructions)
		}
	})
	for _, definition := range [][]byte{[]byte("name: test-skill\ndescription: Good.\n"), []byte("---\nname: test-skill\ndescription: Good.\n"), []byte("---\nname: test-skill\ndescription: Good.\n---\n\xff"), []byte("---\nname: test-skill\ndescription: Good.\n---\n\x00")} {
		directory := fixture(t, "test-skill", "name: test-skill\ndescription: Good.\n", "Body\n")
		writeFile(t, filepath.Join(directory, "SKILL.md"), definition, 0644)
		if _, err := skills.Load(directory); !errors.Is(err, skills.ErrInvalid) {
			t.Fatalf("invalid document accepted: %v", err)
		}
	}
}

func TestLoadBudgets(t *testing.T) {
	t.Run("frontmatter", func(t *testing.T) {
		directory := fixture(t, "test-skill", "name: test-skill\ndescription: Good.\nmetadata:\n  long: '"+strings.Repeat("x", skills.MaxFrontmatterBytes)+"'\n", "Body\n")
		if _, err := skills.Load(directory); !errors.Is(err, skills.ErrLimit) {
			t.Fatalf("frontmatter budget = %v", err)
		}
	})
	t.Run("instructions", func(t *testing.T) {
		directory := fixture(t, "test-skill", "name: test-skill\ndescription: Good.\n", strings.Repeat("x", skills.MaxInstructionBytes+1))
		if _, err := skills.Load(directory); !errors.Is(err, skills.ErrLimit) {
			t.Fatalf("instruction budget = %v", err)
		}
	})
	t.Run("resource", func(t *testing.T) {
		directory := fixture(t, "test-skill", "name: test-skill\ndescription: Good.\n", "Body\n")
		sparseFile(t, filepath.Join(directory, "oversized.bin"), skills.MaxFileBytes+1)
		if _, err := skills.Load(directory); !errors.Is(err, skills.ErrLimit) {
			t.Fatalf("resource budget = %v", err)
		}
	})
	t.Run("total-bytes", func(t *testing.T) {
		directory := fixture(t, "test-skill", "name: test-skill\ndescription: Good.\n", "Body\n")
		for number := 0; number < 5; number++ {
			sparseFile(t, filepath.Join(directory, fmt.Sprintf("resource-%d.bin", number)), skills.MaxFileBytes)
		}
		if _, err := skills.Load(directory); !errors.Is(err, skills.ErrLimit) {
			t.Fatalf("package byte budget = %v", err)
		}
	})
	t.Run("file-count", func(t *testing.T) {
		directory := fixture(t, "test-skill", "name: test-skill\ndescription: Good.\n", "Body\n")
		for number := 0; number < skills.MaxFiles; number++ {
			writeFile(t, filepath.Join(directory, fmt.Sprintf("resource-%04d", number)), nil, 0644)
		}
		if _, err := skills.Load(directory); !errors.Is(err, skills.ErrLimit) {
			t.Fatalf("file count budget = %v", err)
		}
	})
	t.Run("entry-count", func(t *testing.T) {
		directory := fixture(t, "test-skill", "name: test-skill\ndescription: Good.\n", "Body\n")
		for number := 0; number < skills.MaxEntries; number++ {
			if err := os.Mkdir(filepath.Join(directory, fmt.Sprintf("directory-%04d", number)), 0755); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := skills.Load(directory); !errors.Is(err, skills.ErrLimit) {
			t.Fatalf("directory count budget = %v", err)
		}
	})
	t.Run("depth", func(t *testing.T) {
		directory := fixture(t, "test-skill", "name: test-skill\ndescription: Good.\n", "Body\n")
		writeFile(t, filepath.Join(directory, strings.Repeat("directory/", skills.MaxPathDepth), "resource"), nil, 0644)
		if _, err := skills.Load(directory); !errors.Is(err, skills.ErrLimit) {
			t.Fatalf("path depth budget = %v", err)
		}
	})
}

func TestResourcePathsRemainContained(t *testing.T) {
	directory := fixture(t, "test-skill", "name: test-skill\ndescription: Good.\n", "Body\n")
	writeFile(t, filepath.Join(directory, "references", "safe.md"), []byte("safe"), 0644)
	loaded := load(t, directory)
	for _, relative := range []string{"", "../outside", "references/../../outside", "/etc/passwd", "references/../SKILL.md", "./SKILL.md", "references//safe.md", `..\outside`, `C:\secret`, `C:secret`, "SKILL.md\x00"} {
		if data, err := skills.ReadFile(loaded, relative, 1024); !errors.Is(err, skills.ErrUnsafePath) || data != nil {
			t.Fatalf("ReadFile(%q) = %q, %v", relative, data, err)
		}
	}
	for _, limit := range []int64{0, -1} {
		if _, err := skills.ReadFile(loaded, "SKILL.md", limit); !errors.Is(err, skills.ErrLimit) {
			t.Fatalf("ReadFile limit %d = %v", limit, err)
		}
	}
}

func TestSymlinkContainmentAndMutation(t *testing.T) {
	t.Run("internal-file", func(t *testing.T) {
		directory := fixture(t, "test-skill", "name: test-skill\ndescription: Good.\n", "Body\n")
		writeFile(t, filepath.Join(directory, "references", "safe.md"), []byte("safe"), 0644)
		makeSymlink(t, "references/safe.md", filepath.Join(directory, "guide.md"))
		loaded := load(t, directory)
		if data, err := skills.ReadFile(loaded, "guide.md", 1024); err != nil || string(data) != "safe" {
			t.Fatalf("internal symlink = %q, %v", data, err)
		}
	})
	for _, kind := range []string{"relative-outside", "absolute-outside", "directory", "loop", "broken"} {
		t.Run(kind, func(t *testing.T) {
			directory := fixture(t, "test-skill", "name: test-skill\ndescription: Good.\n", "Body\n")
			outside := filepath.Join(filepath.Dir(directory), "outside.md")
			writeFile(t, outside, []byte("secret"), 0644)
			target := "../outside.md"
			want := skills.ErrUnsafePath
			switch kind {
			case "absolute-outside":
				target = outside
			case "directory":
				target = "references"
				if err := os.Mkdir(filepath.Join(directory, "references"), 0755); err != nil {
					t.Fatal(err)
				}
				want = skills.ErrUnsupported
			case "loop":
				target = "link"
			case "broken":
				target = "missing.md"
			}
			makeSymlink(t, target, filepath.Join(directory, "link"))
			if _, err := skills.Load(directory); !errors.Is(err, want) {
				t.Fatalf("symlink activation = %v, want %v", err, want)
			}
		})
	}
	t.Run("root-symlink", func(t *testing.T) {
		directory := fixture(t, "test-skill", "name: test-skill\ndescription: Good.\n", "Body\n")
		link := filepath.Join(t.TempDir(), "test-skill")
		makeSymlink(t, directory, link)
		if _, err := skills.Load(link); !errors.Is(err, skills.ErrUnsafePath) {
			t.Fatalf("symlink root = %v", err)
		}
	})
	t.Run("replaced-parent", func(t *testing.T) {
		directory := fixture(t, "test-skill", "name: test-skill\ndescription: Good.\n", "Body\n")
		writeFile(t, filepath.Join(directory, "references", "safe.md"), []byte("safe"), 0644)
		loaded := load(t, directory)
		outside := t.TempDir()
		writeFile(t, filepath.Join(outside, "safe.md"), []byte("outside secret"), 0644)
		if err := os.Rename(filepath.Join(directory, "references"), filepath.Join(directory, "original-references")); err != nil {
			t.Fatal(err)
		}
		makeSymlink(t, outside, filepath.Join(directory, "references"))
		if data, err := skills.ReadFile(loaded, "references/safe.md", 1024); err == nil || data != nil {
			t.Fatalf("replaced parent leaked bytes: %q, %v", data, err)
		}
	})
}

func TestDigestPinsAllContentAndExecutableModes(t *testing.T) {
	directory := fixture(t, "test-skill", "name: test-skill\ndescription: Good.\n", "Body\n")
	resource := filepath.Join(directory, "assets", "resource")
	writeFile(t, resource, []byte("original"), 0644)
	loaded := load(t, directory)
	if reloaded := load(t, directory); loaded.Digest != reloaded.Digest {
		t.Fatal("unchanged skill digest is not deterministic")
	}
	modified := time.Now().Add(time.Hour)
	if err := os.Chtimes(resource, modified, modified); err != nil {
		t.Fatal(err)
	}
	if reloaded := load(t, directory); loaded.Digest != reloaded.Digest {
		t.Fatal("digest contains mutable modification times")
	}
	writeFile(t, resource, []byte("mutation"), 0644)
	if _, err := skills.ReadFile(loaded, "assets/resource", 1024); !errors.Is(err, skills.ErrChanged) {
		t.Fatalf("changed resource = %v", err)
	}
	changed := load(t, directory)
	if changed.Digest == loaded.Digest {
		t.Fatal("resource bytes are not pinned")
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(resource, 0755); err != nil {
			t.Fatal(err)
		}
		if load(t, directory).Digest == changed.Digest {
			t.Fatal("executable permissions are not pinned")
		}
	}
	writeFile(t, filepath.Join(directory, "SKILL.md"), []byte("---\nname: test-skill\ndescription: Good.\n---\nChanged body\n"), 0644)
	if _, err := skills.ReadFile(changed, "assets/resource", 1024); !errors.Is(err, skills.ErrChanged) {
		t.Fatalf("changed instructions = %v", err)
	}
}

func TestSerializedSkillRemainsDigestVerified(t *testing.T) {
	directory := fixture(t, "test-skill", "name: test-skill\ndescription: Good.\n", "Body\n")
	resource := filepath.Join(directory, "resource")
	writeFile(t, resource, []byte("content"), 0644)
	encoded, err := json.Marshal(load(t, directory))
	if err != nil {
		t.Fatal(err)
	}
	var restored skills.Skill
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if data, err := skills.ReadFile(restored, "resource", 1024); err != nil || string(data) != "content" {
		t.Fatalf("restored resource = %q, %v", data, err)
	}
	writeFile(t, resource, []byte("changed"), 0644)
	if _, err := skills.ReadFile(restored, "resource", 1024); !errors.Is(err, skills.ErrChanged) {
		t.Fatalf("restored digest mismatch = %v", err)
	}
}

func TestRequirementsDoNotExecuteOrPersistEnvironment(t *testing.T) {
	binaries := t.TempDir()
	marker := filepath.Join(t.TempDir(), "executed")
	executable := "skill-helper"
	if runtime.GOOS == "windows" {
		executable += ".exe"
	}
	writeFile(t, filepath.Join(binaries, executable), []byte("#!/bin/sh\nprintf executed > '"+marker+"'\n"), 0755)
	t.Setenv("PATH", binaries)
	t.Setenv("SKILL_SECRET", "do-not-persist-me")
	directory := fixture(t, "test-skill", "name: test-skill\ndescription: Good.\ncompatibility: Requires skill-helper\nmetadata:\n  crude.requires: skill-helper\n  crude.os: "+runtime.GOOS+"\n  crude.arch: "+runtime.GOARCH+"\n  crude.product: crude\n", "Body\n")
	loaded := load(t, directory)
	encoded, err := json.Marshal(loaded)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte("do-not-persist-me")) || bytes.Contains(encoded, []byte(binaries)) {
		t.Fatal("loader persisted runtime environment values")
	}
	if _, err := os.Stat(marker); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dependency was executed: %v", err)
	}
	for _, header := range []string{
		"compatibility: Requires missing-helper\n",
		"compatibility: Requires Python 3.14+ and uv\n",
		"compatibility: Requires and\n",
		"compatibility: Designed for another product\n",
		"metadata:\n  crude.os: unavailable-platform\n",
		"metadata:\n  dependencies: missing-helper\n",
		"metadata:\n  crude.requires: $(touch-marker)\n",
	} {
		directory := fixture(t, "test-skill", "name: test-skill\ndescription: Good.\n"+header, "Body\n")
		if _, err := skills.Inspect(directory); err != nil {
			t.Fatalf("unavailable skills cannot be inspected: %v", err)
		}
		unavailable, err := skills.Load(directory)
		var requirement *skills.RequirementError
		if !errors.Is(err, skills.ErrUnavailable) || !errors.As(err, &requirement) || requirement.Reason == "" {
			t.Fatalf("requirements do not have a precise unavailable reason: %v", err)
		}
		if unavailable.Name != "test-skill" || unavailable.Digest == "" || unavailable.Instructions == "" {
			t.Fatalf("unavailable skill details cannot be inspected: %+v", unavailable)
		}
	}
}

func TestCreateDoesNotOverwriteAuthoredWork(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "new-skill")
	description := "Inspect YAML safely: use quotes ' and \" and preserve\nmultiline descriptions."
	created, err := skills.Create(target, "new-skill", description)
	if err != nil {
		t.Fatal(err)
	}
	if created.Description != description || created.Name != "new-skill" || created.Instructions == "" || load(t, target).Digest != created.Digest {
		t.Fatalf("created template did not load: %+v", created)
	}
	authored := []byte("Authored after template creation.\n")
	writeFile(t, filepath.Join(target, "SKILL.md"), authored, 0644)
	if _, err := skills.Create(target, "new-skill", "Replace it."); !errors.Is(err, os.ErrExist) {
		t.Fatalf("overwrite = %v", err)
	}
	if !bytes.Equal(readFile(t, filepath.Join(target, "SKILL.md")), authored) {
		t.Fatal("creation overwrote authored work")
	}
	if _, err := skills.Create(filepath.Join(parent, "mismatch"), "new-skill", "Good."); !errors.Is(err, skills.ErrInvalid) {
		t.Fatalf("destination mismatch = %v", err)
	}
	if _, err := skills.Create(filepath.Join(parent, "invalid-skill"), "invalid-skill", " "); !errors.Is(err, skills.ErrInvalid) {
		t.Fatalf("invalid description = %v", err)
	}
	assertNoStaging(t, parent)
}

func TestCreationRejectsDestinationSymlinks(t *testing.T) {
	parent, outside := t.TempDir(), t.TempDir()
	makeSymlink(t, outside, filepath.Join(parent, "alias"))
	if _, err := skills.Create(filepath.Join(parent, "alias", "test-skill"), "test-skill", "Good."); !errors.Is(err, skills.ErrUnsafePath) {
		t.Fatalf("symlink parent = %v", err)
	}
	makeSymlink(t, filepath.Join(outside, "missing"), filepath.Join(parent, "test-skill"))
	if _, err := skills.Create(filepath.Join(parent, "test-skill"), "test-skill", "Good."); !errors.Is(err, os.ErrExist) {
		t.Fatalf("existing dangling destination = %v", err)
	}
	if entries, err := os.ReadDir(outside); err != nil || len(entries) != 0 {
		t.Fatalf("creation followed a destination symlink: %v, %v", entries, err)
	}
}

func TestConcurrentCreationPublishesOnlyOne(t *testing.T) {
	parent := t.TempDir()
	target := filepath.Join(parent, "test-skill")
	var workers sync.WaitGroup
	results := make(chan error, 8)
	for attempt := 0; attempt < cap(results); attempt++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			_, err := skills.Create(target, "test-skill", "Good.")
			results <- err
		}()
	}
	workers.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if !errors.Is(err, os.ErrExist) {
			t.Fatalf("concurrent creation returned an unrelated failure: %v", err)
		}
	}
	if succeeded != 1 {
		t.Fatalf("published %d skills, want exactly one", succeeded)
	}
	load(t, target)
	assertNoStaging(t, parent)
}

func TestMakeLocalCopyPreservesImmutableSource(t *testing.T) {
	source := fixture(t, "source-skill", "name: source-skill\ndescription: Good.\nlicense: MIT\nmetadata:\n  author: test\n", "# Original instructions\n")
	writeFile(t, filepath.Join(source, "scripts", "helper.sh"), []byte("#!/bin/sh\nexit 1\n"), 0755)
	writeFile(t, filepath.Join(source, "assets", "binary"), []byte{0, 255, 2}, 0644)
	if err := os.Mkdir(filepath.Join(source, "empty"), 0755); err != nil {
		t.Fatal(err)
	}
	original := load(t, source)
	originalDefinition := readFile(t, filepath.Join(source, "SKILL.md"))
	parent := t.TempDir()
	sameName := filepath.Join(parent, "source-skill")
	copied, err := skills.MakeLocalCopy(context.Background(), original, sameName)
	if err != nil {
		t.Fatal(err)
	}
	if copied.Path != sameName || copied.Digest != original.Digest || !bytes.Equal(readFile(t, filepath.Join(sameName, "SKILL.md")), originalDefinition) {
		t.Fatalf("exact local copy changed immutable bytes: %+v", copied)
	}
	if _, err := os.Stat(filepath.Join(sameName, "empty")); err != nil {
		t.Fatalf("empty resource directory was lost: %v", err)
	}
	if runtime.GOOS != "windows" {
		mode, err := os.Stat(filepath.Join(sameName, "scripts", "helper.sh"))
		if err != nil || mode.Mode().Perm()&0111 == 0 {
			t.Fatalf("script mode was not copied: %v, %v", mode, err)
		}
	}
	renamedTarget := filepath.Join(parent, "local-skill")
	renamed, err := skills.MakeLocalCopy(context.Background(), original, renamedTarget)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.Name != "local-skill" || renamed.Instructions != original.Instructions || renamed.Metadata["author"] != "test" || load(t, renamedTarget).Name != "local-skill" {
		t.Fatalf("renamed copy did not preserve metadata/body: %+v", renamed)
	}
	if _, err := skills.MakeLocalCopy(context.Background(), original, renamedTarget); !errors.Is(err, os.ErrExist) {
		t.Fatalf("copy overwrite = %v", err)
	}
	if _, err := skills.MakeLocalCopy(context.Background(), original, filepath.Join(source, "nested-copy")); !errors.Is(err, skills.ErrUnsafePath) {
		t.Fatalf("copy into immutable source = %v", err)
	}
	writeFile(t, filepath.Join(renamedTarget, "assets", "binary"), []byte("local edit"), 0644)
	if load(t, source).Digest != original.Digest || !bytes.Equal(readFile(t, filepath.Join(source, "SKILL.md")), originalDefinition) {
		t.Fatal("copy or editing the copy mutated its source")
	}
	assertNoStaging(t, parent)
}

func TestMakeLocalCopyPreservesContainedSymlinks(t *testing.T) {
	source := fixture(t, "source-skill", "name: source-skill\ndescription: Good.\n", "Body\n")
	writeFile(t, filepath.Join(source, "references", "guide.md"), []byte("reference"), 0644)
	makeSymlink(t, "references/guide.md", filepath.Join(source, "guide"))
	original := load(t, source)
	target := filepath.Join(t.TempDir(), "source-skill")
	copied, err := skills.MakeLocalCopy(context.Background(), original, target)
	if err != nil {
		t.Fatal(err)
	}
	if copied.Digest != original.Digest {
		t.Fatal("contained symlink was not pinned in the copy")
	}
	if link, err := os.Readlink(filepath.Join(target, "guide")); err != nil || filepath.ToSlash(link) != "references/guide.md" {
		t.Fatalf("symlink not preserved: %q, %v", link, err)
	}
	if data, err := skills.ReadFile(copied, "guide", 1024); err != nil || string(data) != "reference" {
		t.Fatalf("copied symlink read = %q, %v", data, err)
	}
}

func TestCopyCancellationAndSourcePreconditions(t *testing.T) {
	source := fixture(t, "source-skill", "name: source-skill\ndescription: Good.\n", "Body\n")
	loaded := load(t, source)
	parent := t.TempDir()
	target := filepath.Join(parent, "local-skill")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := skills.MakeLocalCopy(ctx, loaded, target); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled copy = %v", err)
	}
	if _, err := skills.LoadContext(ctx, source); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-cancelled load = %v", err)
	}
	unlock := fileio.LockTreeMutation()
	waitCtx, stop := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		_, err := skills.MakeLocalCopy(waitCtx, loaded, target)
		result <- err
	}()
	stop()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("waiting copy cancellation = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("copy ignored cancellation while waiting for mutation ownership")
	}
	unlock()
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cancelled copy published a destination: %v", err)
	}
	writeFile(t, filepath.Join(source, "SKILL.md"), []byte("---\nname: source-skill\ndescription: Good.\n---\nChanged\n"), 0644)
	if _, err := skills.MakeLocalCopy(context.Background(), loaded, target); !errors.Is(err, skills.ErrChanged) {
		t.Fatalf("stale source copy = %v", err)
	}
	assertNoStaging(t, parent)
}

func fixture(t *testing.T, name, header, body string) string {
	t.Helper()
	directory := filepath.Join(t.TempDir(), name)
	writeFile(t, filepath.Join(directory, "SKILL.md"), []byte("---\n"+header+"---\n"+body), 0644)
	return directory
}

func load(t *testing.T, directory string) skills.Skill {
	t.Helper()
	loaded, err := skills.Load(directory)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func writeFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func sparseFile(t *testing.T, path string, size int64) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := errors.Join(file.Truncate(size), file.Close()); err != nil {
		t.Fatal(err)
	}
}

func makeSymlink(t *testing.T, target, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		if runtime.GOOS == "windows" {
			t.Skipf("symlink privileges unavailable: %v", err)
		}
		t.Fatal(err)
	}
}

func assertNoStaging(t *testing.T, parent string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".crude-skill-") {
			t.Fatalf("incomplete skill staging was not cleaned up: %s", entry.Name())
		}
	}
}
