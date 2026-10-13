package remote

import (
	"context"
	"testing"
)

func TestStopShellControlCancelsExactReaderAndIsIdempotent(t *testing.T) {
	w := &Workspace{controlEnabled: true}
	ctx, cancel := context.WithCancel(context.Background())
	reader := &controlReader{cancel: cancel}
	controlReaders.Store(w, reader)
	w.StopShellControl()
	if ctx.Err() == nil || w.controlEnabled {
		t.Fatal("reader survived shell stop")
	}
	if _, retained := controlReaders.Load(w); retained {
		t.Fatal("stopped reader retained")
	}
	w.StopShellControl()
}

func TestOldControlReaderCannotRemoveReplacement(t *testing.T) {
	w := &Workspace{}
	old := &controlReader{cancel: func() {}}
	current := &controlReader{cancel: func() {}}
	controlReaders.Store(w, current)
	defer controlReaders.Delete(w)
	if controlReaders.CompareAndDelete(w, old) {
		t.Fatal("old reader removed replacement")
	}
	if got, _ := controlReaders.Load(w); got != current {
		t.Fatal("replacement lost")
	}
}
