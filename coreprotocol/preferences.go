package coreprotocol

import "crude/types"

const ApplicationPreferencesSave = "settings.application.save"

// Preferences exclude account/capability entities; those have separate exact
// owner operations. ProxyURL is write-only because it may contain credentials.
type ApplicationPreferencesRequest struct {
	ExpectedRevision string               `json:"expected_revision"`
	Value            types.AppSettings    `json:"value"`
	ProxyURL         SecretUpdate[string] `json:"proxy_url"`
}
type ApplicationPreferencesResult struct {
	Revision string            `json:"revision"`
	Value    types.AppSettings `json:"value"`
}
