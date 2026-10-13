package session

import (
	"testing"

	"crude/safety"
	"crude/types/control"
)

func TestResolvedControlDescriptorEnforcesEffectCeiling(t *testing.T) {
	readOnly := safety.ReadOnlyPolicy()
	for _, tc := range []struct {
		name      string
		effect    control.Effect
		humanOnly bool
		want      bool
	}{
		{"read", control.Read, false, true},
		{"write", control.Write, false, false},
		{"delete", control.Delete, false, false},
		{"execute", control.Execute, false, false},
		{"human-only read", control.Read, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := controlDescriptorAllowed(readOnly, control.Descriptor{ID: tc.name, Effect: tc.effect, HumanOnly: tc.humanOnly})
			if got != tc.want {
				t.Fatalf("allowed = %v, want %v", got, tc.want)
			}
		})
	}
}
