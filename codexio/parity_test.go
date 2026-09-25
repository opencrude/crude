// Adapted for Crude from fgn/applypatch 95ed66f6d36a575b00001f2e27aedc1ec7455812.
// See NOTICE for provenance and modification details.
package codexio

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

// The Codex parity corpus records what the real Codex apply_patch binary did
// with each case: whether it succeeded, why it failed, and the files it left
// behind. Plain `go test` replays it against Codex mode without the binary;
// `task parity:record` rewrites it and `task parity:live` compares fresh
// generated cases against the binary.
//
// Outside the parity claim: filesystem topology (a file and a directory with
// the same name), path aliases such as "./a", absolute paths, permissions,
// symlinks, and files that are not valid UTF-8 (JSON cannot store them; Go
// tests cover how Codex mode rejects them). The generator only uses genNames.
const corpusPath = "testdata/codex-parity.json"

const (
	corpusSeed  = 1
	corpusCases = 1500
)

var update = flag.Bool("update", false, "rewrite "+corpusPath+" by running the Codex binary")

type parityCase struct {
	Name  string            `json:"name"`
	Files map[string]string `json:"files"`
	Patch string            `json:"patch"`
	OK    bool              `json:"ok"`
	// Error is the errorCategory of a failure and Stderr its first line.
	Error  string `json:"error,omitempty"`
	Stderr string `json:"stderr,omitempty"`
	// Result holds the files after the run, also after a failure.
	Result map[string]string `json:"result"`
}

type parityCorpus struct {
	CodexCommit     string       `json:"codex_commit"`
	BinarySHA256    string       `json:"binary_sha256"`
	Mode            string       `json:"mode"`
	GeneratorSHA256 string       `json:"generator_sha256"`
	GeneratorSeed   uint64       `json:"generator_seed"`
	GeneratedCases  int          `json:"generated_cases"`
	Cases           []parityCase `json:"cases"`
}

// codexBinary returns the Codex apply_patch binary, or skips the test.
func codexBinary(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("CODEX_APPLY_PATCH_BIN")
	if bin == "" {
		t.Skip("set CODEX_APPLY_PATCH_BIN to a Codex apply_patch binary")
	}
	return bin
}

// runCodex runs a case through the binary and records the outcome.
func runCodex(t *testing.T, bin string, c genCase) parityCase {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	result, err := runOracle(ctx, bin, c.Files, c.Patch)
	if err != nil {
		t.Fatalf("%s: %v", c.Name, err)
	}
	outcome := parityCase{Name: c.Name, Files: c.Files, Patch: c.Patch, OK: result.ok, Result: result.files}
	if !result.ok {
		outcome.Error = errorCategory(result.stderr)
		outcome.Stderr, _, _ = strings.Cut(result.stderr, "\n")
	}
	return outcome
}

// mismatch compares Codex mode with a recorded outcome: success, the error
// category, and the files, including those left behind by a failure.
func mismatch(want parityCase) string {
	got, err := PreserveLineEndings.ApplyFiles(want.Files, want.Patch)
	switch {
	case want.OK && err != nil:
		return fmt.Sprintf("Codex applied it, we failed: %v", err)
	case !want.OK && err == nil:
		return fmt.Sprintf("Codex failed (%s), we applied it: %q", want.Stderr, got)
	case !want.OK && errorCategory(err.Error()) != want.Error:
		return fmt.Sprintf("Codex failed with %q, we failed with %q", want.Stderr, err)
	case !maps.Equal(got, want.Result):
		return fmt.Sprintf("files differ\nwant: %q\ngot:  %q", want.Result, got)
	}
	return ""
}

func TestCodexParity(t *testing.T) {
	t.Parallel()
	if *update {
		recordCorpus(t)
	}
	raw, err := os.ReadFile(corpusPath)
	if err != nil {
		t.Fatal(err)
	}
	var corpus parityCorpus
	err = json.Unmarshal(raw, &corpus)
	if err != nil {
		t.Fatal(err)
	}
	checkCorpusInputs(t, corpus)
	for _, c := range corpus.Cases {
		problem := mismatch(c)
		if problem != "" {
			t.Errorf("%s\nfiles: %q\npatch: %q\n%s", c.Name, c.Files, c.Patch, problem)
		}
	}
}

