package turnloop

import (
	"reflect"
	"testing"

	"crude/agent/safety"
	"crude/agent/tools"
)

func TestErrandAddsNoToolOrCapabilityRestriction(t *testing.T) {
	defs := tools.AgentTools
	if got := FilterTools(defs, nil, nil, CatErrand, false); !reflect.DeepEqual(got, defs) {
		t.Fatal("Errand lost tools from the normal tool set")
	}
	if got := CategoryToolDefaults(CatErrand); len(got.AllowedTools) != 0 || len(got.DeniedTools) != 0 {
		t.Fatal("Errand has a role-specific tool restriction")
	}
	if got := categoryPolicy(CatErrand); !reflect.DeepEqual(got, safety.DefaultPolicy()) {
		t.Fatal("Errand has a role-specific capability restriction")
	}
}
