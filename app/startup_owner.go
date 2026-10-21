package app

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"

	"crude/coreprotocol"
	"crude/types"
)

// StartupSnapshot reads current application configuration without opening a
// workspace. Missing configuration is first use; malformed data stays a repair.
func (a *App) StartupSnapshot(ctx context.Context) (coreprotocol.StartupSnapshot, error) {
	var out coreprotocol.StartupSnapshot
	if err := ctx.Err(); err != nil {
		return out, err
	}
	config, err := loadFileConfigFrom(ConfigPath())
	if err != nil && !os.IsNotExist(err) {
		out.Issues = []types.OperationIssue{configStartupIssue(ConfigPath(), err)}
		a.configMu.Lock()
		out.Theme = FileConfigToAppSettings(a.fc).Theme
		a.configMu.Unlock()
		return out, nil
	}
	settings := FileConfigToAppSettings(config)
	out.Theme, out.Onboarding = settings.Theme, settings.Onboarding
	if out.Onboarding.IntroductionCompleted || len(config.Accounts) > 0 {
		return out, nil
	}
	used, err := hasSavedWorkspace(ctx, filepath.Join(DataDir(a.home), "workspaces"))
	if err != nil {
		out.Issues = append(out.Issues, types.OperationIssue{Title: "Profile state is not available", Summary: "Crude could not read saved workspace state.", Detail: err.Error()})
		return out, nil
	}
	out.ShowIntroduction = !used
	return out, nil
}

// hasSavedWorkspace stops at the first directory. It never opens databases or
// traverses project trees just to decide whether an introduction is necessary.
func hasSavedWorkspace(ctx context.Context, root string) (bool, error) {
	f, err := os.Open(root)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer func() { _ = f.Close() }()
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		entries, err := f.ReadDir(64)
		for _, entry := range entries {
			if entry.IsDir() {
				return true, nil
			}
		}
		if errors.Is(err, io.EOF) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
}

func (a *App) SaveStartup(ctx context.Context, edits []types.SettingEdit) (types.OnboardingSettings, error) {
	if err := ctx.Err(); err != nil {
		return types.OnboardingSettings{}, err
	}
	for _, edit := range edits {
		if len(edit.Path) != 2 || edit.Path[0] != "onboarding" {
			return types.OnboardingSettings{}, errors.New("startup can save only introduction and reminder choices")
		}
	}
	if err := types.ValidateSettingEdits(types.AppSettings{}, edits); err != nil {
		return types.OnboardingSettings{}, err
	}
	settings, err := a.commitCorePreferences(edits)
	return settings.Onboarding, err
}

func (a *App) ReadStartupSettings(ctx context.Context) (types.AppSettings, error) {
	if err := ctx.Err(); err != nil {
		return types.AppSettings{}, err
	}
	config, err := loadFileConfigFrom(ConfigPath())
	if os.IsNotExist(err) {
		return types.AppSettings{}, nil
	}
	if err != nil {
		return types.AppSettings{}, err
	}
	return FileConfigToAppSettings(config), nil
}
