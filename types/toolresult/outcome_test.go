package toolresult

import "testing"

func TestOutcomeMapsToOneSettledLifecycle(t *testing.T) {
	for _, test := range []struct {
		state         State
		status, phase string
	}{
		{Succeeded, "success", "done"}, {Failed, "error", "error"},
		{Denied, "error", "error"}, {Cancelled, "cancelled", "done"},
		{NotRun, "interrupted", "done"}, {Uncertain, "error", "error"},
	} {
		for _, flag := range []bool{false, true} {
			result := Outcome{State: test.state}
			if result.Status(flag) != test.status || result.PreviewPhase(flag) != test.phase {
				t.Fatalf("outcome %q drifted with transport flag %v", test.state, flag)
			}
		}
	}
}
