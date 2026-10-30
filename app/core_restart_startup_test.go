package app

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/types/control"
)

func TestCoreRestartStartupSelectorsAreExactAndIdentityIsNoninteractive(t *testing.T) {
	for _, flags := range []CLIFlags{
		{CoreHandover: "relative.json"},
		{CoreHandover: "/private/manifest.json", Resume: "last"},
		{CoreHandover: "/private/manifest.json", SSHHost: "other"},
		{CoreHandover: "/private/manifest.json", ClientProfile: "other"},
		{CoreHandover: "/private/manifest.json", Force: true},
		{CoreHandoverIdentity: true, CoreHandover: "/private/manifest.json"},
		{CoreHandoverIdentity: true, MCP: true},
		{CoreHandoverIdentity: true, APIKey: "never-forward-me", Account: "other"},
	} {
		if err := validateCoreRestartInvocation(flags); err == nil {
			t.Fatal("handover accepted an alternate selector or authority")
		}
	}
	if err := validateCoreRestartInvocation(CLIFlags{CoreHandoverIdentity: true}); err != nil {
		t.Fatal(err)
	}
	if NeedsInteractiveConsole([]string{"--core-handover-identity"}) || NeedsInteractiveConsole([]string{"--core-handover-identity=true"}) {
		t.Fatal("read-only image probe mutates the console")
	}
	if !NeedsInteractiveConsole([]string{"--core-handover", filepath.Join(t.TempDir(), "manifest.json")}) {
		t.Fatal("foreground successor did not retain the interactive terminal")
	}
	cmd := exec.Command(os.Args[0], "--core-handover-identity")
	// This private test binary's entry point dispatches the real CLI, not a
	// shell fixture which could accidentally implement different selectors.
	cmd.Env = append(os.Environ(), "CRUDE_APP_TEST_MAIN=1", "CRUDE_HOME="+filepath.Join(t.TempDir(), "must-not-be-created"))
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var identity CoreRestartIdentity
	if err := restartDecode(out, &identity, 8<<10); err != nil {
		t.Fatal(err)
	}
	expected, err := CoreRestartIdentityForCurrent()
	if err != nil || identity != expected {
		t.Fatalf("CLI did not report its real immutable image: %+v %v", identity, err)
	}
}

func makeRestartStartupManifest(t *testing.T, home string, old coreOwner) (string, CoreRestartIdentity) {
	t.Helper()
	identity, err := CoreRestartIdentityForCurrent()
	if err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	image, err := stageCoreImage(home, exe, identity.BuildID)
	if err != nil {
		t.Fatal(err)
	}
	operation := "private-startup-checkpoint"
	notBefore := time.Now().UTC().Add(-time.Second)
	dir := filepath.Join(restartProfileDir(home, "startup-profile"), restartHash(operation))
	if err := restartPrivateDirs(home, dir); err != nil {
		t.Fatal(err)
	}
	manifest := coreRestartManifest{
		Version: 1, Home: home, Root: filepath.Join(home, "project"), Profile: "startup-profile", ControlID: operation,
		Target:   control.Target{WorkspaceID: "workspace", SessionID: "session", Shell: &control.ShellOwner{ClientID: "original", Epoch: 1}},
		OldOwner: old, Image: image, Identity: identity, Token: strings.Repeat("b", 64),
		NotBefore: notBefore, ExpiresAt: notBefore.Add(10 * time.Minute),
	}
	manifest.Input.Executable, manifest.Input.SHA256, manifest.Input.GoalID = exe, identity.SHA256, "goal"
	manifest.Caller = control.Caller{Kind: control.AgentCaller, AgentID: "root", RunID: "source-run"}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "manifest.json")
	if err := publishRestartFile(path, data); err != nil {
		t.Fatal(err)
	}
	receipt := coreRestartReceipt{ManifestSHA256: restartHash(string(data)), Event: "launch_requested", Ready: true, StopRequested: true, StopSettled: true, LaunchRequested: true}
	if err := writeRestartReceipt(path, &receipt); err != nil {
		t.Fatal(err)
	}
	return path, identity
}

func TestCoreRestartStartupNeverStopsAnUnexpectedOwner(t *testing.T) {
	for _, kind := range []string{"original", "replacement"} {
		t.Run(kind, func(t *testing.T) {
			fixture := newRestartFixture(t)
			path, _ := makeRestartStartupManifest(t, fixture.host.home, fixture.old)
			expected := fixture.old
			if kind == "replacement" {
				if err := removeCoreOwner(fixture.host.home, fixture.old.Generation); err != nil {
					t.Fatal(err)
				}
				expected.Generation = strings.Repeat("5", 64)
				if err := publishCoreOwner(coreOwnerPath(fixture.host.home), expected); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			if c, err := newCoreRestartDialer(fixture.host.home, path)(ctx, "startup-inspector"); err == nil {
				_ = c.Close()
				t.Fatal("handover startup adopted or stopped an unintended owner")
			}
			actual, err := readCoreOwner(fixture.host.home)
			if err != nil || actual != expected {
				t.Fatal("refused startup changed the protected owner", actual, err)
			}
		})
	}
}

func TestCoreRestartStartupRunsExactImageOnlyAfterProvenAbsence(t *testing.T) {
	home := coreTestHome(t)
	old := coreOwner{Generation: strings.Repeat("6", 64)}
	path, identity := makeRestartStartupManifest(t, home, old)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	c, err := newCoreRestartDialer(home, path)(ctx, "original")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	actual, err := readCoreOwner(home)
	if err != nil || actual.Generation == old.Generation || actual.Digest != identity.SHA256 ||
		actual.Cohort != identity.Cohort || actual.Schema != identity.Schema {
		t.Fatal("socket presence was mistaken for the intended successor image", actual, err)
	}
}
