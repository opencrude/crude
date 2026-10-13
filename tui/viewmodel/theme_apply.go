package viewmodel

import (
	"crude/shellport"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/wm"
	ttypes "crude/types"
)

// themeConsumerID names a theme sink that ApplyThemePreset must update.
// The anti-regression test requires every ID in requiredThemeConsumerIDs to
// appear in the snapshot after a preset switch, and every non-nil *ui.Host
// field on App to be covered by a registered consumer (rootHost covers the
// whole shell tree via Host.SetTheme + applyTokenSet).
type themeConsumerID string

const (
	themeConsumerAppTheme           themeConsumerID = "app.Theme"
	themeConsumerRenderCtx          themeConsumerID = "renderCtx"
	themeConsumerRootHost           themeConsumerID = "rootHost"
	themeConsumerSessionProjections themeConsumerID = "sessionProjections"
	themeConsumerSubPaneProjections themeConsumerID = "subPaneProjections"
	themeConsumerSidebarScreen      themeConsumerID = "sidebarScreen"
	themeConsumerLauncherScreen     themeConsumerID = "launcherScreen"
	themeConsumerHeaderScreen       themeConsumerID = "headerScreen"
	themeConsumerTabScreen          themeConsumerID = "tabScreen"
	themeConsumerInputScreen        themeConsumerID = "inputScreen"
	themeConsumerInputModel         themeConsumerID = "Input"
	themeConsumerChatScreen         themeConsumerID = "chatScreen"
	themeConsumerEditorScreen       themeConsumerID = "editorScreen"
	themeConsumerEditorTokens       themeConsumerID = "EditorState.Editor"
	themeConsumerComparisons        themeConsumerID = "comparisonScreens"
	themeConsumerFindInFiles        themeConsumerID = "findInFiles"
	themeConsumerFilePicker         themeConsumerID = "FilePicker"
	themeConsumerSessionPicker      themeConsumerID = "sessionPicker"
	themeConsumerHelp               themeConsumerID = "helpScreen"
	themeConsumerOpenOverlays       themeConsumerID = "openOverlays"
)

// requiredThemeConsumerIDs is the enumerable contract for theme propagation.
// Adding a new persistent theme-holding surface requires registering it in
// themeConsumers() AND listing it here (or covering it via openOverlays /
// Host-field reflection in the anti-regression test).
var requiredThemeConsumerIDs = []themeConsumerID{
	themeConsumerAppTheme,
	themeConsumerRenderCtx,
	themeConsumerRootHost,
	themeConsumerSessionProjections,
	themeConsumerSubPaneProjections,
	themeConsumerInputModel,
	themeConsumerEditorTokens,
	themeConsumerComparisons,
	themeConsumerOpenOverlays,
}

// themeConsumer is one named sink ApplyThemePreset visits.
type themeConsumer struct {
	id    themeConsumerID
	apply func(th *theme.Theme)
	read  func() *theme.Theme
}

// applyThemeFrame overlays persisted presentation preferences onto a freshly
// built preset theme. Called wherever a preset is applied to the live shell so
// the user's frame and explicit Nerd Font choice survive every theme rebuild
// (startup, settings save, cancel-to-saved, and config.json hot reload).
func (m *App) applyThemeFrame(th *theme.Theme) {
	if m == nil || th == nil || m.ws == nil {
		return
	}
	applyThemePreferences(th, m.ws.LoadAppSettings())
}

func applyThemePreferences(th *theme.Theme, settings ttypes.AppSettings) {
	if th == nil {
		return
	}
	cfg := settings.Theme
	th.Spacing.Frame = ui.Insets{
		Top:    clampFramePad(cfg.PadTopOrDefault()),
		Right:  clampFramePad(cfg.PadRightOrDefault()),
		Bottom: clampFramePad(cfg.PadBottomOrDefault()),
		Left:   clampFramePad(cfg.PadLeftOrDefault()),
	}
	if settings.UI.UseNerdFont {
		enabled := true
		th.Tree.Config.UseNerdFont = &enabled
	}
}

// clampFramePad bounds a frame padding to a sane range: paddings are blank
// cells, so anything beyond the terminal size is meaningless and negative
// values are configuration noise.
func clampFramePad(n int) int {
	if n < 0 {
		return 0
	}
	if n > 20 {
		return 20
	}
	return n
}

// applyWorkspaceFrame is the workspace-first form of applyThemeFrame for
// startup, where the initial theme is built before the App exists.
func applyWorkspaceFrame(th **theme.Theme, ws shellport.Workspace) {
	if th == nil || *th == nil || ws == nil {
		return
	}
	applyThemePreferences(*th, ws.LoadAppSettings())
}

// applyThemePreset replaces the live theme from a named preset and refreshes
// TUI projections that retain theme-derived tokens.
func (m *App) applyThemePreset(preset string) {
	m.ApplyThemePreset(preset)
}

