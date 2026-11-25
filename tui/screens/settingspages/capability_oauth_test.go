package settingspages

import (
	"encoding/json"
	"strings"
	"testing"

	"crude/core/shellport/remote"
	"crude/tui/ui"
	captypes "crude/types/capability"
)

func TestMCPConnectionOAuthSettingsPreserveWriteOnlyReference(t *testing.T) {
	model, root, _ := capabilityFixture(t, captypes.KindMCP)
	catalog := root.capCatalog
	const id = "registered"
	config := captypes.ConnectionConfig{
		ID: id, Name: "Registered", URL: "https://mcp.example.test/mcp", Transport: "http", Auth: "oauth",
		OAuthClientID: "native-client", OAuthIssuer: "https://issuer.example.test",
		CredentialRef: remote.StoredSecret,
	}
	model.capabilities.Connections[id] = config
	catalog.detail, catalog.id = true, id
	catalog.detailForm("connection")
	capabilityAction(catalog, "edit")
	form := model.capabilityDraft
	ref := form.Field("oauth-secret-ref")
	if ref == nil || ref.Type != ui.FieldPassword || ref.Placeholder != "Stored" || ref.Value.String() != "" {
		t.Fatal("the existing secret reference must be write-only in the editor")
	}
	form.SetValue("name", ui.StringValue("Renamed"))
	action, err := catalog.draftAction()
	if err != nil || action.Operation != "save-connection" || action.Connection.CredentialRef != remote.StoredSecret || action.Connection.OAuthIssuer != config.OAuthIssuer {
		t.Fatalf("an unrelated edit did not keep the stored reference: %v", err)
	}
	form.SetValue("oauth-issuer", ui.StringValue("https://other.example.test"))
	if _, err := catalog.draftAction(); err == nil {
		t.Fatal("an unchanged masked field silently transferred its secret reference to another issuer")
	}
	form.SetValue("oauth-secret-ref", ui.StringValue("secret:SECOND_CLIENT"))
	action, err = catalog.draftAction()
	if err != nil || action.Connection.CredentialRef != "secret:SECOND_CLIENT" {
		t.Fatalf("the explicit write-only replacement was not staged: %v", err)
	}
	safeBytes, err := json.Marshal(captypes.SafeConnection(*action.Connection))
	if err != nil || strings.Contains(string(safeBytes), "SECOND_CLIENT") || !strings.Contains(string(safeBytes), "https://other.example.test") {
		t.Fatal("the preview leaked the reference or hid the public issuer")
	}
	form.SetValue("oauth-secret-ref", ui.StringValue("-"))
	form.SetValue("oauth-issuer", ui.StringValue(""))
	form.SetValue("oauth-client", ui.StringValue(""))
	form.SetValue("oauth-metadata", ui.StringValue("https://client.example.test/crude.json"))
	action, err = catalog.draftAction()
	if err != nil || action.Connection.CredentialRef != "" || action.Connection.OAuthMetadataURL != "https://client.example.test/crude.json" {
		t.Fatalf("switching to the public metadata registration failed: %v", err)
	}
	b, err := action.Connection.MarshalMsg(nil)
	if err != nil {
		t.Fatal(err)
	}
	var restored captypes.ConnectionConfig
	if _, err := restored.UnmarshalMsg(b); err != nil || restored.OAuthMetadataURL != action.Connection.OAuthMetadataURL || restored.CredentialRef != "" {
		t.Fatal("OAuth settings did not survive the normal connection wire format")
	}
}
