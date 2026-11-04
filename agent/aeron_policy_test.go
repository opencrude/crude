package agent

import (
	"reflect"
	"testing"

	"crude/safety"
	"crude/tools"
)

func TestAeronAddsNoToolOrCapabilityRestriction(t *testing.T) {
	defs := tools.AgentTools
	if got := FilterTools(defs, nil, nil, CatAeron, false); !reflect.DeepEqual(got, defs) {
		t.Fatal("Aeron lost tools from the normal tool set")
	}
	if got := CategoryToolDefaults(CatAeron); len(got.AllowedTools) != 0 || len(got.DeniedTools) != 0 {
		t.Fatal("Aeron has a role-specific tool restriction")
	}
	if got := categoryPolicy(CatAeron); !reflect.DeepEqual(got, safety.DefaultPolicy()) {
		t.Fatal("Aeron has a role-specific capability restriction")
	}
}
