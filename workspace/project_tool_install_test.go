package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"reflect"
	"strings"
	"testing"

	"crude/insight/catalog"
	"crude/toolstore"
	"crude/types"
)

type installFake struct {
	requests []toolstore.Request
	uses     []string
	install  func(context.Context, toolstore.Request) (toolstore.Tool, error)
}

func (f *installFake) Install(ctx context.Context, r toolstore.Request) (toolstore.Tool, error) {
	f.requests = append(f.requests, r)
	if f.install != nil {
		return f.install(ctx, r)
	}
	if r.AdmitPublication == nil {
		return toolstore.Tool{}, errors.New("missing publication admission")
	}
	if r.PreparePublication != nil {
		if err := r.PreparePublication(ctx); err != nil {
			return toolstore.Tool{}, err
		}
	}
	if err := r.AdmitPublication(ctx); err != nil {
		return toolstore.Tool{}, err
	}
	return toolstore.Tool{ID: r.Package + "/installed"}, nil
}
func (f *installFake) MarkUsed(id, project string) error {
	f.uses = append(f.uses, id+":"+project)
	return nil
}

type installAuthorityFake struct {
	check func(context.Context, string) error
	admit func(context.Context, string) error
}

func (f *installAuthorityFake) Validate(ctx context.Context, id string) error {
	if f.check != nil {
		return f.check(ctx, id)
	}
	return ctx.Err()
}

func (f *installAuthorityFake) AdmitPublication(ctx context.Context, id string) error {
	if f.admit != nil {
		return f.admit(ctx, id)
	}
	return ctx.Err()
}

func installFixture(t *testing.T) (*catalog.Catalog, types.ProjectToolReview, []string) {
	t.Helper()
	doc := catalog.Document{Format: 1}
	for _, name := range []string{"alpha", "beta"} {
		doc.Packages = append(doc.Packages, catalog.PackageRecord{
			Name: name, Licenses: []string{"MIT"},
			Source: catalog.PackageSourceRecord{ID: "pkg:github/owner/" + name + "@v1", Assets: []catalog.AssetRecord{{Targets: []string{"linux_x64_gnu"}, File: "tool.zip", Bin: "bin/serve"}}},
			Bin:    map[string]string{"serve": "{{source.asset.bin}}"},
		})
	}
	data, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	c, err := catalog.Load(nil, data)
	if err != nil {
		t.Fatal(err)
	}
	plan := types.AssemblyPlan{State: types.AssemblyReady, Generation: 3, Languages: []types.AssemblyLanguage{
		{ID: "a", ServerID: "a", Status: types.ToolMissing, Package: "alpha", Command: "serve"},
		{ID: "b", ServerID: "b", Status: types.ToolMissing, Package: "beta", Command: "serve"},
		{ID: "c", ServerID: "c", Status: types.ToolMissing, Package: "unknown", Command: "serve"},
	}}
	r, err := buildProjectToolReview("project", "root", plan, c, []string{"linux_x64_gnu"})
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, item := range r.Items {
		if item.Installable {
			ids = append(ids, item.ID)
		}
	}
	return c, r, ids
}

