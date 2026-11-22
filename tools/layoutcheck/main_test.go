package main

import (
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestRepositoryFollowsTheLayoutRules(t *testing.T) {
	findings, err := run(filepath.Join("..", ".."), allowData)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) > 0 {
		var out bytes.Buffer
		report(&out, findings)
		t.Fatalf("the repository breaks the layout rules:\n%s", out.String())
	}
}

// rules returns "RULE place" for each finding, in order.
func rules(c *checker) []string {
	var out []string
	for _, f := range c.findings {
		out = append(out, f.rule+" "+f.place)
	}
	return out
}

func TestTreeRulesReportTheFileAndTheRule(t *testing.T) {
	c := newChecker(".")
	c.files = []string{
		"main.go",
		"widgets/widget.go",                     // not a top-level entry
		"agent/helper.go",                       // file in a group folder
		"tui/desktopnotify/toast.go.5086887",    // editor copy
		"tui/ui/findReplace.go",                 // mixedCaps file name
		"third_party/upstream/CRUDE.md",         // fine
		"third_party/upstream/LICENSE",          // fine
		"third_party/fork/LICENSE",              // no CRUDE.md
		"third_party/fork/CRUDE_FORK.md",        // wrong note name
		"tui/ui/testdata/ignoredName_Here.go",   // fixtures are exempt
		"internal/logger/logger.go",             // fine
		"third_party/upstream/someFile_Name.go", // vendored code is exempt
		"agent/turnloop/util.go",                // catch-all name
		"agent/toolstore/helpers_test.go",       // test fixtures may share a file
	}
	c.checkTree()
	c.checkThirdParty()
	got := strings.Join(rules(c), "\n")
	for _, want := range []string{
		"LAYOUT-TOP widgets",
		"LAYOUT-GROUP agent/helper.go",
		"FILE-STRAY tui/desktopnotify/toast.go.5086887",
		"FILE-NAME tui/ui/findReplace.go",
		"THIRD-PARTY third_party/fork",
		"THIRD-PARTY third_party/fork/CRUDE_FORK.md",
		"FILE-NAME agent/turnloop/util.go",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, unwanted := range []string{"testdata", "third_party/upstream", "internal/logger", "helpers_test.go"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("reported %q:\n%s", unwanted, got)
		}
	}
	for _, f := range c.findings {
		if f.rule == "FILE-NAME" && f.place == "tui/ui/findReplace.go" && !strings.Contains(f.fix, "find_replace.go") {
			t.Errorf("FILE-NAME fix does not suggest the new name: %q", f.fix)
		}
	}
}

func TestPlatformFilesNameTheirPlatform(t *testing.T) {
	for _, tc := range []struct {
		name, src string
		bad       bool
	}{
		{"backend_linux.go", "//go:build linux\n\npackage p\n", false},
		{"backend_other.go", "//go:build !linux && !windows\n\npackage p\n", false},
		{"engine_nocgo.go", "//go:build !cgo\n\npackage p\n", false},
		{"darwin.go", "//go:build darwin\n\npackage p\n", true},        // no underscore: no constraint
		{"unsupported.go", "//go:build !windows\n\npackage p\n", true}, // says nothing about the platform
		{"icon.go", "package p\n", false},                              // not platform code
		{"cancel_e2e_test.go", "//go:build linux\n\npackage p\n", false},
	} {
		c := newChecker(".")
		c.checkPlatform("pkg/"+tc.name, tc.name, []byte(tc.src))
		if got := len(c.findings) > 0; got != tc.bad {
			t.Errorf("%s: reported=%v, want %v (%v)", tc.name, got, tc.bad, rules(c))
		}
	}
}

