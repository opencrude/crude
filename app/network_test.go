package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"crude/network"
	"crude/types"
	"crude/workspace"
)

func TestNetworkPreferencesRoundTripAndPublishOnlyAfterPersistence(t *testing.T) {
	restore := setTestHome(t, t.TempDir())
	defer restore()
	before := network.Current()
	defer func() { _ = network.ConfigureDefault(before) }()
	file := DefaultFileConfig()
	if err := SaveFileConfig(file); err != nil {
		t.Fatal(err)
	}
	application, err := New(Opts{FC: file})
	if err != nil {
		t.Fatal(err)
	}
	owner := &workspace.Workspace{}
	application.configureWorkspace(owner)
	settings := owner.AppDefaults()
	settings.Network.ProxyMode = network.ProxyModeCustom
	settings.Network.ProxyURL = "socks5h://user:private@localhost:1080"
	settings.Network.NoProxy = "localhost,10.0.0.0/8"
	settings.Network.TLSMinimumVersion = network.TLSMinimumVersion13
	run := owner.PreparePreferencesSave(types.SettingsScopeApplication, settings, types.WorkspaceOverrides{})
	result := run()
	if result.Err != nil {
		t.Fatal(result.Err)
	}
	if owner.AppDefaults().Network.ProxyMode != network.ProxyModeEnvironment {
		t.Fatal("background persistence mutated live workspace state")
	}
	owner.ApplyPreferencesSave(result)
	loaded := FileConfigToAppSettings(LoadFileConfig())
	if !reflect.DeepEqual(loaded.Network, settings.Network) || loaded.Network != network.Current() || loaded.Network != owner.AppDefaults().Network {
		t.Fatal("network persistence, transport policy and workspace projection diverged")
	}
	invalid := owner.AppDefaults()
	invalid.Network.CAFile = filepath.Join(t.TempDir(), "unavailable.pem")
	unchanged, err := os.ReadFile(ConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	failed := owner.PreparePreferencesSave(types.SettingsScopeApplication, invalid, types.WorkspaceOverrides{})()
	if failed.Err == nil {
		t.Fatal("unreadable certificate material was persisted")
	}
	after, err := os.ReadFile(ConfigPath())
	if err != nil || string(after) != string(unchanged) || network.Current() != settings.Network {
		t.Fatal("failed network save changed durable or live trust")
	}
}