func TestReviewedProjectInstall(t *testing.T) {
	c, review, ids := installFixture(t)
	limits := projectToolInstallLimits{100, 200, 3}
	authority := &installAuthorityFake{}
	run := func(ctx context.Context, f *installFake, r types.ProjectToolReview, ids []string, approval, project string) ([]projectToolInstallResult, error) {
		return installReviewedProjectToolsWithCatalog(ctx, f, authority, r, ids, approval, project, limits, c, []string{"linux_x64_gnu"})
	}
	f := &installFake{}
	results, err := run(context.Background(), f, review, ids[:1], "approval", "project")
	if err != nil || len(results) != 1 || !results[0].Attempted || len(f.requests) != 1 || len(f.uses) != 0 {
		t.Fatalf("install: %+v, %v, %+v", results, err, f)
	}
	item := func() types.ProjectToolCandidate {
		for _, item := range review.Items {
			if item.ID == ids[0] {
				return item
			}
		}
		t.Fatal("missing fixture")
		return types.ProjectToolCandidate{}
	}()
	want := toolstore.Request{Package: item.Package, Version: item.Version, Source: item.Source, URL: item.URL, Archive: item.Archive, Bin: item.Bin, Command: item.Command, Licenses: item.Licenses, InstalledBy: "user", Approval: "approval", MaxDownloadBytes: 100, MaxExtractedBytes: 200, MaxEntries: 3}
	got := f.requests[0]
	if got.AdmitPublication == nil {
		t.Fatal("reviewed install omitted the publication fence")
	}
	got.AdmitPublication = nil
	got.PreparePublication = nil
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("request/usage: %+v %v", f.requests[0], f.uses)
	}
	unsupported := ""
	for _, item := range review.Items {
		if !item.Installable {
			unsupported = item.ID
		}
	}
	tampered := types.CloneProjectToolReview(review)
	tampered.Items[0].URL = "https://example.com/evil"
	forged := types.CloneProjectToolReview(review)
	for i := range forged.Items {
		if forged.Items[i].ID == ids[0] {
			forged.Items[i].URL = "https://example.com/evil"
			forged.Items[i].ID = ""
			forged.Items[i].ID = reviewHash(forged.Items[i])
			ids = append([]string(nil), ids...)
			ids[0] = forged.Items[i].ID
			break
		}
	}
	forged.Token = ""
	forged.Token = reviewHash(forged)
	_, original, originalIDs := installFixture(t)
	for _, tc := range []struct {
		name, approval, project string
		review                  types.ProjectToolReview
		ids                     []string
	}{
		{"tamper", "approval", "project", tampered, originalIDs},
		{"forged hashes", "approval", "project", forged, ids},
		{"token", "approval", "project", types.ProjectToolReview{Project: "project", RootIdentity: "root", Token: "bad"}, originalIDs},
		{"duplicate", "approval", "project", original, []string{originalIDs[0], originalIDs[0]}},
		{"unknown", "approval", "project", original, []string{"unknown"}},
		{"unsupported", "approval", "project", original, []string{unsupported}},
		{"empty", "approval", "project", original, nil},
		{"approval", "", "project", original, originalIDs},
		{"project", "approval", "other", original, originalIDs},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &installFake{}
			if _, err := run(context.Background(), f, tc.review, tc.ids, tc.approval, tc.project); err == nil || len(f.requests) != 0 {
				t.Fatalf("accepted invalid review: %v %+v", err, f.requests)
			}
		})
	}
	t.Run("cancel before", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		f := &installFake{}
		res, err := run(ctx, f, original, originalIDs, "approval", "project")
		if !errors.Is(err, context.Canceled) || len(f.requests) != 0 || len(res) != 2 || res[0].Attempted {
			t.Fatalf("cancel: %+v %v", res, err)
		}
	})
	t.Run("partial offline", func(t *testing.T) {
		offline := &net.DNSError{Err: "offline", Name: "host"}
		f := &installFake{}
		f.install = func(_ context.Context, r toolstore.Request) (toolstore.Tool, error) {
			if len(f.requests) == 2 {
				return toolstore.Tool{}, offline
			}
			return toolstore.Tool{ID: r.Package + "/installed"}, nil
		}
		res, err := run(context.Background(), f, original, originalIDs, "approval", "project")
		if !errors.Is(err, offline) || len(f.requests) != 2 || len(f.uses) != 0 || !strings.Contains(res[1].Message, "Check the network connection") {
			t.Fatalf("partial: %+v %v", res, err)
		}
	})
	t.Run("cancel during", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		f := &installFake{install: func(context.Context, toolstore.Request) (toolstore.Tool, error) {
			cancel()
			return toolstore.Tool{}, context.Canceled
		}}
		res, err := run(ctx, f, original, originalIDs, "approval", "project")
		if !errors.Is(err, context.Canceled) || len(f.requests) != 1 || res[1].Attempted || len(f.uses) != 0 {
			t.Fatalf("cancel: %+v %v", res, err)
		}
	})
}