// checkCorpusInputs fails when the corpus was recorded from other inputs than
// the current regression cases and generator produce.
func checkCorpusInputs(t *testing.T, corpus parityCorpus) {
	t.Helper()
	recorded := map[string]parityCase{}
	scenarios := 0
	for _, c := range corpus.Cases {
		recorded[c.Name] = c
		if strings.HasPrefix(c.Name, "codex scenario ") {
			scenarios++
		}
	}
	want := regressionCases()
	generator := newGenerator(corpus.GeneratorSeed)
	for i := range corpusCases {
		want = append(want, generator.patch(fmt.Sprintf("generated %d", i)))
	}
	for _, c := range want {
		got, ok := recorded[c.Name]
		if !ok || got.Patch != c.Patch || !maps.Equal(got.Files, c.Files) {
			t.Fatalf("%s: the corpus does not match the current inputs; run task parity:record", c.Name)
		}
	}
	if corpus.GeneratorSeed != corpusSeed || corpus.GeneratedCases != corpusCases || scenarios == 0 ||
		len(corpus.Cases) != len(want)+scenarios {
		t.Fatal("the corpus does not match the current inputs; run task parity:record")
	}
}

func TestCodexParityLive(t *testing.T) {
	t.Parallel()
	bin := codexBinary(t)
	checkOracle(t, bin)
	count := envInt(t, "PARITY_CASES", 5000)
	seed := envInt(t, "PARITY_SEED", int(time.Now().UnixNano()%1_000_000))
	t.Logf("PARITY_SEED=%d PARITY_CASES=%d", seed, count)
	generator := newGenerator(uint64(seed))
	outcomes := map[string]int{}
	features := map[string][2]int{}
	failures := 0
	for i := range count {
		c := runCodex(t, bin, generator.patch(fmt.Sprintf("seed %d case %d", seed, i)))
		outcomes[cmpOr(c.Error, "ok")]++
		for _, feature := range caseFeatures(c) {
			counts := features[feature]
			if c.OK {
				counts[0]++
			} else {
				counts[1]++
			}
			features[feature] = counts
		}
		problem := mismatch(c)
		if problem != "" {
			failures++
			if failures <= 10 {
				t.Errorf("%s\nfiles: %q\npatch: %q\n%s", c.Name, c.Files, c.Patch, problem)
			}
		}
	}
	t.Logf("Codex outcomes: %v", outcomes)
	for _, feature := range slices.Sorted(maps.Keys(features)) {
		t.Logf("%-22s applied %5d, rejected %5d", feature, features[feature][0], features[feature][1])
	}
	if failures > 0 {
		t.Errorf("%d of %d cases differ from Codex; rerun with PARITY_SEED=%d", failures, count, seed)
	}
}

// caseFeatures names what a case exercises, so the live test can show that
// every feature is reached by both applied and rejected patches.
func caseFeatures(c parityCase) []string {
	var features []string
	add := func(name string, present bool) {
		if present {
			features = append(features, name)
		}
	}
	files := strings.Join(slices.Collect(maps.Values(c.Files)), "\x00")
	add("CRLF file", strings.Contains(files, "\r\n"))
	add("CR file", strings.Contains(strings.ReplaceAll(files, "\r\n", ""), "\r"))
	add("typographic file", strings.ContainsAny(files, "\u2013\u2019\u201d\u00a0"))
	add("anchor", strings.Contains(c.Patch, "\n@@ "))
	add("end of file", strings.Contains(c.Patch, "*** End of File"))
	add("move", strings.Contains(c.Patch, "*** Move to: "))
	add("heredoc", strings.HasPrefix(c.Patch, "<<"))
	add("several operations", strings.Count(c.Patch, " File: ") > 1)
	return features
}

func cmpOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func envInt(t *testing.T, name string, fallback int) int {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return n
}

// checkOracle fails unless the binary reproduces the independently written
// expectations of Codex's own line-ending tests, which proves both the
// binary and the preserve-line-endings mode.
func checkOracle(t *testing.T, bin string) {
	t.Helper()
	for _, c := range upstreamCodexCases {
		patch := "*** Begin Patch\n*** Update File: f.txt\n" + c.diff + "*** End Patch"
		result, err := runOracle(t.Context(), bin, map[string]string{"f.txt": c.input}, patch)
		if err != nil {
			t.Fatal(err)
		}
		want := c.want
		if want == "" {
			want = c.input
		}
		if result.ok != (c.err == "") || result.files["f.txt"] != want {
			t.Fatalf("oracle self-check %q: got %q (ok=%v, %s), want %q", c.name,
				result.files["f.txt"], result.ok, result.stderr, want)
		}
	}
}

