package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/types"
	"crude/workspace"
)

func TestStartupNavigationNeverConstructsWorkspace(t *testing.T) {
	home := t.TempDir()
	setCoreTestHome(t, home)
	desktop := filepath.Join(home, "Desktop")
	project := filepath.Join(desktop, "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	var factoryCalls atomic.Int32
	r, err := New(func(string) (*workspace.Workspace, error) {
		factoryCalls.Add(1)
		return nil, errors.New("fixture factory must not run")
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	c, err := r.Connect("startup-navigation")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, root := range []string{home, desktop, filepath.VolumeName(home) + string(filepath.Separator)} {
		out := callCore(t, c, "inspect", "", coreprotocol.StartupInspect, coreprotocol.StartupInspectRequest{Root: root})
		var snapshot coreprotocol.StartupSnapshot
		if err := json.Unmarshal(out.Payload, &snapshot); err != nil {
			t.Fatal(err)
		}
		if snapshot.FolderKind == "" || snapshot.PathError != "" {
			t.Fatalf("not classified: %+v", snapshot)
		}
		raw, _ := json.Marshal(coreprotocol.AttachRequest{Root: root})
		attached := c.Call(t.Context(), coreprotocol.Request{ID: "forbidden-attach", Owner: c.Owner, Method: coreprotocol.Attach, Payload: raw})
		if attached.Error == nil || attached.Error.Code != "project_folder_required" {
			t.Fatalf("top-level attached: %+v", attached)
		}
	}
	var browse coreprotocol.StartupBrowseResult
	result := callCore(t, c, "browse", "", coreprotocol.StartupBrowse, coreprotocol.StartupBrowseRequest{Path: desktop})
	if err := json.Unmarshal(result.Payload, &browse); err != nil {
		t.Fatal(err)
	}
	if len(browse.Entries) != 1 || browse.Entries[0].Path != project {
		t.Fatalf("browse: %+v", browse)
	}
	var catalog workspace.BrowserCatalog
	result = callCore(t, c, "catalog", "", coreprotocol.StartupCatalog, coreprotocol.StartupCatalogRequest{Root: desktop})
	if err := json.Unmarshal(result.Payload, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Entries) != 1 || catalog.Entries[0].Path != project {
		t.Fatalf("catalog: %+v", catalog)
	}
	if factoryCalls.Load() != 0 || len(r.workspaces) != 0 || len(r.creating) != 0 {
		t.Fatal("navigation constructed project services")
	}
}

func TestStartupProjectAndLinkAdmission(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	project := t.TempDir()
	var calls atomic.Int32
	r, err := New(func(root string) (*workspace.Workspace, error) {
		calls.Add(1)
		return nil, errors.New("selected project reached factory")
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	c, _ := r.Connect("project-admission")
	defer c.Close()
	for _, root := range []string{project, filepath.Join(project, "missing")} {
		raw, _ := json.Marshal(coreprotocol.AttachRequest{Root: root})
		result := c.Call(t.Context(), coreprotocol.Request{ID: "attach", Owner: c.Owner, Method: coreprotocol.Attach, Payload: raw})
		if result.Error == nil {
			t.Fatal("fixture unexpectedly attached")
		}
		if root == project && result.Error.Code != "workspace_unavailable" {
			t.Fatalf("project rejected before factory: %v", result.Error)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("factory calls=%d", calls.Load())
	}
	alias := filepath.Join(t.TempDir(), "home-alias")
	home, _ := os.UserHomeDir()
	if err := os.Symlink(home, alias); err != nil {
		return
	}
	if _, err := inspectProject(alias); err == nil {
		t.Fatal("home alias admitted")
	}
}

func TestStartupCatalogStopsWithinFlatDirectoryBudget(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 10020; i++ {
		if err := os.WriteFile(filepath.Join(root, fmt.Sprintf("file-%05d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	cat, err := startupCatalog(t.Context(), root, false)
	if err != nil {
		t.Fatal(err)
	}
	if !cat.Truncated || cat.Visited != 10000 || len(cat.Entries) != 0 {
		t.Fatalf("budget not truthful: %+v", cat)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := startupCatalog(ctx, root, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel=%v", err)
	}
}

type blockingStartupOwner struct{ started chan struct{} }

func (b blockingStartupOwner) StartupSnapshot(ctx context.Context) (coreprotocol.StartupSnapshot, error) {
	close(b.started)
	<-ctx.Done()
	return coreprotocol.StartupSnapshot{}, ctx.Err()
}
func (b blockingStartupOwner) SaveStartup(context.Context, []types.SettingEdit) (types.OnboardingSettings, error) {
	return types.OnboardingSettings{}, nil
}

func (b blockingStartupOwner) ReadStartupSettings(context.Context) (types.AppSettings, error) {
	return types.AppSettings{}, nil
}

func TestStartupCloseCancelsAndJoinsRead(t *testing.T) {
	r, err := New(func(string) (*workspace.Workspace, error) {
		t.Error("unexpected factory")
		return nil, errors.New("unexpected")
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	owner := blockingStartupOwner{started: make(chan struct{})}
	if err := r.SetStartupOwner(owner); err != nil {
		t.Fatal(err)
	}
	c, _ := r.Connect("closing-startup")
	raw, _ := json.Marshal(coreprotocol.StartupInspectRequest{Root: t.TempDir()})
	result := c.Dispatch(context.Background(), coreprotocol.Request{ID: "read", Owner: c.Owner, Method: coreprotocol.StartupInspect, Payload: raw})
	<-owner.started
	done := make(chan error, 1)
	go func() { done <- r.Close() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not cancel startup worker")
	}
	if out := <-result; out.Error == nil {
		t.Fatal("cancelled startup read succeeded")
	}
}