func TestReviewedProjectInstallRequiresCurrentEffectAuthority(t *testing.T) {
	c, review, ids := installFixture(t)
	limits := projectToolInstallLimits{100, 200, 3}
	f := &installFake{}
	if _, err := installReviewedProjectToolsWithCatalog(t.Context(), f, nil, review, ids, "receipt", "project", limits, c, []string{"linux_x64_gnu"}); err == nil || len(f.requests) != 0 {
		t.Fatal("missing effect authority started an install", err)
	}

	revoked := errors.New("project changed")
	t.Run("revoked before download", func(t *testing.T) {
		f := &installFake{}
		a := &installAuthorityFake{check: func(context.Context, string) error { return revoked }}
		res, err := installReviewedProjectToolsWithCatalog(t.Context(), f, a, review, ids, "receipt", "project", limits, c, []string{"linux_x64_gnu"})
		if !errors.Is(err, revoked) || len(f.requests) != 0 || res[0].Attempted || len(f.uses) != 0 {
			t.Fatalf("revoked project started an install: %+v %v", res, err)
		}
	})
	t.Run("revoked before publication", func(t *testing.T) {
		f := &installFake{}
		var admitted string
		a := &installAuthorityFake{admit: func(_ context.Context, id string) error {
			admitted = id
			return revoked
		}}
		res, err := installReviewedProjectToolsWithCatalog(t.Context(), f, a, review, ids, "receipt", "project", limits, c, []string{"linux_x64_gnu"})
		if !errors.Is(err, revoked) || admitted != ids[0] || len(f.requests) != 1 || res[1].Attempted || len(f.uses) != 0 {
			t.Fatalf("revoked publication continued: %+v %v", res, err)
		}
	})
	t.Run("revoked after publication", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		f := &installFake{install: func(ctx context.Context, r toolstore.Request) (toolstore.Tool, error) {
			if err := r.AdmitPublication(ctx); err != nil {
				return toolstore.Tool{}, err
			}
			cancel()
			return toolstore.Tool{ID: r.Package + "/installed"}, nil
		}}
		res, err := installReviewedProjectToolsWithCatalog(ctx, f, &installAuthorityFake{}, review, ids, "receipt", "project", limits, c, []string{"linux_x64_gnu"})
		if !errors.Is(err, context.Canceled) || res[0].Tool.ID == "" || res[1].Attempted || len(f.uses) != 0 || !strings.Contains(res[0].Message, "Review the tools again") {
			t.Fatalf("stale publication was treated as usable: %+v %v", res, err)
		}
	})
	t.Run("cancellation during post-install validation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		checks := 0
		a := &installAuthorityFake{check: func(context.Context, string) error {
			checks++
			if checks == 3 {
				cancel()
			}
			return nil
		}}
		f := &installFake{}
		res, err := installReviewedProjectToolsWithCatalog(ctx, f, a, review, ids, "receipt", "project", limits, c, []string{"linux_x64_gnu"})
		if !errors.Is(err, context.Canceled) || checks != 3 || res[0].Tool.ID == "" || res[1].Attempted || len(f.uses) != 0 {
			t.Fatalf("cancelled validation was reported as success: %+v %v", res, err)
		}
	})
	t.Run("unconfirmed publication is not a retry instruction", func(t *testing.T) {
		cause := &toolstore.PublicationUnconfirmedError{ToolID: "fixture/build", Err: &net.DNSError{Err: "offline", Name: "host"}}
		f := &installFake{install: func(context.Context, toolstore.Request) (toolstore.Tool, error) {
			return toolstore.Tool{ID: "fixture/build"}, cause
		}}
		res, err := installReviewedProjectToolsWithCatalog(t.Context(), f, &installAuthorityFake{}, review, ids, "receipt", "project", limits, c, []string{"linux_x64_gnu"})
		if !errors.Is(err, cause) || !strings.Contains(res[0].Message, "cannot confirm") || strings.Contains(res[0].Message, "network connection") || res[1].Attempted || len(f.uses) != 0 {
			t.Fatalf("unconfirmed publication offered a blind retry: %+v %v", res, err)
		}
	})
}