// recordCorpus runs the regression cases, Codex's scenario fixtures, and the
// generated cases through the binary and writes the corpus.
func recordCorpus(t *testing.T) {
	t.Helper()
	bin := codexBinary(t)
	repo := os.Getenv("CODEX_REPO")
	if repo == "" {
		t.Fatal("set CODEX_REPO to the openai/codex checkout the binary was built from")
	}
	status := git(t, repo, "status", "--porcelain", "--", "codex-rs")
	if status != "" {
		t.Fatalf("%s has uncommitted changes under codex-rs:\n%s", repo, status)
	}
	commit := git(t, repo, "rev-parse", "HEAD")
	// task parity:oracle writes the commit it built from next to the binary.
	built, err := os.ReadFile(bin + ".commit")
	if err != nil || strings.TrimSpace(string(built)) != commit {
		t.Fatalf("%s was not built from %s at %s; run task parity:oracle", bin, repo, commit)
	}
	checkOracle(t, bin)
	corpus := parityCorpus{
		CodexCommit:     commit,
		BinarySHA256:    fileSHA256(t, bin),
		Mode:            "PreserveLineEndings",
		GeneratorSHA256: fileSHA256(t, "casegen_test.go"),
		GeneratorSeed:   corpusSeed,
		GeneratedCases:  corpusCases,
	}
	cases := regressionCases()
	for _, scenario := range scenarioCases(t, repo) {
		outcome := runCodex(t, bin, scenario.genCase)
		// Validate the harness against the expectations Codex wrote.
		if !maps.Equal(outcome.Result, scenario.expected) {
			t.Fatalf("%s: oracle left %q, fixture expects %q", scenario.Name, outcome.Result, scenario.expected)
		}
		corpus.Cases = append(corpus.Cases, outcome)
	}
	generator := newGenerator(corpusSeed)
	for i := range corpusCases {
		cases = append(cases, generator.patch(fmt.Sprintf("generated %d", i)))
	}
	for _, c := range cases {
		corpus.Cases = append(corpus.Cases, runCodex(t, bin, c))
	}
	for _, c := range corpus.Cases {
		// JSON would replace invalid UTF-8, so the corpus must not need it.
		for _, text := range append(slicesOf(c.Files, c.Result), c.Patch) {
			if !utf8.ValidString(text) {
				t.Fatalf("%s: corpus text is not valid UTF-8", c.Name)
			}
		}
	}
	raw, err := json.MarshalIndent(corpus, "", "\t")
	if err != nil {
		t.Fatal(err)
	}
	err = os.WriteFile(corpusPath, append(raw, '\n'), 0o644)
	if err != nil {
		t.Fatal(err)
	}
}

func fileSHA256(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:])
}

func slicesOf(maps ...map[string]string) []string {
	var values []string
	for _, m := range maps {
		for name, value := range m {
			values = append(values, name, value)
		}
	}
	return values
}

func git(t *testing.T, repo string, args ...string) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "git", append([]string{"-C", repo}, args...)...).Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

type scenario struct {
	genCase

	expected map[string]string
}

// scenarioCases reads the scenario fixtures of a Codex checkout: files
// before, a patch, and the files Codex is expected to leave behind.
func scenarioCases(t *testing.T, repo string) []scenario {
	t.Helper()
	root := filepath.Join(repo, "codex-rs", "apply-patch", "tests", "fixtures", "scenarios")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var scenarios []scenario
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		patch, err := os.ReadFile(filepath.Join(dir, "patch.txt"))
		if err != nil {
			t.Fatal(err)
		}
		files := readTree(t, filepath.Join(dir, "input"))
		scenarios = append(scenarios, scenario{
			genCase:  genCase{Name: "codex scenario " + entry.Name(), Files: files, Patch: string(patch)},
			expected: readTree(t, filepath.Join(dir, "expected")),
		})
	}
	sort.Slice(scenarios, func(i, j int) bool { return scenarios[i].Name < scenarios[j].Name })
	return scenarios
}

