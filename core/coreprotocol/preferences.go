package coreprotocol

import (
	"crude/types/setting"
)

const ApplicationPreferencesSave = "settings.application.save"

// Preferences exclude account/capability entities; those have separate exact
// owner operations. ProxyURL is write-only because it may contain credentials.
type ApplicationPreferencesRequest struct {
	Edits    []setting.Edit `json:"edits"`
	ProxyURL SecretString   `json:"proxy_url"`
}
type ApplicationPreferencesResult struct {
	Revision string      `json:"revision"`
	Value    setting.App `json:"value"`
}
