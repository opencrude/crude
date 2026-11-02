package workspace

import (
	"fmt"
	"path/filepath"
	"strings"

	"crude/insight/catalog"
	"crude/types"
)

// recomputeLanguageStatusLocked is the sole owner of the visible language state.
func (m *LSPManager) recomputeLanguageStatusLocked() {
	var s types.LanguageServiceStatus
	switch {
	case m.activeStatus == LSPIdle && m.activeError != "":
		s = types.LanguageServiceStatus{State: types.LanguageFailed, Label: "Language server failed", Message: "Language server could not check this file. The file is too large. Change the file size limit in Language servers settings.", Action: types.LanguageActionOpenSettings, ActionLabel: "Open settings"}
	case m.isFailedLocked():
		name := m.activeServerNameLocked()
		if name == "" {
			name = "Language server"
		} else {
			name = filepath.Base(name)
		}
		s = types.LanguageServiceStatus{State: types.LanguageFailed, Label: "Language server failed", Message: fmt.Sprintf("%s failed. The server reported an error. Check its installation and restart it.", name), Action: types.LanguageActionRestartServer, ActionLabel: "Restart server"}
	case m.analysisBlocked != "":
		s = types.LanguageServiceStatus{State: types.LanguageFailed, Label: "Language server failed", Message: "Language servers could not start. This folder changed or cannot be verified. Trust this folder again in Workspace settings.", Action: types.LanguageActionTrustFolder, ActionLabel: "Trust folder"}
	case !m.analysisSettings.Trusted():
		s = types.LanguageServiceStatus{State: types.LanguageRestricted, Label: "Restricted mode", Message: "Language servers do not run in restricted mode.", Action: types.LanguageActionTrustFolder, ActionLabel: "Trust folder"}
	case m.analysisGovernor == nil || m.analysisGovernorError != "":
		s = types.LanguageServiceStatus{State: types.LanguageFailed, Label: "Language server failed", Message: "Language servers could not start. The process service is unavailable. Reopen this workspace and try again."}
	case !m.settings.Enabled:
		s = types.LanguageServiceStatus{State: types.LanguageOff, Label: "Language servers off", Message: "Language servers are off. Turn them on in Language servers settings.", Action: types.LanguageActionOpenSettings, ActionLabel: "Open settings"}
	case m.paused || m.closed:
		s = types.LanguageServiceStatus{State: types.LanguageOff, Label: "Language servers stopped", Message: "Language servers are stopped. Start language servers to check files.", Action: types.LanguageActionStartServers, ActionLabel: "Start Language Servers"}
	case m.documents[m.activeFilePath] != nil && !m.languageServerMatchesActiveLocked():
		name := strings.TrimPrefix(filepath.Ext(m.activeFilePath), ".")
		if language, ok := catalog.Current().LanguageForPath(m.activeFilePath, nil); ok && language.Name != "" {
			name = language.Name
		}
		message := "No language server for this file. Set one up in Language servers settings."
		if name != "" {
			message = fmt.Sprintf("No language server for %s. Set one up in Language servers settings.", name)
		}
		if reason, missing := m.assemblyStatusLocked(m.activeFilePath); missing {
			message = reason
		}
		s = types.LanguageServiceStatus{State: types.LanguageNoServer, Message: message, Action: types.LanguageActionOpenSettings, ActionLabel: "Open settings"}
	case m.activeStatus == LSPStarting || m.syncing:
		name := m.activeServerNameLocked()
		if name == "" {
			name = "language server"
		}
		s = types.LanguageServiceStatus{State: types.LanguageStarting, Label: "Checking…", Message: fmt.Sprintf("Starting %s.", name)}
	case m.activeStatus == LSPConnected:
		s = types.LanguageServiceStatus{State: types.LanguageReady, Message: "Language server is running."}
	case m.documents[m.activeFilePath] != nil:
		if state := m.states[m.documents[m.activeFilePath].key]; state != nil && state.stopped {
			s = types.LanguageServiceStatus{State: types.LanguageOff, Label: "Language servers stopped", Message: "Language server is stopped. Start language servers to check files.", Action: types.LanguageActionStartServers, ActionLabel: "Start Language Servers"}
		} else {
			s = types.LanguageServiceStatus{State: types.LanguageIdle, Message: "No results yet. Start language servers to check files.", Action: types.LanguageActionStartServers, ActionLabel: "Start Language Servers"}
		}
	default:
		s = types.LanguageServiceStatus{State: types.LanguageIdle, Message: "No results yet. Open a source file to check it."}
	}
	m.language = s
}

func (m *LSPManager) languageServerMatchesActiveLocked() bool {
	_, ok := m.resolveServerLocked(m.activeFilePath)
	return ok
}
