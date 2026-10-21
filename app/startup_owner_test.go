package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"testing"

	"crude/network"
	"crude/types"
)

func startupOwnerHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CRUDE_HOME", home)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", os.Getenv("HOME"))
	previous := network.Current()
	t.Cleanup(func() { _ = network.ConfigureDefault(previous) })
	return home
}

func writeStartupConfig(t *testing.T, raw string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(ConfigPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ConfigPath(), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
}

func newStartupOwner(t *testing.T, home string) *App {
	t.Helper()
	// Do not configure a workspace: first-use writes happen before one exists.
	a, err := New(Opts{Home: home, FC: LoadFileConfig()})
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func startupChoice(field, value string) types.SettingEdit {
	return types.SettingEdit{Path: []string{"onboarding", field}, After: json.RawMessage(value)}
}

func readStartupJSON(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestStartupOwnerProfileEligibility(t *testing.T) {
	for _, tc := range []struct {
		name, config string
		workspace    bool
		introduction bool
		hidden       bool
	}{
		{name: "missing", introduction: true},
		{name: "empty", config: `{}`, introduction: true},
		{name: "empty accounts", config: `{"accounts":{}}`, introduction: true},
		{name: "account without introduction flag", config: `{"accounts":{"local":{"id":"local","provider_id":"openai","label":"Local"}}}`},
		{name: "workspace without introduction flag", config: `{}`, workspace: true},
		{name: "completed", config: `{"onboarding":{"introduction_completed":true}}`},
		{name: "suppression is not completion", config: `{"onboarding":{"ai_setup_reminder_hidden":true}}`, introduction: true, hidden: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := startupOwnerHome(t)
			if tc.config != "" {
				writeStartupConfig(t, tc.config)
			}
			if tc.workspace {
				// Metadata alone establishes prior use; no usable database is required.
				if err := os.MkdirAll(filepath.Join(DataDir(home), "workspaces", "saved-but-unavailable"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			out, err := newStartupOwner(t, home).StartupSnapshot(t.Context())
			if err != nil || len(out.Issues) != 0 || out.ShowIntroduction != tc.introduction || out.Onboarding.AISetupReminderHidden != tc.hidden {
				t.Fatalf("eligibility: %+v, err=%v", out, err)
			}
			if tc.config == "" {
				if _, err := os.Stat(ConfigPath()); !os.IsNotExist(err) {
					t.Fatalf("inspection wrote a config: %v", err)
				}
			} else if raw, err := os.ReadFile(ConfigPath()); err != nil || string(raw) != tc.config {
				t.Fatal("inspection changed saved configuration")
			}
		})
	}
}

func TestStartupOwnerSaveSparseChoicesSurviveRestart(t *testing.T) {
	home := startupOwnerHome(t)
	a := newStartupOwner(t, home)
	if _, err := a.SaveStartup(t.Context(), []types.SettingEdit{startupChoice("introduction_completed", "true")}); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"onboarding": map[string]any{"introduction_completed": true}}
	if got := readStartupJSON(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("first write materialized unrelated defaults: %#v", got)
	}
	a = newStartupOwner(t, home)
	if _, err := a.SaveStartup(t.Context(), []types.SettingEdit{startupChoice("ai_setup_reminder_hidden", "true")}); err != nil {
		t.Fatal(err)
	}
	out, err := newStartupOwner(t, home).StartupSnapshot(t.Context())
	if err != nil || out.ShowIntroduction || !out.Onboarding.IntroductionCompleted || !out.Onboarding.AISetupReminderHidden {
		t.Fatalf("restart lost committed choices: %+v, err=%v", out, err)
	}
	// Explicit false is a stored choice, not an instruction to materialize defaults.
	if _, err := a.SaveStartup(t.Context(), []types.SettingEdit{startupChoice("ai_setup_reminder_hidden", "false")}); err != nil {
		t.Fatal(err)
	}
	want["onboarding"].(map[string]any)["ai_setup_reminder_hidden"] = false
	if got := readStartupJSON(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("sparse false choice: %#v", got)
	}
}

func TestStartupOwnerConcurrentEditsPreserveCurrentFile(t *testing.T) {
	home := startupOwnerHome(t)
	writeStartupConfig(t, `{}`)
	first, second := newStartupOwner(t, home), newStartupOwner(t, home)
	// Both owners are stale when an independent writer changes other groups.
	writeStartupConfig(t, `{"accounts":{"local":{"id":"local","provider_id":"openai","label":"Local","api_key":"fixture-secret"}},"network":{"proxy_mode":"custom","proxy_url":"http://127.0.0.1:9","disable_http2":false},"editor":{"tab_size":7},"lsp":{"enabled":false},"future_key":{"keep":[1,2]}}`)
	want := readStartupJSON(t)
	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for i, a := range []*App{first, second} {
		field := []string{"introduction_completed", "ai_setup_reminder_hidden"}[i]
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := a.SaveStartup(context.Background(), []types.SettingEdit{startupChoice(field, "true")})
			errs <- err
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	want["onboarding"] = map[string]any{"introduction_completed": true, "ai_setup_reminder_hidden": true}
	if got := readStartupJSON(t); !reflect.DeepEqual(got, want) {
		t.Fatalf("startup commits lost sibling choices or unrelated current fields: %#v", got)
	}
	out, err := newStartupOwner(t, home).StartupSnapshot(t.Context())
	if err != nil || out.ShowIntroduction || !out.Onboarding.AISetupReminderHidden {
		t.Fatalf("restart after concurrent saves: %+v, %v", out, err)
	}
}

func TestStartupOwnerRepairAndWriteErrorsAreVisible(t *testing.T) {
	for _, scenario := range []string{"malformed", "config directory", "blocked config parent", "read only parent"} {
		t.Run(scenario, func(t *testing.T) {
			home := startupOwnerHome(t)
			a := newStartupOwner(t, home)
			const broken = `{"accounts":{"key":"do-not-echo"},`
			switch scenario {
			case "malformed":
				writeStartupConfig(t, broken)
			case "config directory":
				if err := os.MkdirAll(ConfigPath(), 0700); err != nil {
					t.Fatal(err)
				}
			case "blocked config parent":
				if err := os.WriteFile(filepath.Dir(ConfigPath()), []byte("not a directory"), 0600); err != nil {
					t.Fatal(err)
				}
			case "read only parent":
				if runtime.GOOS == "windows" {
					t.Skip("POSIX directory permissions are not Windows ACLs")
				}
				writeStartupConfig(t, `{}`)
				parent := filepath.Dir(ConfigPath())
				if err := os.Chmod(parent, 0500); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.Chmod(parent, 0700) })
				// Skip only when this host's privileges bypass directory permissions.
				probe, err := os.CreateTemp(parent, "permission-probe-")
				if err == nil {
					_ = probe.Close()
					t.Skip("host can write through read-only directory permissions")
				}
			}
			if scenario != "read only parent" {
				out, err := a.StartupSnapshot(t.Context())
				if err != nil || out.ShowIntroduction || len(out.Issues) == 0 || out.Issues[0].Target != types.RepairConfigFile || out.Issues[0].Path != ConfigPath() {
					t.Fatalf("repair mistaken for first use: %+v, %v", out, err)
				}
			}
			if _, err := a.SaveStartup(t.Context(), []types.SettingEdit{startupChoice("introduction_completed", "true")}); err == nil {
				t.Fatal("failed configuration save reported success")
			}
			if a.committedDefaults().Onboarding.IntroductionCompleted {
				t.Fatal("failed save published completion in memory")
			}
			if scenario == "malformed" {
				if raw, err := os.ReadFile(ConfigPath()); err != nil || string(raw) != broken {
					t.Fatal("save overwrote malformed configuration")
				}
			}
		})
	}
}

func TestStartupOwnerRejectsInvalidAndCancelledEdits(t *testing.T) {
	home := startupOwnerHome(t)
	a := newStartupOwner(t, home)
	for _, edit := range []types.SettingEdit{
		{Path: []string{"editor", "tab_size"}, After: json.RawMessage(`8`)},
		{Path: []string{"onboarding", "unknown"}, After: json.RawMessage(`true`)},
		startupChoice("introduction_completed", `"true"`),
	} {
		if _, err := a.SaveStartup(t.Context(), []types.SettingEdit{edit}); err == nil {
			t.Fatalf("accepted invalid startup edit: %+v", edit)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := a.SaveStartup(ctx, []types.SettingEdit{startupChoice("introduction_completed", "true")}); err == nil {
		t.Fatal("cancelled save succeeded")
	}
	if _, err := os.Stat(ConfigPath()); !os.IsNotExist(err) {
		t.Fatalf("invalid or cancelled save wrote config: %v", err)
	}
}
