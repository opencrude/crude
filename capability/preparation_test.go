package capability

import (
	"errors"
	"os"
	"reflect"
	"testing"

	captypes "crude/types/capability"
)

func TestPackagePreparationInstallsWithoutStartingServer(t *testing.T) {
	npm := captypes.ConnectionConfig{ID: "registry_a", Name: "A", Transport: "stdio", Command: "npx", PackageType: "npm", PackageID: "computer-use-mcp", Version: "1.8.0",
		Args: []string{"-y", "--registry=https://registry.example", "computer-use-mcp@1.8.0", "--verbose"}}
	request, needed := packagePreparation(npm)
	if !needed || request.Command != "npx" || !reflect.DeepEqual(request.Args, []string{"--yes", "--registry=https://registry.example", "--package=computer-use-mcp@1.8.0", "--call", "exit 0"}) || request.Key == "" {
		t.Fatalf("npm preparation = %+v %v", request, needed)
	}
	pypi := captypes.ConnectionConfig{ID: "registry_b", Name: "B", Transport: "stdio", Command: "uvx", PackageType: "pypi", PackageID: "ghidra-mcp", Version: "1.0.0",
		Args: []string{"--python", "3.12", "ghidra-mcp==1.0.0", "serve"}}
	request, needed = packagePreparation(pypi)
	if !needed || !reflect.DeepEqual(request.Args, []string{"--python", "3.12", "--from", "ghidra-mcp==1.0.0", "python", "-c", ""}) {
		t.Fatalf("pypi preparation = %+v %v", request, needed)
	}
	edited := npm
	edited.Args = []string{"-y", "computer-use-mcp@2.0.0"}
	shell := npm
	shell.Host = "shell"
	plain := captypes.ConnectionConfig{ID: "plain", Transport: "stdio", Command: "server"}
	for _, config := range []captypes.ConnectionConfig{edited, shell, plain} {
		if _, needed := packagePreparation(config); needed {
			t.Fatalf("connection %q must launch without package preparation", config.ID)
		}
	}
}

// A slow package download must never run inside the launch handshake, where
// its deadline killed npx mid-install and left a half-written cache. The
// launch is authorized first (installation runs package code), then the
// session prepares the package, then the server starts.
func TestPackageBackedLaunchPreparesAfterAuthorization(t *testing.T) {
	service, settings, _ := capabilityFixture(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	settings.Connections["helper"] = captypes.ConnectionConfig{ID: "helper", Name: "Helper", Scope: captypes.ScopeApplication, Transport: "stdio",
		Command: executable, Args: []string{"-test.run=^TestAutonomyStdioHelper$", "helper-package@1.0.0"},
		Environment: map[string]string{"CRUDE_AUTONOMY_STDIO_HELPER": "1"}, PackageType: "npm", PackageID: "helper-package", Version: "1.0.0"}
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	actor := captypes.RuntimeActor{ID: "root", RunID: "run", Effects: []captypes.Effect{captypes.EffectRead, captypes.EffectExecute}}
	_, err = service.Activate(t.Context(), actor, "helper", false)
	var launch *captypes.LaunchApprovalRequired
	if !errors.As(err, &launch) {
		t.Fatalf("package preparation preceded launch authorization: %v", err)
	}
	_, err = service.Activate(launch.CapabilityApprovalContextFor(t.Context(), "yolo"), actor, "helper", false)
	var preparation *captypes.PreparationRequired
	if !errors.As(err, &preparation) || preparation.ConnectionID != "helper" {
		t.Fatalf("authorized package launch skipped preparation: %v", err)
	}
	if connectionState(service, "helper") == "connected" {
		t.Fatal("server started before its package was prepared")
	}
	// The run's authorization is recorded: completing preparation launches
	// without a second approval.
	if _, err := service.Activate(captypes.WithPrepared(t.Context(), preparation.Key), actor, "helper", false); err != nil {
		t.Fatal(err)
	}
	if state := connectionState(service, "helper"); state != "connected" {
		t.Fatalf("prepared package did not launch: %s", state)
	}
	// A live connection is reused without another launch or preparation.
	if _, err := service.Activate(t.Context(), actor, "helper", false); err != nil {
		t.Fatalf("reusing the live connection asked again: %v", err)
	}
	// A successful launch proves the package is installed for later launches.
	if err := service.Disconnect("helper"); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Activate(t.Context(), actor, "helper", false); err != nil {
		t.Fatalf("relaunch in the same run required preparation or approval again: %v", err)
	}
}

func connectionState(service *Service, id string) string {
	for _, state := range service.Snapshot().Connections {
		if state.ID == id {
			return state.State
		}
	}
	return ""
}
