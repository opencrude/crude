package core_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"crude/app"
	"crude/core"
	"crude/coreprotocol"
	"crude/network"
	"crude/types"
	"crude/workspace"
)

type startupFuzzLogger struct{}

func (startupFuzzLogger) Info(string, string, ...any)  {}
func (startupFuzzLogger) Warn(string, string, ...any)  {}
func (startupFuzzLogger) Error(string, string, ...any) {}
func (startupFuzzLogger) Debug(string, string, ...any) {}

// This external test package deliberately uses app.App, not a stand-in startup
// owner: core cannot import app in production. Each input has two temporary roots,
// at most eight semantic actions, real SQLite/catalog owners, and no model calls.
func FuzzCoreStartupWorkspaces(f *testing.F) {
	// Keep literal URI delimiters in the profile path in native fuzz workers too,
	// not just t.TempDir's seed#N names: catalog/deletion must address the same DB.
	f.Add(uint8(1), []byte{11})
	f.Add(uint8(1), []byte{4, 6, 7})
	f.Add(uint8(0), []byte{0, 1, 2, 3, 4, 4, 5, 11})
	f.Add(uint8(1), []byte{4, 6, 7, 8, 4, 6, 7, 8})
	f.Add(uint8(2), []byte{4, 7, 6, 7, 4, 8, 5, 11})
	f.Add(uint8(3), []byte{0, 1, 2, 3, 9, 5, 10, 11})
	f.Add(uint8(4), []byte{24, 25, 26, 27, 9, 10, 17, 11})
	f.Add(uint8(0), []byte{1, 25, 26, 0, 24, 2, 29, 39})
	f.Add(uint8(8), []byte{0, 1, 2, 3, 4, 6, 7, 8})
	f.Fuzz(func(t *testing.T, profile uint8, sequence []byte) {
		home, projects := filepath.Join(t.TempDir(), "profile#literal"), t.TempDir()
		if err := os.Mkdir(home, 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("CRUDE_HOME", home)
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		previousNetwork := network.Current()
		t.Cleanup(func() {
			if err := network.ConfigureDefault(previousNetwork); err != nil {
				t.Error(err)
			}
		})
		roots := [2]string{filepath.Join(projects, "current"), filepath.Join(projects, "target")}
		for _, root := range roots {
			if err := os.Mkdir(root, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "project.txt"), []byte("project content\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		config := app.ConfigPath()
		initial := []string{"", `{}`, `{"editor":{"tab_size":7},"future":{"keep":[1,2]}}`, `{"onboarding":`, `{"accounts":{"fixture":{"id":"fixture","label":"Saved account"}}}`}[profile%5]
		if initial != "" {
			if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(config, []byte(initial), 0600); err != nil {
				t.Fatal(err)
			}
		}
		newOwner := func() *app.App {
			t.Helper()
			a, err := app.New(app.Opts{Home: home, FC: app.LoadFileConfig()})
			if err != nil {
				t.Fatal(err)
			}
			return a
		}
		owner, observer := newOwner(), newOwner()
		r, err := core.New(func(root string) (*workspace.Workspace, error) {
			return workspace.NewWorkspace(root, "", "startup-fuzzer", types.Settings{}, startupFuzzLogger{}), nil
		}, filepath.Join(home, "receipts.db"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := r.Close(); err != nil {
				t.Error(err)
			}
		})
		hasOwner := profile&8 == 0
		if hasOwner {
			if err := r.SetStartupOwner(owner); err != nil {
				t.Fatal(err)
			}
		}
		c, err := r.Connect("startup-semantic")
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		serial := 0
		call := func(ws, method string, input coreprotocol.Value, output coreprotocol.Target, faultCode string) coreprotocol.Response {
			t.Helper()
			serial++
			payload, err := coreprotocol.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			result := c.Call(ctx, coreprotocol.Request{ID: fmt.Sprintf("startup-%d", serial), Owner: c.Owner, WorkspaceID: ws, Method: method, Payload: payload})
			if faultCode != "" {
				if result.Error == nil || result.Error.Code != faultCode {
					t.Fatalf("%s: want fault %s, got %+v", method, faultCode, result.Error)
				}
			} else if result.Error != nil {
				t.Fatalf("%s: %+v", method, result.Error)
			} else if output != nil {
				if err := coreprotocol.Decode(result.Payload, output); err != nil {
					t.Fatal(err)
				}
			}
			return result
		}
		// The oracle is a sparse JSON map, not ApplySettingsEdits or a mirrored
		// settings owner. Explicit false and absence remain distinguishable.
		wantConfig := map[string]any{}
		malformed := profile%5 == 3
		if initial != "" && !malformed {
			if err := json.Unmarshal([]byte(initial), &wantConfig); err != nil {
				t.Fatal(err)
			}
		}
		configExists := initial != ""
		check := func(saved bool) {
			t.Helper()
			raw, err := os.ReadFile(config)
			if !configExists {
				if !os.IsNotExist(err) {
					t.Fatalf("read-only or refused startup operation created config: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			} else if malformed {
				if string(raw) != initial {
					t.Fatal("startup repaired malformed configuration implicitly")
				}
			} else {
				var got map[string]any
				if err := json.Unmarshal(raw, &got); err != nil || !reflect.DeepEqual(got, wantConfig) {
					t.Fatalf("sparse config lost choices or unrelated fields: got %s want %#v: %v", raw, wantConfig, err)
				}
			}
			var choices types.OnboardingSettings
			if values, ok := wantConfig["onboarding"].(map[string]any); ok {
				choices.IntroductionCompleted, _ = values["introduction_completed"].(bool)
				choices.AISetupReminderHidden, _ = values["ai_setup_reminder_hidden"].(bool)
			}
			independent, err := observer.StartupSnapshot(t.Context())
			accounts, _ := wantConfig["accounts"].(map[string]any)
			intro := !malformed && !saved && !choices.IntroductionCompleted && len(accounts) == 0
			if err != nil || independent.ShowIntroduction != intro || independent.Onboarding != choices || (len(independent.Issues) > 0) != malformed {
				t.Fatalf("real application owner disagrees with disk eligibility: %+v, %v", independent, err)
			}
			var inspected coreprotocol.StartupSnapshot
			call("", coreprotocol.StartupInspect, &coreprotocol.StartupInspectRequest{Root: roots[0]}, &inspected, "")
			if inspected.Root != roots[0] || inspected.PathError != "" || inspected.FolderKind != "" {
				t.Fatalf("valid project inspect: %+v", inspected)
			}
			if hasOwner && (inspected.ShowIntroduction != intro || inspected.Onboarding != choices || (len(inspected.Issues) > 0) != malformed) {
				t.Fatal("core startup projection diverged from committed application choices")
			}
			code := ""
			if !hasOwner {
				code = "settings_unavailable"
			} else if malformed {
				code = "startup_failed"
			}
			var onboarding coreprotocol.OnboardingResult
			call("", coreprotocol.StartupOnboarding, &coreprotocol.Empty{}, &onboarding, code)
			if code == "" && (onboarding.Application.Onboarding != choices || len(onboarding.Accounts) != len(accounts)) {
				t.Fatal("onboarding read does not reflect durable application owner")
			}
		}
		check(false)
		workspaceRoot := filepath.Join(app.DataDir(home), "workspaces")
		if entries, err := os.ReadDir(workspaceRoot); err != nil && !os.IsNotExist(err) || len(entries) != 0 {
			t.Fatal("detached startup constructed workspace storage")
		}
		var current coreprotocol.AttachResult
		call("", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: roots[0]}, &current, "")
		var target coreprotocol.AttachResult
		targetSaved, targetAttached := false, false
		var deleted workspace.DeletedWorkspace
		savedDir := filepath.Join(workspaceRoot, workspace.WorkspaceID(roots[1]))
		for i, action := range sequence[:min(len(sequence), 8)] {
			t.Logf("action %d: %d", i, action)
			switch action % 12 {
			case 0, 1, 2:
				field := "introduction_completed"
				if action%12 == 1 || action%12 == 2 && action&16 != 0 {
					field = "ai_setup_reminder_hidden"
				}
				value := action&16 == 0
				edit := types.SettingEdit{Path: []string{"onboarding", field}}
				if action%12 != 2 {
					edit.After = json.RawMessage(fmt.Sprint(value))
				}
				code := ""
				if !hasOwner {
					code = "settings_unavailable"
				} else if malformed {
					code = "startup_failed"
				}
				call("", coreprotocol.StartupSave, &coreprotocol.StartupSaveRequest{Edits: []types.SettingEdit{edit}}, nil, code)
				if code == "" {
					configExists = true
					values, ok := wantConfig["onboarding"].(map[string]any)
					if !ok {
						values = make(map[string]any)
						wantConfig["onboarding"] = values
					}
					if edit.After == nil {
						delete(values, field)
						if len(values) == 0 {
							delete(wantConfig, "onboarding")
						}
					} else {
						values[field] = value
					}
				}
			case 3:
				edits := []types.SettingEdit{{Path: []string{"editor", "tab_size"}, After: json.RawMessage(`9`)}, {Path: []string{"onboarding", "introduction_completed"}, After: json.RawMessage(`"wrong type"`)}, {Path: []string{"onboarding", "unknown"}, After: json.RawMessage(`true`)}}
				call("", coreprotocol.StartupSave, &coreprotocol.StartupSaveRequest{Edits: []types.SettingEdit{edits[int(action/12)%len(edits)]}}, nil, "invalid_request")
			case 4:
				var opened coreprotocol.AttachResult
				call("", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: roots[1]}, &opened, "")
				if target.WorkspaceID != "" && opened.WorkspaceID != target.WorkspaceID || opened.View.Root != roots[1] {
					t.Fatal("repeated/cold project open changed canonical identity")
				}
				target, targetSaved, targetAttached = opened, true, true
			case 5:
				invalid := []string{home, filepath.Join(projects, "missing"), filepath.Join(roots[1], "project.txt")}[int(action/12)%3]
				call("", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: invalid}, nil, "project_folder_required")
				if _, err := os.Stat(filepath.Join(workspaceRoot, workspace.WorkspaceID(invalid))); !os.IsNotExist(err) {
					t.Fatal("refused path constructed saved workspace storage")
				}
			case 6:
				if targetAttached {
					call(target.WorkspaceID, coreprotocol.Detach, &coreprotocol.Empty{}, nil, "")
					targetAttached = false
				}
			case 7:
				if !targetSaved || deleted.RecoveryPath != "" {
					break
				}
				var plan workspace.WorkspaceDeletion
				call(current.WorkspaceID, coreprotocol.WorkspaceDeletePreview, &coreprotocol.WorkspaceDeletePreviewRequest{ID: workspace.WorkspaceID(roots[1]), ProjectDir: roots[1]}, &plan, "")
				if targetAttached {
					if plan.Issue == nil || plan.Token != "" {
						t.Fatal("attached target admitted deletion")
					}
					break
				}
				if plan.Issue != nil || plan.Token == "" {
					t.Fatalf("detached target was not deletable: %+v", plan)
				}
				before, err := os.ReadFile(filepath.Join(savedDir, "workspace.db"))
				if err != nil {
					t.Fatal(err)
				}
				var result coreprotocol.WorkspaceDeletionResult
				call(current.WorkspaceID, coreprotocol.WorkspaceDelete, &coreprotocol.WorkspaceDeleteRequest{Token: plan.Token}, &result, "")
				deleted, targetSaved = result.Deleted, false
				if deleted.ID != workspace.WorkspaceID(roots[1]) || deleted.ProjectDir != roots[1] || deleted.RecoveryPath == "" {
					t.Fatal("delete lost saved/recovery identity")
				}
				after, err := os.ReadFile(filepath.Join(deleted.RecoveryPath, "data", "workspace.db"))
				if err != nil || !bytes.Equal(before, after) {
					t.Fatal("delete changed rather than moved durable database")
				}
				call(current.WorkspaceID, coreprotocol.WorkspaceDelete, &coreprotocol.WorkspaceDeleteRequest{Token: plan.Token}, nil, "preview_expired")
			case 8:
				if deleted.RecoveryPath == "" {
					break
				}
				code := ""
				if targetSaved {
					code = "workspace_replaced"
				}
				call(current.WorkspaceID, coreprotocol.WorkspaceRestore, &deleted, nil, code)
				if code == "" {
					if _, err := os.Stat(filepath.Join(deleted.RecoveryPath, "data")); !os.IsNotExist(err) {
						t.Fatal("restore left duplicate recovery source")
					}
					targetSaved, deleted = true, workspace.DeletedWorkspace{}
				}
			case 9:
				var dirs coreprotocol.StartupBrowseResult
				call("", coreprotocol.StartupBrowse, &coreprotocol.StartupBrowseRequest{Path: projects}, &dirs, "")
				if len(dirs.Entries) != 2 || dirs.Truncated {
					t.Fatalf("bounded folder browsing lost projects: %+v", dirs)
				}
			case 10:
				observer = newOwner() // Cold application owner must read the same file.
			case 11:
				var infos coreprotocol.WorkspaceInfos
				call(current.WorkspaceID, coreprotocol.SavedWorkspaces, &coreprotocol.Empty{}, &infos, "")
				wantCount := 1
				if targetSaved {
					wantCount++
				}
				if len(infos) != wantCount {
					t.Fatalf("saved catalog contains deleted or missing workspace: %+v", infos)
				}
				for _, info := range infos {
					if info.ID != workspace.WorkspaceID(info.ProjectDir) || info.ProjectDir != roots[0] && (info.ProjectDir != roots[1] || !targetSaved) {
						t.Fatalf("saved catalog reported a foreign project identity: %+v", info)
					}
				}
			}
			check(true)
			var live coreprotocol.WorkspaceSnapshot
			call(current.WorkspaceID, coreprotocol.Snapshot, &coreprotocol.Empty{}, &live, "")
			if live.ID != current.WorkspaceID || live.Root != roots[0] || len(live.Sessions) != 0 {
				t.Fatal("startup operation replaced current attachment or created a session")
			}
			for slot, root := range roots {
				if raw, err := os.ReadFile(filepath.Join(root, "project.txt")); err != nil || string(raw) != "project content\n" {
					t.Fatal("saved-workspace lifecycle changed project files")
				}
				path := filepath.Join(workspaceRoot, workspace.WorkspaceID(root), "workspace.db")
				if slot == 0 || targetSaved {
					startupFuzzDatabase(t, path, root)
				} else if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
					t.Fatal("absent/deleted workspace storage reappeared")
				}
			}
			if deleted.RecoveryPath != "" {
				startupFuzzDatabase(t, filepath.Join(deleted.RecoveryPath, "data", "workspace.db"), roots[1])
			}
		}
	})
}

// Read-only SQL is independent of both the core projection and catalog decoder.
// Handles close before the next delete/restore action; no lease escapes a check.
func startupFuzzDatabase(t *testing.T, path, project string) {
	t.Helper()
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	}()
	var got string
	if err := db.QueryRowContext(t.Context(), `SELECT value FROM workspace_metadata WHERE key = 'project_dir'`).Scan(&got); err != nil || got != project {
		t.Fatalf("durable project identity: got %q want %q: %v", got, project, err)
	}
	var count int
	if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM workspace_sessions`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("startup/lifecycle unexpectedly persisted sessions: %d: %v", count, err)
	}
}
