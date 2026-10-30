package app

import (
	"path/filepath"
	"testing"

	"crude/types/handover"
)

func TestCoreRestartUnchangedObservationDoesNotAppendReceipts(t *testing.T) {
	f := newRestartFixture(t)
	if _, err := f.host.Schedule(t.Context(), f.request); err != nil {
		t.Fatal(err)
	}
	f.port.state.Phase, f.port.state.ReadyContextRevision = handover.Ready, 7
	first, err := f.host.Inspect(t.Context(), f.request.ID)
	if err != nil || first.Phase != handover.Ready {
		t.Fatal(first, err)
	}
	count := func() int {
		files, err := filepath.Glob(filepath.Join(filepath.Dir(f.host.manifestPath(f.request.ID)), "receipt-*.json"))
		if err != nil {
			t.Fatal(err)
		}
		return len(files)
	}
	before := count()
	for range 128 {
		state, err := f.host.Inspect(t.Context(), f.request.ID)
		if err != nil || state != first {
			t.Fatal("read-only polling changed public operation state", state, err)
		}
	}
	if got := count(); got != before {
		t.Fatalf("unchanged quarter-second polling appended receipts: before=%d after=%d", before, got)
	}
}
