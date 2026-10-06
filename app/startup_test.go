package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/network"
	"crude/provider"
	"crude/session"
	"crude/types"
)

const unavailableStartupAccount = "5c7eb662d2f8abcca6eeeb1cf1acbc50"

func TestPrepareStartupMissingConfiguredAccountKeepsSelectionAndConfigBytes(t *testing.T) {
	defer setTestHome(t, t.TempDir())()
	fc := DefaultFileConfig()
	agents := session.DefaultAgentsConfig()
	profile := agents.Profiles[agents.ActiveProfile]
	main := profile.Categories[session.CategoryDefault]
	main.AccountID = unavailableStartupAccount
	profile.Categories[session.CategoryDefault] = main
	agents.Profiles[agents.ActiveProfile] = profile
	fc.Agents = &agents
	if err := SaveFileConfig(fc); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}

	prepared := PrepareStartup(CLIFlags{})
	if len(prepared.Issues) != 1 || prepared.Issues[0].Target != types.RepairAccounts || !strings.Contains(prepared.Issues[0].Detail, unavailableStartupAccount) {
		t.Fatalf("missing typed account repair: %#v", prepared.Issues)
	}
	gotMain := prepared.Config.Agents.Profiles[prepared.Config.Agents.ActiveProfile].Categories[session.CategoryDefault]
	if gotMain.AccountID != unavailableStartupAccount {
		t.Fatalf("startup changed selected account to %q", gotMain.AccountID)
	}
	after, err := os.ReadFile(ConfigPath())
	if err != nil || string(after) != string(before) {
		t.Fatal("startup rewrote the saved configuration")
	}
}

func TestPrepareStartupMalformedConfigIsNonWritingRepair(t *testing.T) {
	defer setTestHome(t, t.TempDir())()
	if err := os.MkdirAll(filepath.Dir(ConfigPath()), 0700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"accounts":{"work":{"api_key":"do-not-echo"}},`)
	if err := os.WriteFile(ConfigPath(), raw, 0600); err != nil {
		t.Fatal(err)
	}
	prepared := PrepareStartup(CLIFlags{})
	if len(prepared.Issues) != 1 || prepared.Issues[0].Target != types.RepairConfigFile || prepared.Issues[0].Path != ConfigPath() {
		t.Fatalf("config issue = %#v", prepared.Issues)
	}
	if strings.Contains(prepared.Issues[0].Detail, "do-not-echo") {
		t.Fatal("decode issue exposed raw configuration content")
	}
	after, err := os.ReadFile(ConfigPath())
	if err != nil || string(after) != string(raw) {
		t.Fatal("malformed configuration bytes changed during startup")
	}
}

func TestPrepareStartupUnavailableResumeOpensLauncherWithIssue(t *testing.T) {
	defer setTestHome(t, t.TempDir())()
	prepared := PrepareStartup(CLIFlags{Resume: "last"})
	if prepared.ResumeID != "" || len(prepared.Issues) != 1 || prepared.Issues[0].Target != types.RepairSessionAccount {
		t.Fatalf("resume preparation = %#v", prepared)
	}
}

func TestPrepareStartupCLISelectionDoesNotFallback(t *testing.T) {
	defer setTestHome(t, t.TempDir())()
	fc := DefaultFileConfig()
	fc.Accounts = map[string]provider.Account{"valid": {ID: "valid", ProviderID: "openrouter", APIKey: "secret", Connection: provider.DefaultTemplates()["openrouter"]}}
	if err := SaveFileConfig(fc); err != nil {
		t.Fatal(err)
	}
	prepared := PrepareStartup(CLIFlags{Provider: "openrouter", Account: unavailableStartupAccount})
	if len(prepared.Issues) != 1 || prepared.Issues[0].Target != types.RepairAccounts {
		t.Fatalf("selection issue = %#v", prepared.Issues)
	}
	if prepared.Config.Accounts["valid"].APIKey != "secret" {
		t.Fatal("startup replaced or damaged the configured account set")
	}
	if got := FileConfigToSession(prepared.Config).GetActiveDefaultCategory().AccountID; got != unavailableStartupAccount {
		t.Fatalf("failed CLI account silently fell back to %q", got)
	}
}

func TestStartupInvalidNetworkKeepsShellAndPolicyUntilValidSave(t *testing.T) {
	defer setTestHome(t, t.TempDir())()
	previous := network.Current()
	t.Cleanup(func() { _ = network.ConfigureDefault(previous) })
	fc := DefaultFileConfig()
	invalid := network.DefaultSettings()
	invalid.CAFile = filepath.Join(t.TempDir(), "missing-ca.pem")
	fc.Network = &invalid
	if err := SaveFileConfig(fc); err != nil {
		t.Fatal(err)
	}
	application, err := New(Opts{FC: fc})
	if err != nil || application == nil {
		t.Fatalf("invalid certificate locked user out: %v", err)
	}
	if len(application.startupIssues) != 1 || application.startupIssues[0].Target != types.RepairNetwork {
		t.Fatal("missing actionable certificate startup issue")
	}
	if _, err := network.NewTransport(network.Current(), 0); err == nil {
		t.Fatal("invalid certificate silently bypassed")
	}
}

func TestStartupConfigRepairBlocksOutboundWithoutOverwritingBytes(t *testing.T) {
	defer setTestHome(t, t.TempDir())()
	previous := network.Current()
	t.Cleanup(func() { _ = network.ConfigureDefault(previous) })
	if err := os.MkdirAll(filepath.Dir(ConfigPath()), 0700); err != nil {
		t.Fatal(err)
	}
	raw := []byte(`{"network":{"proxy_url":"private`)
	if err := os.WriteFile(ConfigPath(), raw, 0600); err != nil {
		t.Fatal(err)
	}
	prepared := PrepareStartup(CLIFlags{})
	application, err := New(Opts{FC: prepared.Config, StartupIssues: prepared.Issues})
	if err != nil || application == nil {
		t.Fatal("malformed config locked user out")
	}
	if _, err := network.NewTransport(network.Current(), 0); err == nil {
		t.Fatal("malformed policy silently substituted normal outbound defaults")
	}
	after, err := os.ReadFile(ConfigPath())
	if err != nil || string(after) != string(raw) {
		t.Fatal("startup rewrote malformed policy")
	}
}
