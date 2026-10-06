package settings

import (
	"strings"
	"testing"

	"crude/tui/surface"
	"crude/tui/ui"
	captypes "crude/types/capability"
)

func TestMCPDetailRefreshesLiveFingerprintAndGrantWithoutLosingFilterFocus(t *testing.T) {
	model, root, host := capabilityFixture(t, captypes.KindMCP)
	catalog := root.capCatalog
	capabilityAction(catalog, "inspect")
	catalog.detailForm("tools")
	catalog.filter.SetValue("lookup")
	catalog.syncDetailRows()
	capabilityAction(catalog, "inspect-item")
	host.Focus(catalog.filter.ID())
	filter := catalog.filter

	snapshot := model.capabilitySnapshot
	snapshot.Generation++
	snapshot.Settings = captypes.Clone(snapshot.Settings)
	connection := snapshot.Settings.Connections["docs"]
	connection.Grants["lookup"] = captypes.ToolGrant{
		Effect:      captypes.EffectRead,
		Scope:       "workspace/docs",
		Fingerprint: "lookup-schema-v2",
	}
	snapshot.Settings.Connections["docs"] = connection
	snapshot.Connections[0].Fingerprint = "connection-fingerprint-v2"
	snapshot.Connections[0].Tools[0].Fingerprint = "lookup-schema-v2"
	snapshot.Connections[0].Tools[0].Effect = captypes.EffectRead
	snapshot.Connections[0].Tools[0].Scope = "workspace/docs"
	snapshot.Connections[0].Tools[0].Reason = ""
	model.SetCapabilitySnapshot(snapshot)

	painted := paintCapabilitySync(t, host, 160, 40)
	if catalog.filter != filter || catalog.filter.Value() != "lookup" {
		t.Fatalf("live MCP refresh replaced or cleared the retained filter: %q", catalog.filter.Value())
	}
	if host.Focused() != catalog.filter.ID() {
		t.Fatalf("live MCP refresh stole focus: got %q want %q", host.Focused(), catalog.filter.ID())
	}
	if !strings.Contains(painted, "lookup-schema-v2") || strings.Contains(painted, "lookup-schema\n") {
		t.Fatalf("open MCP tool detail did not replace the observed fingerprint in place:\n%s", painted)
	}
	if !strings.Contains(painted, "Granted; agent role and selection still apply") {
		t.Fatalf("open MCP tool detail did not paint the new grant state:\n%s", painted)
	}
}

func TestMCPPromptDraftAndFocusSurviveConstrainedLiveSnapshot(t *testing.T) {
	model, root, host := capabilityFixture(t, captypes.KindMCP)
	catalog := root.capCatalog
	capabilityAction(catalog, "inspect")
	catalog.detailForm("prompts")
	capabilityAction(catalog, "inspect-item")
	paintCapabilitySync(t, host, 80, 24)
	form := catalog.forms[catalog.viewKey()]
	form.SetValue("argument/topic", ui.StringValue("keep this harmless draft"))
	host.MarkLayout()
	host.Layout()
	host.Focus(catalog.form.ID())
	catalog.form.FocusField("argument/topic")
	control := catalog.form.Field("argument/topic")

	snapshot := model.capabilitySnapshot
	snapshot.Generation++
	snapshot.Connections[0].Prompts = append(snapshot.Connections[0].Prompts, captypes.Prompt{
		ConnectionID: "docs",
		Name:         "summarize",
		Description:  "Summarize a topic",
	})
	model.SetCapabilitySnapshot(snapshot)

	painted := paintCapabilitySync(t, host, 80, 24)
	if catalog.forms[catalog.viewKey()] != form || catalog.form.Field("argument/topic") != control {
		t.Fatal("live MCP refresh rebuilt the retained prompt draft owner")
	}
	if got := form.Field("argument/topic").Value.String(); got != "keep this harmless draft" {
		t.Fatalf("live MCP refresh changed prompt draft to %q", got)
	}
	if host.Focused() != catalog.form.ID() {
		t.Fatalf("live MCP refresh stole constrained form focus: got %q", host.Focused())
	}
	if !strings.Contains(painted, "keep this harmless draft") {
		t.Fatalf("constrained retained draft was not painted:\n%s", painted)
	}
}

func TestMCPInspectedToolRemovalClearsObsoleteSchemaAndGrant(t *testing.T) {
	model, root, host := capabilityFixture(t, captypes.KindMCP)
	catalog := root.capCatalog
	capabilityAction(catalog, "inspect")
	catalog.detailForm("tools")
	capabilityAction(catalog, "inspect-item")
	paintCapabilitySync(t, host, 160, 40)
	model.capabilitySnapshot.Generation++
	model.capabilitySnapshot.Connections[0].Tools = nil
	model.SetCapabilitySnapshot(model.capabilitySnapshot)
	painted := paintCapabilitySync(t, host, 160, 40)
	if !strings.Contains(painted, "is no longer observed") || strings.Contains(painted, "Fingerprint: lookup-schema") {
		t.Fatalf("removed tool retained an obsolete inspection:\n%s", painted)
	}
}

func paintCapabilitySync(t *testing.T, host *ui.Host, width, height int) string {
	t.Helper()
	host.Resize(width, height)
	host.MarkLayout()
	host.Layout()
	buffer := surface.NewCellBuffer(width, height)
	host.Paint(buffer)
	return bufferPlain(buffer)
}
