package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/types"
)

func TestWatchConfigEmitsThemePreset(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	cfgPath := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	fc := DefaultFileConfig()
	fc.Theme = &types.ThemeSettings{Preset: "dark"}
	if err := SaveFileConfig(fc); err != nil {
		t.Fatal(err)
	}

	ch := make(chan tea.Msg, 4)
	stop, err := WatchConfig(func(msg tea.Msg) { ch <- msg })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	fc.Theme = &types.ThemeSettings{Preset: "high-contrast"}
	if err := SaveFileConfig(fc); err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-ch:
		got, ok := msg.(types.ConfigReloadedMsg)
		if !ok {
			t.Fatalf("msg=%T", msg)
		}
		if got.ThemePreset != "high-contrast" {
			t.Fatalf("preset=%q", got.ThemePreset)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for config reload")
	}
}

func TestWatchConfigSettlesAfterRapidWrites(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	cfgPath := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}

	ch := make(chan tea.Msg, 4)
	stop, err := WatchConfig(func(msg tea.Msg) { ch <- msg })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	writePreset := func(preset string) {
		fc := DefaultFileConfig()
		fc.Theme = &types.ThemeSettings{Preset: preset}
		if err := SaveFileConfig(fc); err != nil {
			t.Fatal(err)
		}
	}

	writePreset("dark")
	writePreset("light")
	writePreset("high-contrast")

	select {
	case msg := <-ch:
		got, ok := msg.(types.ConfigReloadedMsg)
		if !ok {
			t.Fatalf("msg=%T", msg)
		}
		if got.ThemePreset != "high-contrast" {
			t.Fatalf("preset=%q want high-contrast after rapid writes settled", got.ThemePreset)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for settled config reload")
	}

	select {
	case msg := <-ch:
		t.Fatalf("unexpected second reload: %#v", msg)
	case <-time.After(configWatchDebounce + 50*time.Millisecond):
	}
}

func TestWatchConfigSkipsInvalidJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	cfgPath := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	fc := DefaultFileConfig()
	fc.Theme = &types.ThemeSettings{Preset: "light"}
	if err := SaveFileConfig(fc); err != nil {
		t.Fatal(err)
	}

	ch := make(chan tea.Msg, 4)
	stop, err := WatchConfig(func(msg tea.Msg) { ch <- msg })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	if err := os.WriteFile(cfgPath, []byte(`{"theme":{"preset":"high-contrast"`), 0o600); err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-ch:
		t.Fatalf("invalid JSON should not emit reload: %#v", msg)
	case <-time.After(configWatchDebounce + 200*time.Millisecond):
	}
}

func TestWatchConfigRecoversAfterInvalidThenValidWrite(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	cfgPath := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	fc := DefaultFileConfig()
	fc.Theme = &types.ThemeSettings{Preset: "dark"}
	if err := SaveFileConfig(fc); err != nil {
		t.Fatal(err)
	}

	ch := make(chan tea.Msg, 4)
	stop, err := WatchConfig(func(msg tea.Msg) { ch <- msg })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	if err := os.WriteFile(cfgPath, []byte(`not-json`), 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-ch:
		t.Fatalf("invalid JSON should not emit reload: %#v", msg)
	case <-time.After(configWatchDebounce + 200*time.Millisecond):
	}

	fc.Theme = &types.ThemeSettings{Preset: "high-contrast"}
	if err := SaveFileConfig(fc); err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-ch:
		got, ok := msg.(types.ConfigReloadedMsg)
		if !ok {
			t.Fatalf("msg=%T", msg)
		}
		if got.ThemePreset != "high-contrast" {
			t.Fatalf("preset=%q want high-contrast", got.ThemePreset)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reload after valid write")
	}
}

func TestWatchConfigStopIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	cfgPath := ConfigPath()
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SaveFileConfig(DefaultFileConfig()); err != nil {
		t.Fatal(err)
	}

	stop, err := WatchConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	stop()
	stop()
}

func TestWatchConfigRenameCreateEmitsReload(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	cfgDir := filepath.Dir(ConfigPath())
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}

	ch := make(chan tea.Msg, 4)
	stop, err := WatchConfig(func(msg tea.Msg) { ch <- msg })
	if err != nil {
		t.Fatal(err)
	}
	defer stop()

	cfgPath := ConfigPath()
	tempPath := cfgPath + ".tmp"
	fc := DefaultFileConfig()
	fc.Theme = &types.ThemeSettings{Preset: "light"}
	data, err := json.MarshalIndent(fc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tempPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tempPath, cfgPath); err != nil {
		t.Fatal(err)
	}

	select {
	case msg := <-ch:
		got, ok := msg.(types.ConfigReloadedMsg)
		if !ok {
			t.Fatalf("msg=%T", msg)
		}
		if got.ThemePreset != "light" {
			t.Fatalf("preset=%q want light", got.ThemePreset)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for reload after rename/create")
	}
}
