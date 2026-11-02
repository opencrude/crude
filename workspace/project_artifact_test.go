package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/internal/testsymlink"
)

func TestProjectArtifactSelectionRefusesUnsafeOrUnboundedOperands(t *testing.T) {
	cases := [][]ProjectArtifactSet{
		{{ID: "x"}},
		{{ID: "x", CompilationDatabase: "commands.json", CMakeIndex: "index.json"}},
		{{ID: "x", CompilationDatabase: "../commands.json"}},
		{{ID: "x", CompilationDatabase: filepath.Join(t.TempDir(), "commands.json")}},
		{{ID: "x", CompilationDatabase: "."}},
		{{ID: "x", CompilationDatabase: "nul\x00file"}},
		{{ID: "x", CompilationDatabase: strings.Repeat("a", projectContextMaxPathBytes+1)}},
		{{ID: "x", CompilationDatabase: "a.json"}, {ID: "x", CompilationDatabase: "b.json"}},
		{{ID: "a", CompilationDatabase: "same.json"}, {ID: "b", CompilationDatabase: "same.json"}},
		{{ID: "x", CMakeIndex: filepath.Join("build", "index.json"), CMakeReplies: []string{"outside.json"}}},
		{{ID: "x", CMakeIndex: "index.json", CMakeReplies: []string{"index.json"}}},
		make([]ProjectArtifactSet, projectContextMaxArtifacts+1),
	}
	for _, selected := range cases {
		if _, err := normalizeProjectSelection(selected); !errors.Is(err, ErrProjectContextSelection) {
			t.Fatal("invalid declaration gained artifact read authority", selected, err)
		}
	}
}

func TestProjectArtifactOversizedReplySelectionIsBoundedBeforeCopyOrSort(t *testing.T) {
	// An adversarial caller already owns this input. Selection intake must reject
	// its shape without copying/sorting the unbounded invalid tail on the actor.
	replies := make([]string, 65536)
	for i := range replies {
		replies[i] = "same-invalid-tail.json"
	}
	selected := []ProjectArtifactSet{{ID: "cmake", CMakeIndex: "index.json", CMakeReplies: replies}}
	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := normalizeProjectSelection(selected); !errors.Is(err, ErrProjectContextSelection) {
				b.Fatal("oversized reply selection was accepted", err)
			}
		}
	})
	if got := result.AllocedBytesPerOp(); got > 4096 {
		t.Fatal("invalid selection copied an unbounded reply tail before refusing it", got)
	}
}

