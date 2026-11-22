package app

import (
	"crude/agent/provider"
	"crude/agent/session"
	"crude/internal/network"
	"crude/types/setting"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// commitCoreAccount writes this account's explicit changes without replaying
// untouched credentials or other accounts.
func (a *App) commitCoreAccount(change setting.AccountChange) (setting.App, error) {
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
				latest, exists := current.Accounts[change.ID]
				if !exists || change.Before != nil && !reflect.DeepEqual(latest, *change.Before) {
					return fmt.Errorf("the account changed; review it and try again")
				}
				return releaseStoredAccount(current, change.ID, change.RemovalPreview)
			}
			if change.Before != nil {
				latest, exists := current.Accounts[change.ID]
				if !exists {
					return fmt.Errorf("account was removed; add an account to configure it again")
				}
				if err := setting.ApplyEdits(&latest, setting.Edits(*change.Before, *change.After)); err != nil {
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
		return setting.App{}, err
	}
	a.notifyDefaultsChanged()
	return detachedCoreSettings(updated), nil
}

// releaseStoredAccount removes the account and, in the same file write, every
// setting that names it (types.ReleaseAccount). Nothing keeps an ID that no
// account has.
func releaseStoredAccount(current *FileConfig, id string, preview string) error {
	before := FileConfigToAppSettings(*current)
	if preview != "" && preview != setting.AccountRemovalPreview(before, id) {
		return fmt.Errorf("account uses changed; review them and try again")
	}
	before.Accounts = nil
	after := setting.CloneApp(before)
	if setting.ReleaseAccount(&after, id) {
		if err := applyPreferenceEdits(current, after, setting.Edits(before, after)); err != nil {
			return err
		}
		// The preference patch now owns the stored bytes, so the account
		// leaves them in the same patch.
		raw, err := setting.PatchStored(current.raw, []setting.Edit{{Path: []string{"accounts", id}}})
		if err != nil {
			return err
		}
		current.raw = raw
	}
	delete(current.Accounts, id)
	return nil
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
func (a *App) commitCorePreferences(edits []setting.Edit) (setting.App, error) {
	if err := setting.ValidateProjectToolDecisionEdits(edits); err != nil {
		return setting.App{}, err
	}
	if err := setting.ValidateAnalysisEdits(setting.ScopeApplication, edits); err != nil {
		return setting.App{}, err
	}
	if err := setting.ValidatePredictionEdits(setting.ScopeApplication, edits); err != nil {
		return setting.App{}, err
	}
	if len(edits) == 0 {
		return a.committedDefaults(), nil
	}
	networkChanged := slices.ContainsFunc(edits, func(e setting.Edit) bool { return len(e.Path) > 0 && e.Path[0] == "network" })
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
			if err := setting.ApplyEdits(&actual, edits); err != nil {
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
		return setting.App{}, err
	}
	if networkChanged {
		if err := network.ConfigureDefault(FileConfigToAppSettings(updated).Network); err != nil {
			return setting.App{}, err
		}
	}
	a.notifyDefaultsChanged()
	return detachedCoreSettings(updated), nil
}

// applyPreferenceEdits maps the neutral preference fields onto the file's single
// stored definition. It never serializes resolved/default values for neighbors.
func applyPreferenceEdits(current *FileConfig, desired setting.App, edits []setting.Edit) error {
	if err := setting.ValidateProjectToolDecisionEdits(edits); err != nil {
		return err
	}
	if err := setting.ValidateAnalysisEdits(setting.ScopeApplication, edits); err != nil {
		return err
	}
	if err := setting.ValidatePredictionEdits(setting.ScopeApplication, edits); err != nil {
		return err
	}
	raw, err := json.Marshal(current)
	if err != nil {
		return err
	}
	if len(current.raw) != 0 {
		raw = current.raw
	}
	var stored []setting.Edit
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
			case "agent_profiles", "shell", "max_wait_minutes", "suggestions":
			default:
				return fmt.Errorf("unknown agent setting")
			}
		}
		value := edit.After
		if len(edit.Path) == 3 && edit.Path[0] == "agents" && edit.Path[1] == "Categories" && value != nil {
			var category setting.Category
			if err := json.Unmarshal(value, &category); err != nil {
				return err
			}
			cfg := &session.Config{Agents: session.DefaultAgentsConfig()}
			session.ApplyWorkspaceAgentDefaults(cfg, setting.Agents{Categories: map[string]setting.Category{edit.Path[2]: category}})
			value, err = json.Marshal(cfg.Agents.Profiles[cfg.Agents.ActiveProfile].Categories[edit.Path[2]])
			if err != nil {
				return err
			}
		}
		stored = append(stored, setting.Edit{Path: path, After: value})
	}
	raw, err = setting.PatchStored(raw, stored)
	if err != nil {
		return err
	}
	next := FileConfig{}
	if err := setting.DecodeStored(raw, &next); err != nil {
		return err
	}
	next.raw = raw
	*current = next
	return nil
}

func categoryStoredName(name string) string {
	t := reflect.TypeFor[setting.Category]()
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
func (a *App) committedDefaults() setting.App {
	a.configMu.Lock()
	defer a.configMu.Unlock()
	return detachedCoreSettings(a.fc)
}

func detachedCoreSettings(value FileConfig) setting.App {
	raw, _ := json.Marshal(FileConfigToAppSettings(value))
	var out setting.App
	_ = json.Unmarshal(raw, &out)
	return out
}
