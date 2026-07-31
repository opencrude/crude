package viewmodel

import (
	"crude/tui/theme"
	ttypes "crude/tui/types"
	"crude/tui/ui"
	"crude/tui/wm"
)

// themeConsumerID names a theme sink that ApplyThemePreset must update.
// The anti-regression test requires every ID in requiredThemeConsumerIDs to
// appear in the snapshot after a preset switch, and every non-nil *ui.Host
// field on App to be covered by a registered consumer.
type themeConsumerID string

const (
	themeConsumerAppTheme           themeConsumerID = "app.Theme"
	themeConsumerRenderCtx          themeConsumerID = "renderCtx"
	themeConsumerSidebarHost        themeConsumerID = "sidebarHost"
	themeConsumerSidebarScreen      themeConsumerID = "sidebarScreen"
	themeConsumerSessionProjections themeConsumerID = "sessionProjections"
	themeConsumerSubPaneProjections themeConsumerID = "subPaneProjections"
	themeConsumerLauncherHost       themeConsumerID = "launcherHost"
	themeConsumerLauncherScreen     themeConsumerID = "launcherScreen"
	themeConsumerHeaderHost         themeConsumerID = "headerHost"
	themeConsumerHeaderScreen       themeConsumerID = "headerScreen"
	themeConsumerTabHost            themeConsumerID = "tabHost"
	themeConsumerTabScreen          themeConsumerID = "tabScreen"
	themeConsumerInputHost          themeConsumerID = "inputHost"
	themeConsumerInputScreen        themeConsumerID = "inputScreen"
	themeConsumerInputModel         themeConsumerID = "Input"
	themeConsumerChatHost           themeConsumerID = "chatHost"
	themeConsumerChatScreen         themeConsumerID = "chatScreen"
	themeConsumerEditorHost         themeConsumerID = "editorHost"
	themeConsumerEditorScreen       themeConsumerID = "editorScreen"
	themeConsumerEditorTokens       themeConsumerID = "EditorState.Editor"
	themeConsumerStatusHost         themeConsumerID = "statusHost"
	themeConsumerStatusBar          themeConsumerID = "StatusBar"
	themeConsumerFindInFiles        themeConsumerID = "findInFiles"
	themeConsumerFilePicker         themeConsumerID = "FilePicker"
	themeConsumerSessionPicker      themeConsumerID = "sessionPicker"
	themeConsumerOpenOverlays       themeConsumerID = "openOverlays"
)

// requiredThemeConsumerIDs is the enumerable contract for theme propagation.
// Adding a new persistent theme-holding surface requires registering it in
// themeConsumers() AND listing it here (or covering it via openOverlays /
// Host-field reflection in the anti-regression test).
var requiredThemeConsumerIDs = []themeConsumerID{
	themeConsumerAppTheme,
	themeConsumerRenderCtx,
	themeConsumerSidebarHost,
	themeConsumerSessionProjections,
	themeConsumerSubPaneProjections,
	themeConsumerLauncherHost,
	themeConsumerHeaderHost,
	themeConsumerTabHost,
	themeConsumerInputHost,
	themeConsumerInputModel,
	themeConsumerChatHost,
	themeConsumerEditorHost,
	themeConsumerStatusHost,
	themeConsumerOpenOverlays,
}

