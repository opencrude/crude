package app

import (
	"crude/network"
	"crude/provider"
	"crude/session"
	ttypes "crude/types"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// commitCoreAccount writes this account's explicit changes without replaying
// untouched credentials or other accounts.
func (a *App) commitCoreAccount(change ttypes.AccountChange) (ttypes.AppSettings, error) {
	var updated FileConfig
	err := a.accounts.Commit(func(_ map[string]provider.Account) (map[string]provider.Account, error) {
		a.configMu.Lock()
		defer a.configMu.Unlock()
		var saveErr error
		updated, saveErr = updateFileConfig(func(current *FileConfig) error {
			if change.ID == "" || change.After != nil && change.ProviderID == "" {
				return fmt.Errorf("invalid account identity")
			}
			if change.Before != nil && change.Before.ID != change.ID {
				return fmt.Errorf("invalid account precondition")
			}
			if change.After == nil {
				delete(current.Accounts, change.ID)
			} else {
				if change.Before != nil {
					latest, exists := current.Accounts[change.ID]
					if !exists {
						return fmt.Errorf("account was removed; add an account to configure it again")
					}
					if err := ttypes.ApplySettingsEdits(&latest, ttypes.SettingsEdits(*change.Before, *change.After)); err != nil {
						return err
					}
					change.After = &latest
				}
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
	a.notifyDefaultsChanged()
	return detachedCoreSettings(updated), nil
}

func (a *App) reloadFileConfiguration() error {
	return a.accounts.Commit(func(_ map[string]provider.Account) (map[string]provider.Account, error) {
		a.configMu.Lock()
		defer a.configMu.Unlock()
		// Read at the owner boundary, not from a potentially delayed watcher
		// result. File saves and reloads use the same lock order.
		latest, err := loadFileConfigFrom(ConfigPath())
		if err != nil {
			return nil, err
		}
		settings := FileConfigToAppSettings(latest)
		if err := settings.Network.ValidateFiles(); err != nil {
			return nil, err
		}
		if a.remote != nil {
			change, err := a.remote.Prepare(settings.Network)
			if err != nil {
				return nil, err
			}
			defer change.Abort()
			if err = change.Commit(); err != nil {
				return nil, err
			}
		}
		if err := network.ConfigureDefault(settings.Network); err != nil {
			return nil, err
		}
		a.fc = latest
		a.persistedAccounts = cloneAccounts(latest.Accounts)
		return latest.Accounts, nil
	})
}

// commitCorePreferences commits only the changed fields against latest disk data.
// Accounts and capability entities keep their independent exact transactions.
func (a *App) commitCorePreferences(edits []ttypes.SettingEdit) (ttypes.AppSettings, error) {
	if len(edits) == 0 {
		return a.committedDefaults(), nil
	}
	networkChanged := slices.ContainsFunc(edits, func(e ttypes.SettingEdit) bool { return len(e.Path) > 0 && e.Path[0] == "network" })
	var updated FileConfig
	err := a.accounts.Commit(func(_ map[string]provider.Account) (map[string]provider.Account, error) {
		a.configMu.Lock()
		defer a.configMu.Unlock()
		var err error
		var listeners *remoteListenerChange
		defer func() {
			if listeners != nil {
				listeners.Abort()
			}
		}()
		updated, err = updateFileConfig(func(current *FileConfig) error {
			actual := FileConfigToAppSettings(*current)
			actual.Accounts = nil
			if err := ttypes.ApplySettingsEdits(&actual, edits); err != nil {
				return err
			}
			if networkChanged {
				if err := actual.Network.ValidateFiles(); err != nil {
					return err
				}
				if a.remote != nil {
					listeners, err = a.remote.Prepare(actual.Network)
					if err != nil {
						return err
					}
				}
			}
			return applyPreferenceEdits(current, actual, edits)
		})
		if err != nil {
			return nil, err
		}
		a.fc = updated
		a.persistedAccounts = cloneAccounts(updated.Accounts)
		if listeners != nil {
			if err = listeners.Commit(); err != nil {
				return nil, fmt.Errorf("settings saved but remote listeners could not be published: %w", err)
			}
		}
		return updated.Accounts, nil
	})
	if err != nil {
		return ttypes.AppSettings{}, err
	}
	if networkChanged {
		if err := network.ConfigureDefault(FileConfigToAppSettings(updated).Network); err != nil {
			return ttypes.AppSettings{}, err
		}
	}
	a.notifyDefaultsChanged()
	return detachedCoreSettings(updated), nil
}

// applyPreferenceEdits maps the neutral preference fields onto the file's single
// stored definition. It never serializes resolved/default values for neighbors.
func applyPreferenceEdits(current *FileConfig, desired ttypes.AppSettings, edits []ttypes.SettingEdit) error {
	raw, err := json.Marshal(current)
	if err != nil {
		return err
	}
	if len(current.raw) != 0 {
		raw = current.raw
	}
	var stored []ttypes.SettingEdit
	for _, edit := range edits {
		path := slices.Clone(edit.Path)
		if len(path) == 0 || path[0] == "accounts" {
			continue
		}
		if path[0] == "agents" {
			if len(path) < 2 {
				return fmt.Errorf("edit an individual agent field")
			}
			switch path[1] {
			case "Categories":
				path = append([]string{"agents", "profiles", desired.Agents.ActiveProfile, "categories"}, path[2:]...)
				if len(edit.Path) > 3 {
					path[5] = categoryStoredName(edit.Path[3])
				}
			case "Enabled":
				path = []string{"agents", "profiles", desired.Agents.ActiveProfile, "multi_agent"}
			case "MaxConcurrent":
				path[1] = "max_concurrent"
			case "ActiveProfile":
				path[1] = "active_profile"
			case "agent_profiles":
			case "execution_hygiene":
			default:
				return fmt.Errorf("unknown agent setting")
			}
		}
		value := edit.After
		if len(edit.Path) == 3 && edit.Path[0] == "agents" && edit.Path[1] == "Categories" && value != nil {
			var category ttypes.CategorySettings
			if err := json.Unmarshal(value, &category); err != nil {
				return err
			}
			cfg := &session.Config{Agents: session.DefaultAgentsConfig()}
			session.ApplyWorkspaceAgentDefaults(cfg, ttypes.AgentsSettings{Categories: map[string]ttypes.CategorySettings{edit.Path[2]: category}})
			value, err = json.Marshal(cfg.Agents.Profiles[cfg.Agents.ActiveProfile].Categories[edit.Path[2]])
			if err != nil {
				return err
			}
		}
		stored = append(stored, ttypes.SettingEdit{Path: path, After: value})
	}
	raw, err = ttypes.PatchStoredSettings(raw, stored)
	if err != nil {
		return err
	}
	next := FileConfig{}
	if err := ttypes.DecodeStoredSettings(raw, &next); err != nil {
		return err
	}
	next.raw = raw
	*current = next
	return nil
}

func categoryStoredName(name string) string {
	t := reflect.TypeFor[ttypes.CategorySettings]()
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		jsonName := strings.Split(field.Tag.Get("json"), ",")[0]
		if jsonName == "" {
			jsonName = field.Name
		}
		if jsonName != name {
			continue
		}
		target, ok := reflect.TypeFor[session.CategoryConfig]().FieldByName(field.Name)
		if ok {
			return strings.Split(target.Tag.Get("json"), ",")[0]
		}
	}
	return name
}

// notifyDefaultsChanged tells the core host that committed application
// defaults changed, so every retained workspace adopts them, not only the
// workspace whose request performed the commit.
func (a *App) notifyDefaultsChanged() {
	if a.defaultsChanged != nil {
		a.defaultsChanged()
	}
}

// committedDefaults is the current durable application snapshot.
func (a *App) committedDefaults() ttypes.AppSettings {
	a.configMu.Lock()
	defer a.configMu.Unlock()
	return detachedCoreSettings(a.fc)
}

func detachedCoreSettings(value FileConfig) ttypes.AppSettings {
	raw, _ := json.Marshal(FileConfigToAppSettings(value))
	var out ttypes.AppSettings
	_ = json.Unmarshal(raw, &out)
	return out
}