// ApplyThemePreset is the exported theme switch used by app-layer hot-reload
// and settings live preview. It walks the single enumerable consumer list so
// new surfaces cannot silently miss updates.
func (m *App) ApplyThemePreset(preset string) {
	if m == nil {
		return
	}
	th := ui.ThemeFromPreset(preset)
	m.applyThemeFrame(th)
	for _, c := range m.themeConsumers() {
		if c.apply != nil {
			c.apply(th)
		}
	}
	// Invalidate every colour-holding render cache. Theme switches must not
	// reuse ANSI/chroma/message pixels painted under the previous palette.
	m.cachedOutput = ""
	if m.mainSurf != nil {
		m.mainSurf.Buffer().Clear()
	}
}

// themeConsumers returns the authoritative list of theme sinks. This is the
// only place persistent surfaces should be wired for preset changes.
func (m *App) themeConsumers() []themeConsumer {
	return []themeConsumer{
		{
			id: themeConsumerAppTheme,
			apply: func(th *theme.Theme) {
				m.Theme = th
			},
			read: func() *theme.Theme { return m.Theme },
		},
		{
			id: themeConsumerRenderCtx,
			apply: func(th *theme.Theme) {
				if m.renderCtx != nil {
					m.renderCtx.Theme = th
				}
			},
			read: func() *theme.Theme {
				if m.renderCtx == nil {
					return nil
				}
				return m.renderCtx.Theme
			},
		},
		{
			id: themeConsumerRootHost,
			apply: func(th *theme.Theme) {
				if m.rootHost != nil {
					m.rootHost.SetTheme(th)
				}
			},
			read: func() *theme.Theme {
				if m.rootHost == nil {
					return nil
				}
				return m.rootHost.Theme()
			},
		},
		{
			id: themeConsumerSessionProjections,
			apply: func(th *theme.Theme) {
				if m.sessionProjections != nil {
					m.sessionProjections.SetTheme(th)
				}
				for _, state := range m.agentPrompts {
					if state.confirmation != nil {
						state.confirmation.SetTokens(ui.NewTokenSet(th))
					}
					if state.question != nil {
						state.question.SetTokens(ui.NewTokenSet(th))
					}
				}
			},
			read: func() *theme.Theme {
				if m.sessionProjections == nil {
					return nil
				}
				return m.sessionProjections.Theme()
			},
		},
		{
			id: themeConsumerSubPaneProjections,
			apply: func(th *theme.Theme) {
				if m.subPaneProjections != nil {
					m.subPaneProjections.SetTheme(th)
				}
			},
			read: func() *theme.Theme {
				if m.subPaneProjections == nil {
					return nil
				}
				return m.subPaneProjections.Theme()
			},
		},
		{
			id: themeConsumerSidebarScreen,
			apply: func(th *theme.Theme) {
				if m.sidebarScreen != nil {
					m.sidebarScreen.SetTheme(th)
				}
				if m.activityBar != nil {
					m.activityBar.SetTheme(th)
				}
				if m.activityList != nil {
					m.activityList.SetTheme(th)
				}
				if m.gitPanel != nil {
					m.gitPanel.SetTheme(th)
				}
				for _, s := range m.termScreens {
					if s != nil {
						s.SetTheme(th)
					}
				}
				if m.termDock != nil {
					m.termDock.SetTheme(th)
				}
			},
			read: func() *theme.Theme {
				if m.rootHost == nil {
					return nil
				}
				return m.rootHost.Theme()
			},
		},
		{
			id: themeConsumerLauncherScreen,
			apply: func(th *theme.Theme) {
				if m.launcherScreen != nil {
					m.launcherScreen.SetTokens(ui.NewTokenSet(th))
				}
			},
			read: func() *theme.Theme {
				if m.rootHost == nil {
					return nil
				}
				return m.rootHost.Theme()
			},
		},
		{
			id: themeConsumerHeaderScreen,
			apply: func(th *theme.Theme) {
				if m.headerScreen != nil {
					m.headerScreen.SetTheme(th)
				}
			},
			read: func() *theme.Theme {
				if m.rootHost == nil {
					return nil
				}
				return m.rootHost.Theme()
			},
		},
		{
			id: themeConsumerTabScreen,
			apply: func(th *theme.Theme) {
				if m.tabScreen != nil {
					m.tabScreen.SetTheme(th)
				}
			},
			read: func() *theme.Theme {
				if m.rootHost == nil {
					return nil
				}
				return m.rootHost.Theme()
			},
		},
		{
			id: themeConsumerInputScreen,
			apply: func(th *theme.Theme) {
				if m.inputScreen != nil {
					m.inputScreen.SetTheme(th)
				}
			},
			read: func() *theme.Theme {
				if m.rootHost == nil {
					return nil
				}
				return m.rootHost.Theme()
			},
		},
		{
			id: themeConsumerInputModel,
			apply: func(th *theme.Theme) {
				m.Input.SetTheme(th)
			},
			read: func() *theme.Theme { return m.Theme },
		},
		{
			id: themeConsumerChatScreen,
			apply: func(th *theme.Theme) {
				if m.chatScreen != nil {
					m.chatScreen.SetTheme(th)
				}
			},
			read: func() *theme.Theme {
				if m.rootHost == nil {
					return nil
				}
				return m.rootHost.Theme()
			},
		},
		{
			id: themeConsumerEditorScreen,
			apply: func(th *theme.Theme) {
				if m.editorScreen != nil {
					m.editorScreen.SetTheme(th)
				}
			},
			read: func() *theme.Theme {
				if m.rootHost == nil {
					return nil
				}
				return m.rootHost.Theme()
			},
		},
		{
			id: themeConsumerEditorTokens,
			apply: func(th *theme.Theme) {
				if m.EditorState != nil && m.EditorState.Editor != nil {
					m.EditorState.Editor.SetTheme(th)
				}
				if m.EditorState != nil {
					for _, tab := range m.EditorState.Tabs {
						if tab != nil && tab.Editor != nil && tab.Editor != m.EditorState.Editor {
							tab.Editor.SetTheme(th)
						}
					}
				}
			},
			read: func() *theme.Theme {
				if m.EditorState != nil && m.EditorState.Editor != nil {
					return m.EditorState.Editor.Theme()
				}
				return m.Theme
			},
		},
		{
			id: themeConsumerComparisons,
			apply: func(th *theme.Theme) {
				for _, view := range m.gitReviews {
					view.screen.SetTheme(th)
				}
				for _, view := range m.localDiffs {
					view.screen.SetTheme(th)
				}
			},
			read: func() *theme.Theme { return m.Theme },
		},
		{
			id: themeConsumerFindInFiles,
			apply: func(th *theme.Theme) {
				if m.findInFiles != nil {
					m.findInFiles.SetTokens(ui.NewTokenSet(th))
				}
			},
			read: func() *theme.Theme { return m.Theme },
		},
		{
			id: themeConsumerFilePicker,
			apply: func(th *theme.Theme) {
				if m.FilePicker != nil {
					m.FilePicker.SetTokens(ui.NewTokenSet(th))
				}
			},
			read: func() *theme.Theme { return m.Theme },
		},
		{
			id: themeConsumerSessionPicker,
			apply: func(th *theme.Theme) {
				if m.sessionPicker != nil {
					m.sessionPicker.SetTheme(th)
				}
			},
			read: func() *theme.Theme { return m.Theme },
		},
		{
			id: themeConsumerHelp,
			apply: func(th *theme.Theme) {
				if m.helpScreen != nil {
					m.helpScreen.SetTheme(th)
				}
			},
			read: func() *theme.Theme { return m.Theme },
		},
		{
			id: themeConsumerOpenOverlays,
			apply: func(th *theme.Theme) {
				m.applyThemeToOpenOverlays(th)
			},
			read: func() *theme.Theme {
				if m.renderCtx == nil {
					return nil
				}
				return m.renderCtx.Theme
			},
		},
	}
}

