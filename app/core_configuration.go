package app

import (
	"crude/network"
	"crude/provider"
	ttypes "crude/types"
	"crude/workspace"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// commitCoreAccount commits an exact entity preimage without publishing workspace state.
func (a *App) commitCoreAccount(change ttypes.AccountChange) (ttypes.AppSettings, error) {
	var updated FileConfig
	err := a.accounts.Commit(func(_ map[string]provider.Account) (map[string]provider.Account, error) {
		a.configMu.Lock()
		defer a.configMu.Unlock()
		var saveErr error
		updated, saveErr = updateFileConfig(func(current *FileConfig) error {
			if change.ID == "" || change.ProviderID == "" {
				return fmt.Errorf("invalid account identity")
			}
			if change.Before != nil && change.Before.ID != change.ID {
				return fmt.Errorf("invalid account precondition")
			}
			before, exists := current.Accounts[change.ID]
			if change.Before == nil {
				if exists {
					return workspace.ErrApplicationSettingsConflict
				}
			} else if !exists || !reflect.DeepEqual(before, *change.Before) {
				return workspace.ErrApplicationSettingsConflict
			}
			if change.After == nil {
				if change.Before == nil {
					return fmt.Errorf("cannot remove a missing account")
				}
				delete(current.Accounts, change.ID)
			} else {
				if change.After.ID != change.ID || change.After.ProviderID != change.ProviderID || strings.TrimSpace(change.After.Label) == "" {
					return fmt.Errorf("invalid account identity or name")
				}
				resolved := change.After.Connection
				if err := resolved.Validate(); err != nil {
					return fmt.Errorf("invalid account connection: %w", err)
				}
				if resolved.AuthType != provider.AuthNone && change.After.APIKey == "" && (change.After.OAuth == nil || change.After.OAuth.AccessToken == "" && change.After.OAuth.RefreshToken == "") {
					return fmt.Errorf("configure authentication for account %q", change.After.Label)
				}
				for id, other := range current.Accounts {
					if id != change.ID && strings.EqualFold(strings.TrimSpace(other.Label), strings.TrimSpace(change.After.Label)) {
						return fmt.Errorf("an account with that name already exists")
					}
				}
				if current.Accounts == nil {
					current.Accounts = make(map[string]provider.Account)
				}
				current.Accounts[change.ID] = cloneAccount(*change.After)
			}
			return nil
		})
		if saveErr != nil {
			return nil, saveErr
		}
		a.fc = updated
		a.persistedAccounts = cloneAccounts(updated.Accounts)
		delete(a.rotationPredecessors, change.ID)
		return updated.Accounts, nil
	})
	if err != nil {
		return ttypes.AppSettings{}, err
	}
	return detachedCoreSettings(updated), nil
}

// commitCorePreferences uses a complete private preimage, excluding independently
// owned accounts. The file lock compares against disk, not a stale shell draft.
func (a *App) commitCorePreferences(before, after ttypes.AppSettings) (ttypes.AppSettings, error) {
	before.Accounts, after.Accounts = nil, nil
	if err := after.Network.Validate(); err != nil {
		return ttypes.AppSettings{}, err
	}
	var updated FileConfig
	err := a.accounts.Commit(func(_ map[string]provider.Account) (map[string]provider.Account, error) {
		a.configMu.Lock()
		defer a.configMu.Unlock()
		var err error
		updated, err = updateFileConfig(func(current *FileConfig) error {
			actual := FileConfigToAppSettings(*current)
			actual.Accounts = nil
			if !reflect.DeepEqual(actual, before) {
				return workspace.ErrApplicationSettingsConflict
			}
			if network.Normalize(before.Network) != network.Normalize(after.Network) || network.StartupPolicyError() != nil {
				if err := after.Network.ValidateFiles(); err != nil {
					return err
				}
			}
			ApplyAppSettingsToFileConfig(current, after)
			return nil
		})
		if err != nil {
			return nil, err
		}
		a.fc = updated
		a.persistedAccounts = cloneAccounts(updated.Accounts)
		return updated.Accounts, nil
	})
	if err != nil {
		return ttypes.AppSettings{}, err
	}
	if err := network.ConfigureDefault(FileConfigToAppSettings(updated).Network); err != nil {
		return ttypes.AppSettings{}, err
	}
	return detachedCoreSettings(updated), nil
}

func detachedCoreSettings(value FileConfig) ttypes.AppSettings {
	raw, _ := json.Marshal(FileConfigToAppSettings(value))
	var out ttypes.AppSettings
	_ = json.Unmarshal(raw, &out)
	return out
}
