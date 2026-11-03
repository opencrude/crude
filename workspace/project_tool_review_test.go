package workspace

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"crude/insight/catalog"
	"crude/types"
)

func reviewCatalog(t *testing.T, source, file, bin, license string) *catalog.Catalog {
	t.Helper()
	doc := catalog.Document{Format: 1, Packages: []catalog.PackageRecord{{
		Name: "fixture", Licenses: []string{license},
		Source: catalog.PackageSourceRecord{ID: source, Assets: []catalog.AssetRecord{{Targets: []string{"linux_x64_gnu"}, File: file, Bin: bin}}},
		Bin:    map[string]string{"serve": "{{source.asset.bin}}"},
	}}}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Load(nil, data)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestProjectToolReview(t *testing.T) {
	c := reviewCatalog(t, "pkg:github/owner/repo@v1", "tool.zip", "bin/serve", "MIT")
	targets := []string{"linux_x64_gnu"}
	plan := types.AssemblyPlan{State: types.AssemblyReady, Generation: 9, Languages: []types.AssemblyLanguage{
		{ID: "b", ServerID: "server-b", Status: types.ToolMissing, Package: "fixture", Command: "serve"},
		{ID: "a", ServerID: "server-a", Status: types.ToolMissing, Package: "fixture", Command: "serve"},
		{ID: "c", ServerID: "other", Status: types.ToolMissing, Package: "unknown", Command: "other"},
		{ID: "d", Status: types.ToolReady},
	}}
	build := func(p types.AssemblyPlan, cat *catalog.Catalog, root string) types.ProjectToolReview {
		t.Helper()
		r, err := buildProjectToolReview("project", root, p, cat, targets)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	got := build(plan, c, "root")
	if len(got.Items) != 2 || got.Token == "" {
		t.Fatalf("review: %+v", got)
	}
	var installed, unsupported types.ProjectToolCandidate
	for _, item := range got.Items {
		if item.Installable {
			installed = item
		} else {
			unsupported = item
		}
	}
	if !reflect.DeepEqual(installed.Languages, []string{"a", "b"}) || !reflect.DeepEqual(installed.ServerIDs, []string{"server-a", "server-b"}) ||
		installed.URL != "https://github.com/owner/repo/releases/download/v1/tool.zip" || installed.Bin != "bin/serve" ||
		installed.SizeKnown || installed.ChecksumPublished || !reflect.DeepEqual(installed.Licenses, []string{"MIT"}) {
		t.Fatalf("download: %+v", installed)
	}
	if !strings.Contains(unsupported.Reason, "Install it yourself") || unsupported.Package != "unknown" {
		t.Fatalf("unsupported: %+v", unsupported)
	}
	reordered := types.CloneAssemblyPlan(plan)
	reordered.Languages[0], reordered.Languages[2] = reordered.Languages[2], reordered.Languages[0]
	if other := build(reordered, c, "root"); !reflect.DeepEqual(got, other) {
		t.Fatalf("order changed review: %+v", other)
	}
	clone := types.CloneProjectToolReview(got)
	clone.Items[0].Languages[0] = "changed"
	if reflect.DeepEqual(got, clone) {
		t.Fatal("clone aliases original")
	}
	changedPlan := types.CloneAssemblyPlan(plan)
	changedPlan.Generation++
	for _, changed := range []types.ProjectToolReview{
		build(plan, c, "other"), build(changedPlan, c, "root"),
		build(plan, reviewCatalog(t, "pkg:github/owner/repo@v2", "tool.zip", "bin/serve", "MIT"), "root"),
		build(plan, reviewCatalog(t, "pkg:github/owner/repo@v1", "other.zip", "bin/serve", "MIT"), "root"),
		build(plan, reviewCatalog(t, "pkg:github/owner/repo@v1", "tool.zip", "bin/other", "MIT"), "root"),
		build(plan, reviewCatalog(t, "pkg:github/owner/repo@v1", "tool.zip", "bin/serve", "Apache-2.0"), "root"),
	} {
		if changed.Token == got.Token {
			t.Fatal("changed preimage has same token")
		}
	}
	for _, bad := range []types.AssemblyPlan{{}, {State: types.AssemblyReady, Limited: true}} {
		if _, err := buildProjectToolReview("project", "root", bad, c, targets); err == nil {
			t.Fatalf("accepted incomplete plan %+v", bad)
		}
	}
	if _, err := buildProjectToolReview("", "root", plan, c, targets); err == nil {
		t.Fatal("accepted empty project")
	}
	if _, err := buildProjectToolReview("project", "", plan, c, targets); err == nil {
		t.Fatal("accepted empty root")
	}
	empty := build(types.AssemblyPlan{State: types.AssemblyReady}, c, "root")
	if len(empty.Items) != 0 || empty.Token == "" {
		t.Fatalf("empty review: %+v", empty)
	}
	unsupportedCatalog := reviewCatalog(t, "pkg:npm/fixture@v1", "tool.zip", "bin/serve", "MIT")
	u := build(plan, unsupportedCatalog, "root")
	for _, item := range u.Items {
		if item.Installable || !strings.Contains(item.Reason, "Install it yourself") {
			t.Fatalf("unsupported form: %+v", item)
		}
	}
}

func TestProjectToolReviewNamesPackageManagerPrerequisites(t *testing.T) {
	c, err := catalog.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	plan := types.AssemblyPlan{State: types.AssemblyReady, Generation: 10, Languages: []types.AssemblyLanguage{{
		ID: "fixture", ServerID: "fixture", Status: types.ToolMissing,
		Package: "pyright", Command: "pyright-langserver",
	}}}
	review, err := buildProjectToolReview("project", "root", plan, c, catalog.CurrentTargets())
	if err != nil || len(review.Items) != 1 {
		t.Fatalf("package manager review: %+v %v", review, err)
	}
	item := review.Items[0]
	if item.Installable || item.Version != "1.1.414" || item.Source != "npm registry" || item.URL != "pkg:npm/pyright@1.1.414" ||
		item.Bin != "npm:pyright-langserver" ||
		!strings.Contains(item.Reason, "Node.js") || !strings.Contains(item.Reason, "npm") {
		t.Fatalf("package manager prerequisite was not explained accurately: %+v", item)
	}
}

func TestProjectToolReviewTracksPackageManagerRecipeChanges(t *testing.T) {
	plan := types.AssemblyPlan{State: types.AssemblyReady, Generation: 8, Languages: []types.AssemblyLanguage{{
		ID: "fixture", ServerID: "fixture", Status: types.ToolMissing, Package: "fixture", Command: "serve",
	}}}
	build := func(source, program string) types.ProjectToolReview {
		t.Helper()
		doc := catalog.Document{Format: 1, Packages: []catalog.PackageRecord{{
			Name: "fixture", Source: catalog.PackageSourceRecord{ID: source},
			Bin: map[string]string{"serve": program},
		}}}
		data, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		c, err := catalog.Load(nil, data)
		if err != nil {
			t.Fatal(err)
		}
		review, err := buildProjectToolReview("project", "root", plan, c, nil)
		if err != nil || len(review.Items) != 1 {
			t.Fatalf("review: %+v %v", review, err)
		}
		return review
	}
	first := build("pkg:pypi/fixture@1.0?extra=one", "pypi:serve")
	for _, changed := range []types.ProjectToolReview{
		build("pkg:pypi/fixture@1.0?extra=two", "pypi:serve"),
		build("pkg:pypi/fixture@1.0?extra=one", "pypi:other"),
	} {
		if changed.Items[0].Installable || changed.Items[0].ID == first.Items[0].ID || changed.Token == first.Token {
			t.Fatalf("changed recipe reused an earlier review: %+v", changed)
		}
	}
}
