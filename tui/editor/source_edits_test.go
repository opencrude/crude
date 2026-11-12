package editor

import (
	"crude/document"
	"reflect"
	"testing"
)

func TestSourceEditsRejectInvalidPlanBeforeAnyEffect(t *testing.T) {
	for _, edit := range []document.Edit{{Start: 2, End: 3, Text: "x"}, {Start: 0, End: 1, Text: string([]byte{255})}, {Start: 7, End: 8, Text: "x"}} {
		e := loadEditorText(t, "source.txt", "a😀é\r\nz")
		e.SetSelection(0, 0, 0, 3)
		before := e.captureViewState()
		revision := e.Revision()
		source := e.Value()
		if err := e.ApplySourceEdits(revision, []document.Edit{edit}); err == nil || e.Value() != source || e.Revision() != revision || !reflect.DeepEqual(e.captureViewState(), before) {
			t.Fatal("invalid formatter plan changed source", edit)
		}
	}
}
