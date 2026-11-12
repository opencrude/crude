package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestNewDevelopmentProfileCopiesSettingsButNotSignIns(t *testing.T) {
	release, dev := t.TempDir(), filepath.Join(t.TempDir(), ".crude-dev")
	original := []byte(`{"theme":{"name":"dark"},"accounts":{"key":{"id":"key","api_key":"sk-test"},"signed":{"id":"signed","oauth":{"access_token":"a","refresh_token":"r"}}}}`)
	if err := os.MkdirAll(DataDir(release), 0o700); err != nil {
		t.Fatal(err)
	}
	releaseConfig := filepath.Join(DataDir(release), "config.json")
	if err := os.WriteFile(releaseConfig, original, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := seedDevelopmentProfile(dev, release); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(DataDir(dev), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	var seeded struct {
		Theme    map[string]string                     `json:"theme"`
		Accounts map[string]map[string]json.RawMessage `json:"accounts"`
	}
	if err := json.Unmarshal(data, &seeded); err != nil {
		t.Fatal(err)
	}
	if seeded.Theme["name"] != "dark" || string(seeded.Accounts["key"]["api_key"]) != `"sk-test"` {
		t.Fatalf("development profile lost settings or API keys: %s", data)
	}
	if _, ok := seeded.Accounts["signed"]["oauth"]; ok || seeded.Accounts["signed"] == nil {
		t.Fatalf("signed-in account must stay listed without its shared refresh token: %s", data)
	}
	if after, _ := os.ReadFile(releaseConfig); string(after) != string(original) {
		t.Fatal("seeding changed the release profile")
	}

	// Later starts never overwrite choices made in the development profile.
	if err := os.WriteFile(releaseConfig, []byte(`{"theme":{"name":"light"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := seedDevelopmentProfile(dev, release); err != nil {
		t.Fatal(err)
	}
	if again, _ := os.ReadFile(filepath.Join(DataDir(dev), "config.json")); string(again) != string(data) {
		t.Fatal("an existing development profile was replaced")
	}
}

func TestNewDevelopmentProfileStartsEmptyWithoutAReleaseProfile(t *testing.T) {
	dev := filepath.Join(t.TempDir(), ".crude-dev")
	if err := seedDevelopmentProfile(dev, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(DataDir(dev)); !os.IsNotExist(err) {
		t.Fatalf("no release profile must leave the development profile to normal startup: %v", err)
	}
}