// readTree reads every file under dir; a missing dir is an empty tree.
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return filepath.SkipAll
		}
		if err != nil || entry.IsDir() {
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name, err := filepath.Rel(dir, path)
		files[filepath.ToSlash(name)] = string(contents)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// regressionCases are hand-written cases for behavior the generator rarely
// reaches, and for cases that once differed from Codex.
func regressionCases() []genCase {
	update := func(name, contents string, diff ...string) genCase {
		return genCase{
			Name:  "regression " + name,
			Files: map[string]string{"f.txt": contents},
			Patch: "*** Begin Patch\n*** Update File: f.txt\n" + strings.Join(diff, "\n") + "\n*** End Patch\n",
		}
	}
	patch := func(name string, files map[string]string, lines ...string) genCase {
		return genCase{Name: "regression " + name, Files: files, Patch: strings.Join(lines, "\n")}
	}
	f := map[string]string{"f.txt": "a\n"}
	return []genCase{
		// Match precedence: each matcher scans the whole range before the next.
		update("later exact beats earlier trailing whitespace", "a \na\n", "@@", "-a", "+x"),
		update("later trailing whitespace beats earlier trim", " a\na \n", "@@", "-a", "+x"),
		update("later trim beats earlier punctuation", "a \u2013 b\n  a - b\n", "@@", "-a - b", "+x"),
		update("first of several exact matches", "a\nb\na\n", "@@", "-a", "+x"),
		update("anchor precedence", "f \nx\nf\nx\n", "@@ f", "-x", "+y"),
		// Where the result lands and what it keeps.
		update("anchored insertion goes to the end", "a\nb\nc\n", "@@ a", "+x"),
		update("fuzzy context keeps file bytes and endings", "a  \r\nb\r\n", "@@", " a", "-b", "+c"),
		update("same text removed and kept", "x\nx\nx\n", "@@", "-x", " x", "+y"),
		update("insertions around context", "a\n", "@@", "+1", "+2", " a", "+3", "+4"),
		update("insertions at one position keep order", "a\n", "@@", "+1", "@@", "+2"),
		update("EOF after an advanced cursor", "a\nb\na\nb\n", "@@", " a", "@@", "-b", "+z", "*** End of File"),
		update("EOF context not at end", "a\nb\nc\n", "@@", "-a", "+A", "*** End of File"),
		update("EOF pure insertion", "a\n", "@@", "+b", "*** End of File"),
		update("trailing empty context retry", "a\nb", "@@", " a", "-b", "+B", " "),
		update("retry down to an empty pattern", "a\n", "@@", "-", "+x"),
		update("retry down to an empty pattern after a cursor", "a\nb\n", "@@", " a", "@@", " ", "+x"),
		update("retry after context down to one line", "\n", " ", "-"),
		update("final newline added", "a", "@@", "-a", "+b"),
		update("CR only file", "a\rb\r", "@@", "-a", "+A"),
		update("mixed endings kept", "a\nb\r\nc\rd", "@@", " a", "-b", "+B", " c"),
		update("empty file insertion", "", "@@", "+a"),
		update("stacked anchors", "class A:\n  def f():\n    pass\n",
			"@@ class A:", "@@   def f():", "-    pass", "+    return"),
		update("blank lines after EOF", "a\n", "@@", "-a", "+b", "*** End of File", "", "   "),
		update("only EOF marker", "a\n", "*** End of File"),
		update("empty section", "a\n", "@@", "@@", "-a", "+b"),
		// Patch structure.
		patch("move after leading end of file", f, "*** Begin Patch", "*** Update File: f.txt",
			"*** End of File", "*** Move to: g.txt", "@@", "-a", "+b", "*** End Patch"),
		patch("carriage returns in diff lines", map[string]string{"f.txt": "a\r\n"}, "*** Begin Patch",
			"*** Update File: f.txt", "@@", "-a\r\r\r", "+b\r\r\r", "*** End Patch"),
		patch("move to itself", f, "*** Begin Patch", "*** Update File: f.txt", "*** Move to: f.txt",
			"@@", "-a", "+b", "*** End Patch"),
		patch("add then update", nil, "*** Begin Patch", "*** Add File: n.txt", "+a",
			"*** Update File: n.txt", "@@", "-a", "+b", "*** End Patch"),
		patch("empty patch", f, "*** Begin Patch", "*** End Patch"),
		patch("failure after a successful operation", f, "*** Begin Patch", "*** Add File: n.txt", "+new",
			"*** Update File: f.txt", "@@", "-a", "+b", "*** Update File: m.txt", "@@", "-x", "+y", "*** End Patch"),
		patch("invalid update after a valid one", f, "*** Begin Patch", "*** Update File: f.txt", "@@", "-a", "+b",
			"*** Update File: f.txt", "bad", "*** End Patch"),
		patch("syntax error before a missing file", f, "*** Begin Patch", "*** Update File: m.txt", "@@",
			"*** End Patch"),
	}
}
