package tools

import (
	"encoding/json"
	"strings"
	"testing"
)

func collaborationSchema(t *testing.T, name string) (ToolFuncDef, map[string]any) {
	t.Helper()
	for _, tool := range CollaborationTools {
		if tool.Function.Name != name {
			continue
		}
		var schema map[string]any
		if err := json.Unmarshal(tool.Function.Parameters, &schema); err != nil {
			t.Fatal(err)
		}
		return tool.Function, schema
	}
	t.Fatalf("collaboration tool %s not offered", name)
	return ToolFuncDef{}, nil
}

func schemaProperties(t *testing.T, schema map[string]any) map[string]any {
	t.Helper()
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("missing properties: %v", schema)
	}
	return properties
}

func TestAgentStartOffersOptionalExactProfileWithRequiredCategory(t *testing.T) {
	definition, schema := collaborationSchema(t, "agent_start")
	properties := schemaProperties(t, schema)
	profile, ok := properties["profile"].(map[string]any)
	if !ok || profile["type"] != "string" {
		t.Fatalf("missing profile string: %v", properties)
	}
	if _, oldName := properties["name"]; oldName {
		t.Fatal("nickname masquerades as configured profile")
	}
	if !strings.Contains(definition.Description, "not the allocated nickname") ||
		!strings.Contains(profile["description"].(string), "match category") {
		t.Fatal("profile/category identity is ambiguous")
	}
	required := schema["required"].([]any)
	if len(required) != 2 || required[0] != "description" || required[1] != "category" {
		t.Fatalf("profile must remain optional while category remains required: %v", required)
	}
}

func TestCollaborationRequestAndControlSchemasStateRevisionFences(t *testing.T) {
	send, sendSchema := collaborationSchema(t, "agent_send")
	sendProperties := schemaProperties(t, sendSchema)
	for _, field := range []string{"request_id", "request_revision", "supersedes", "recipient_ids"} {
		if _, ok := sendProperties[field]; !ok {
			t.Errorf("request schema missing %s", field)
		}
	}
	for _, required := range []string{"revision 1", "exact next request_revision", "original recipient set", "latest event"} {
		if !strings.Contains(send.Description, required) {
			t.Errorf("request revision rule missing %q", required)
		}
	}
	respond, schema := collaborationSchema(t, "agent_respond")
	required := schema["required"].([]any)
	if len(required) != 4 || !strings.Contains(respond.Description, "latest exact work-request revision") ||
		!strings.Contains(respond.Description, "request owner") {
		t.Fatalf("response must fence latest revision and owner: %s %v", respond.Description, required)
	}
	control, controlSchema := collaborationSchema(t, "agent_control")
	properties := schemaProperties(t, controlSchema)
	for _, field := range []string{"run_id", "expected_version", "runtime_epoch"} {
		property, ok := properties[field].(map[string]any)
		if !ok || !strings.Contains(property["description"].(string), "Required for ") {
			t.Errorf("conditional control field %s missing requirement: %v", field, properties[field])
		}
	}
	for _, required := range []string{"Pause/cancel require run_id and expected_version", "resume/reopen require runtime_epoch and no active run", "not the execution stack", "not proof the control applied"} {
		if !strings.Contains(control.Description, required) {
			t.Errorf("control contract missing %q", required)
		}
	}
}

func TestPublishReportSchemaKeepsRevisionAndLifecycleDistinct(t *testing.T) {
	report, schema := collaborationSchema(t, "publish_report")
	if _, ok := schemaProperties(t, schema)["supersedes_revision"]; !ok {
		t.Fatal("report correction lacks exact revision")
	}
	if !strings.Contains(report.Description, "not proof the run succeeded") {
		t.Fatal("report publication misrepresented as lifecycle success")
	}
}