// themeConsumerSnapshots returns the theme each registered consumer currently
// holds. Used by the anti-regression test after ApplyThemePreset.
func (m *App) themeConsumerSnapshots() map[themeConsumerID]*theme.Theme {
	out := make(map[themeConsumerID]*theme.Theme, len(m.themeConsumers()))
	for _, c := range m.themeConsumers() {
		if c.read != nil {
			out[c.id] = c.read()
		}
	}
	return out
}

// applyThemeToOpenOverlays pushes the new theme into every open floating
// window host and marks content dirty so chrome repaints immediately
// (FloatingWindow caches painted surfaces until contentDirty).
func (m *App) applyThemeToOpenOverlays(th *theme.Theme) {
	if m == nil || th == nil || m.windowManager == nil {
		return
	}
	for _, fw := range m.windowManager.Windows() {
		if fw == nil {
			continue
		}
		applyThemeToFloatingContent(fw, th)
		fw.MarkContentDirty()
	}
}

func applyThemeToFloatingContent(fw *wm.FloatingWindow, th *theme.Theme) {
	if fw == nil || fw.Content == nil || th == nil {
		return
	}
	switch c := fw.Content.(type) {
	case *ui.WindowFrameWidget:
		if c != nil && c.Host() != nil {
			c.Host().SetTheme(th)
		}
	case *ui.ComponentWidget:
		if c != nil && c.Host() != nil {
			c.Host().SetTheme(th)
		}
	case interface{ Host() *ui.Host }:
		if h := c.Host(); h != nil {
			h.SetTheme(th)
		}
	case interface{ SetTheme(*theme.Theme) }:
		c.SetTheme(th)
	case interface{ SetTokens(*ui.TokenSet) }:
		c.SetTokens(ui.NewTokenSet(th))
	}
}

// themePresetFromSettings resolves the active theme preset from app settings.
func themePresetFromSettings(s ttypes.AppSettings) string {
	if s.Theme.Preset != "" {
		return s.Theme.Preset
	}
	return theme.PresetName(nil)
}
