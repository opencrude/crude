package provider

import (
	"reflect"
	"testing"
)

func TestServiceTierChoicesForMapsProtocolTiersToRoleChoices(t *testing.T) {
	for _, tc := range []struct {
		name          string
		offered, want []string
	}{
		{"default and flex", []string{"default", "flex"}, []string{"standard", "flex"}},
		{"priority also allows fast", []string{"priority"}, []string{"fast", "priority"}},
		{"protocol names do not change role order or duplicate choices", []string{"flex", "fast", "priority", "standard_only", "default", "auto"}, []string{"standard", "fast", "priority", "flex"}},
		{"protocol without tiers", nil, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ServiceTierChoicesFor(tc.offered); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("choices for %v = %#v, want %#v", tc.offered, got, tc.want)
			}
		})
	}
}
