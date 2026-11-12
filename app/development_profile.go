package app

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"crude/fileio"
)

// seedDevelopmentProfile gives a new development profile the release
// profile's preferences and API-key accounts, so `go run .` is usable at once.
// Signed-in (OAuth) credentials are not copied: sharing a refresh token would let
// either profile end the other's sign-in. The release profile is only read, and
// an existing development profile is never changed.
func seedDevelopmentProfile(home, releaseHome string) error {
	dataDir := DataDir(home)
	if _, err := os.Lstat(dataDir); !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	data, err := os.ReadFile(filepath.Join(DataDir(releaseHome), "config.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(data, &config); err != nil {
		return err
	}
	if raw, ok := config["accounts"]; ok {
		var accounts map[string]map[string]json.RawMessage
		if err := json.Unmarshal(raw, &accounts); err != nil {
			return err
		}
		for _, account := range accounts {
			delete(account, "oauth")
		}
		if config["accounts"], err = json.Marshal(accounts); err != nil {
			return err
		}
	}
	seeded, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return err
	}
	return fileio.WriteAtomicNewChecked(filepath.Join(dataDir, "config.json"), seeded, 0o600, nil)
}