func TestExportedNamesDoNotRepeatThePackage(t *testing.T) {
	src := `package plan

import "crude/types/setting"

type Plan struct{}         // the package's own type: allowed (ring.Ring)
type PlanStatus string     // repeats
type Planner interface{}   // a different word: allowed
func NewPlan() *Plan       { return nil }
func PlanTitle() string    { return "" } // repeats
func (p *Plan) GetOwner() string { return "" } // getter
func GetDefault() int      { return 0 }       // getter
func Getaway() int         { return 0 }       // not a getter
type Store = setting.Store // alias
var PlanLimit = 3          // repeats
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "agent/plan/model.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	c := newChecker(".")
	c.checkDecls(fset, f, "agent/plan/model.go", "plan")
	var got []string
	for _, f := range c.findings {
		got = append(got, f.rule+" "+f.name)
	}
	want := "NAME-REPEAT PlanStatus\nNAME-REPEAT PlanTitle\nNAME-GETTER Plan.GetOwner\nNAME-GETTER GetDefault\nNAME-ALIAS Store\nNAME-REPEAT PlanLimit"
	if strings.Join(got, "\n") != want {
		t.Fatalf("findings:\n%s\nwant:\n%s", strings.Join(got, "\n"), want)
	}
	if fix := c.findings[0].fix; !strings.Contains(fix, "Rename it to Status") {
		t.Fatalf("NAME-REPEAT fix = %q", fix)
	}
}

func TestPackageRules(t *testing.T) {
	c := newChecker(".")
	c.checkName(&pkg{ImportPath: "crude/lsp/client", Name: "lspclient"}, "lsp/client", "client")
	c.checkName(&pkg{ImportPath: "crude/types", Name: "types"}, "types", "types")
	c.checkName(&pkg{ImportPath: "crude/text/tab_writer", Name: "tab_writer"}, "text/tab_writer", "tab_writer")
	c.checkName(&pkg{ImportPath: "crude/cmd/tool", Name: "main"}, "cmd/tool", "tool")
	got := strings.Join(rules(c), "\n")
	want := "NAME-FOLDER lsp/client\nNAME-MEANING types\nNAME-PACKAGE text/tab_writer"
	if got != want {
		t.Fatalf("findings:\n%s\nwant:\n%s", got, want)
	}
}

func TestChildPackagesBelongToTheirParent(t *testing.T) {
	c := newChecker(".")
	for _, p := range []*pkg{
		{ImportPath: "crude/core", Name: "core", Imports: []string{"crude/core/wire"}},
		{ImportPath: "crude/core/wire", Name: "wire"},                                           // part: the parent imports it
		{ImportPath: "crude/core/client", Name: "client", Imports: []string{"crude/core/wire"}}, // extension: builds on a part
		{ImportPath: "crude/core/wiretest", Name: "wiretest", Imports: []string{"crude/core"}},  // extension: builds on the parent
		{ImportPath: "crude/core/memory", Name: "memory"},                                       // unrelated
	} {
		c.pkgs[p.ImportPath] = p
	}
	c.checkChildren(c.pkgs["crude/core"], "core")
	if got := strings.Join(rules(c), "\n"); got != "LAYOUT-CHILD core/memory" {
		t.Fatalf("findings:\n%s", got)
	}
}

func TestInternalPackagesNeverImportARuntimeLayer(t *testing.T) {
	c := newChecker(".")
	c.checkInternal(&pkg{ImportPath: "crude/internal/retry", Imports: []string{"crude/internal/logger", "context"}}, "internal/retry")
	c.checkInternal(&pkg{ImportPath: "crude/internal/bad", Imports: []string{"crude/core/corewire"}}, "internal/bad")
	if got := strings.Join(rules(c), "\n"); got != "LAYOUT-INTERNAL internal/bad" {
		t.Fatalf("findings:\n%s", got)
	}
}

func TestAllowListExcusesOnceAndFailsWhenStale(t *testing.T) {
	allow, err := parseAllow("# comment\nFILE-STRAY\ttui/keep.go.1\t-\tA reviewed reason.\nFILE-STRAY\tgone.go.1\t-\tStale.\n")
	if err != nil {
		t.Fatal(err)
	}
	c := newChecker(".")
	c.allow = allow
	c.files = []string{"tui/keep.go.1", "main.go"}
	c.checkTree()
	c.checkAllowUsed()
	got := strings.Join(rules(c), "\n")
	if got != "ALLOW-UNUSED tools/layoutcheck/allow.txt" {
		t.Fatalf("findings:\n%s", got)
	}
	if _, err := parseAllow("FILE-STRAY\tx\t-\t\n"); err == nil {
		t.Fatal("an exception without a reason was accepted")
	}
}

func TestReportNamesTheFixAndTheRuleSection(t *testing.T) {
	var out bytes.Buffer
	code := report(&out, []finding{{rule: "NAME-REPEAT", place: "agent/plan/model.go:16", name: "PlanStatus",
		message: "plan.PlanStatus repeats the package name", fix: "Rename it to Status; callers already write plan."}})
	want := "agent/plan/model.go:16: NAME-REPEAT: plan.PlanStatus repeats the package name\n" +
		"    fix: Rename it to Status; callers already write plan.\n" +
		"    rule: docs/reference/code-layout.md#name-repeat\n"
	if code != 1 || !strings.HasPrefix(out.String(), want) {
		t.Fatalf("code=%d output:\n%s", code, out.String())
	}
	out.Reset()
	if report(&out, nil) != 0 || out.String() != "layoutcheck: OK\n" {
		t.Fatalf("clean report = %q", out.String())
	}
}

// Every rule the gate can report links to a section that explains it.
func TestEveryRuleHasAReferenceSection(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(reference)))
	if err != nil {
		t.Fatal(err)
	}
	ids := regexp.MustCompile(`(?:c\.add\(|finding\{)"([A-Z]+-[A-Z]+)"`).FindAllSubmatch(src, -1)
	if len(ids) < 10 {
		t.Fatalf("found only %d rule IDs in main.go", len(ids))
	}
	for _, m := range ids {
		anchor := `<a id="` + strings.ToLower(string(m[1])) + `"></a>`
		if !bytes.Contains(doc, []byte(anchor)) {
			t.Errorf("%s has no section in %s (want %s)", m[1], reference, anchor)
		}
	}
}