func TestProjectArtifactBoundedRegularReadsAndCancellation(t *testing.T) {
	root := t.TempDir()
	writeProjectContextFixture(t, root, "commands.json", "[]")
	content, stamp, err := readProjectArtifact(t.Context(), root, "commands.json", 2)
	if err != nil || string(content) != "[]" || stamp.info == nil {
		t.Fatal("exact-limit regular read failed", err)
	}
	if _, _, err := readProjectArtifact(t.Context(), root, "commands.json", 1); err == nil {
		t.Fatal("oversized artifact ignored remaining aggregate budget")
	}
	if err := os.Mkdir(filepath.Join(root, "directory"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, _, err := readProjectArtifact(t.Context(), root, "directory", 100); err == nil {
		t.Fatal("directory became a regular artifact")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := readProjectArtifact(ctx, root, "absent", 2); !errors.Is(err, context.Canceled) {
		t.Fatal("pre-cancelled observation reached filesystem/missing-file classification", err)
	}
	if err := validateProjectArtifact(ctx, root, "commands.json", stamp); !errors.Is(err, context.Canceled) {
		t.Fatal("final set validation ignored cancellation", err)
	}
	writeProjectContextFixture(t, root, "commands.json", "different bytes")
	if err := validateProjectArtifact(t.Context(), root, "commands.json", stamp); !errors.Is(err, ErrProjectContextChanged) {
		t.Fatal("artifact metadata mutation did not invalidate its retained stamp", err)
	}
}

func TestProjectArtifactRejectsSymlinkFileAndParents(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeProjectContextFixture(t, outside, "commands.json", "[]")
	testsymlink.Create(t, filepath.Join(outside, "commands.json"), filepath.Join(root, "link.json"))
	testsymlink.Create(t, outside, filepath.Join(root, "linked"))
	for _, path := range []string{"link.json", filepath.Join("linked", "commands.json")} {
		if content, _, err := readProjectArtifact(t.Context(), root, path, 100); err == nil || content != nil {
			t.Fatal("symlink allowed a project artifact read outside its selected authority", path, err)
		}
	}
}

func TestProjectArtifactNativeDuplicateIdentityDoesNotCreateTwoInputs(t *testing.T) {
	root := t.TempDir()
	writeProjectContextFixture(t, root, "one.json", "[]")
	if err := os.Link(filepath.Join(root, "one.json"), filepath.Join(root, "two.json")); err != nil {
		t.Fatal("native hard-link identity fixture unavailable (not a pass)", err)
	}
	selection := []ProjectArtifactSet{{ID: "one", CompilationDatabase: "one.json"}, {ID: "two", CompilationDatabase: "two.json"}}
	data, _, err := readProjectContext(t.Context(), root, selection)
	if !errors.Is(err, ErrProjectContextSelection) || len(data.identities) != 0 || len(data.compilation) != 0 {
		t.Fatal("distinct spellings of one native file bypassed duplicate identity rejection", err)
	}
}

func TestProjectArtifactCaseVariantsFollowNativeIdentity(t *testing.T) {
	root := t.TempDir()
	writeProjectContextFixture(t, root, "one.json", "[]")
	writeProjectContextFixture(t, root, "ONE.JSON", projectContextDatabaseFixture)
	lower, err := os.Stat(filepath.Join(root, "one.json"))
	if err != nil {
		t.Fatal(err)
	}
	upper, err := os.Stat(filepath.Join(root, "ONE.JSON"))
	if err != nil {
		t.Fatal(err)
	}
	selection := []ProjectArtifactSet{{ID: "one", CompilationDatabase: "one.json"}, {ID: "two", CompilationDatabase: "ONE.JSON"}}
	data, _, err := readProjectContext(t.Context(), root, selection)
	if os.SameFile(lower, upper) {
		if !errors.Is(err, ErrProjectContextSelection) || len(data.identities) != 0 || len(data.compilation) != 0 {
			t.Fatal("case aliases of one native file became distinct inputs", err)
		}
	} else if err != nil || len(data.identities) != 2 || data.compilation["one"].Len() != 0 || data.compilation["two"].Len() != 2 {
		t.Fatal("string case folding merged genuinely distinct native inputs", err)
	}
}

func TestProjectContextNativeCaseAliasInvalidation(t *testing.T) {
	root := t.TempDir()
	writeProjectContextFixture(t, root, "commands.json", "[]")
	lower, err := os.Stat(filepath.Join(root, "commands.json"))
	if err != nil {
		t.Fatal(err)
	}
	upper, aliasErr := os.Stat(filepath.Join(root, "COMMANDS.JSON"))
	if errors.Is(aliasErr, os.ErrNotExist) {
		// Case-sensitive storage needs an explicit native alias. A hard link
		// exercises the same file identity rather than skipping the lifecycle.
		if err := os.Link(filepath.Join(root, "commands.json"), filepath.Join(root, "COMMANDS.JSON")); err != nil {
			t.Fatal("create native case-variant alias:", err)
		}
		upper, aliasErr = os.Stat(filepath.Join(root, "COMMANDS.JSON"))
	}
	if aliasErr != nil || !os.SameFile(lower, upper) {
		t.Fatal("case-variant fixture does not alias the native source:", aliasErr)
	}
	p := NewProjectContextService(root, "workspace-1")
	t.Cleanup(p.Close)
	if err := p.Select([]ProjectArtifactSet{{ID: "db", CompilationDatabase: "COMMANDS.JSON"}}); err != nil {
		t.Fatal(err)
	}
	before := awaitProjectContext(t, p, "ready")
	writeProjectContextFixture(t, root, "commands.json", projectContextDatabaseFixture)
	p.Invalidate() // workspace ingress does not filter the delivered native spelling
	if _, ok := p.CompilationDatabase(before.Generation, "db"); ok {
		t.Fatal("native alias update retained an old database as current")
	}
	after := awaitProjectContext(t, p, "ready")
	if after.Generation <= before.Generation || after.Artifacts[0].SHA256 == before.Artifacts[0].SHA256 {
		t.Fatal("native alias invalidation failed to observe the replacement bytes")
	}
}