// themeConsumer is one named sink ApplyThemePreset visits.
type themeConsumer struct {
	id    themeConsumerID
	apply func(th *theme.Theme)
	read  func() *theme.Theme
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
	for _, c := range m.themeConsumers() {
		if c.apply != nil {
			c.apply(th)
		}
	}
	// Invalidate every colour-holding render cache. Theme switches must not
	// reuse ANSI/chroma/message pixels painted under the previous palette.
	m.cachedOutput = ""
	if m.mainContentSurf != nil {
		m.mainContentSurf.Buffer().Clear()
	}
	if m.mainEditorSurf != nil {
		m.mainEditorSurf.Buffer().Clear()
	}
	if m.mainChatSurf != nil {
		m.mainChatSurf.Buffer().Clear()
	}
	if m.mainLauncherSurf != nil {
		m.mainLauncherSurf.Buffer().Clear()
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
			id: themeConsumerSidebarHost,
			apply: func(th *theme.Theme) {
				if m.sidebarHost != nil {
					m.sidebarHost.SetTheme(th)
				}
			},
			read: func() *theme.Theme {
				if m.sidebarHost == nil {
					return nil
				}
				return m.sidebarHost.Theme()
			},
		},
		{
			id: themeConsumerSidebarScreen,
			apply: func(th *theme.Theme) {
				if m.sidebarScreen != nil {
					m.sidebarScreen.SetTheme(th)
				}
			},
			read: func() *theme.Theme {
				if m.sidebarHost == nil {
					return nil
				}
				return m.sidebarHost.Theme()
			},
		},
		{
			id: themeConsumerSessionProjections,
			apply: func(th *theme.Theme) {
				if m.sessionProjections != nil {
					m.sessionProjections.SetTheme(th)
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
			id: themeConsumerLauncherHost,
			apply: func(th *theme.Theme) {
				if m.launcherHost != nil {
					m.launcherHost.SetTheme(th)
				}
			},
			read: func() *theme.Theme {
				if m.launcherHost == nil {
					return nil
				}
				return m.launcherHost.Theme()
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
				if m.launcherHost == nil {
					return nil
				}
				return m.launcherHost.Theme()
			},
		},
		{
			id: themeConsumerHeaderHost,
			apply: func(th *theme.Theme) {
				if m.headerHost != nil {
					m.headerHost.SetTheme(th)
				}
			},
			read: func() *theme.Theme {
				if m.headerHost == nil {
					return nil
				}
				return m.headerHost.Theme()
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
				if m.headerHost == nil {
					return nil
				}
				return m.headerHost.Theme()
			},
		},
		{
			id: themeConsumerTabHost,
			apply: func(th *theme.Theme) {
				if m.tabHost != nil {
					m.tabHost.SetTheme(th)
				}
			},
			read: func() *theme.Theme {
				if m.tabHost == nil {
					return nil
				}
				return m.tabHost.Theme()
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
				if m.tabHost == nil {
					return nil
				}
				return m.tabHost.Theme()
			},
		},
		{
			id: themeConsumerInputHost,
			apply: func(th *theme.Theme) {
				if m.inputHost != nil {
					m.inputHost.SetTheme(th)
				}
			},
			read: func() *theme.Theme {
				if m.inputHost == nil {
					return nil
				}
				return m.inputHost.Theme()
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
				if m.inputHost == nil {
					return nil
				}
				return m.inputHost.Theme()
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
			id: themeConsumerChatHost,
			apply: func(th *theme.Theme) {
				if m.chatHost != nil {
					m.chatHost.SetTheme(th)
				}
			},
			read: func() *theme.Theme {
				if m.chatHost == nil {
					return nil
				}
				return m.chatHost.Theme()
			},
		},
		{
			id: themeConsumerChatScreen,
			apply: func(th *theme.Theme) {
				if m.chatScreen != nil {
					m.chatScreen.SetTheme(th)
				}
			},
			read: func() *theme.Theme {
				if m.chatHost == nil {
					return nil
				}
				return m.chatHost.Theme()
			},
		},
		{
			id: themeConsumerEditorHost,
			apply: func(th *theme.Theme) {
				if m.editorHost != nil {
					m.editorHost.SetTheme(th)
				}
			},
			read: func() *theme.Theme {
				if m.editorHost == nil {
					return nil
				}
				return m.editorHost.Theme()
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
				if m.editorHost == nil {
					return nil
				}
				return m.editorHost.Theme()
			},
		},
		{
			id: themeConsumerEditorTokens,
			apply: func(th *theme.Theme) {
				if m.EditorState != nil && m.EditorState.Editor != nil {
					m.EditorState.Editor.SetTheme(th)
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
			id: themeConsumerStatusHost,
			apply: func(th *theme.Theme) {
				if m.statusHost != nil {
					m.statusHost.SetTheme(th)
				}
			},
			read: func() *theme.Theme {
				if m.statusHost == nil {
					return nil
				}
				return m.statusHost.Theme()
			},
		},
		{
			id: themeConsumerStatusBar,
			apply: func(th *theme.Theme) {
				if m.StatusBar != nil {
					m.StatusBar.SetTheme(th)
				}
			},
			read: func() *theme.Theme {
				if m.statusHost == nil {
					return nil
				}
				return m.statusHost.Theme()
			},
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
