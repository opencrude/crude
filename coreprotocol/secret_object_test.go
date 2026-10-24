package coreprotocol_test

import (
	"testing"

	"crude/coreprotocol"
	"crude/types/jsondoc"
)

func TestSecretObjectDistinguishesAbsentNullAndEmpty(t *testing.T) {
	var null jsondoc.Object
	empty := jsondoc.Object{}
	value := jsondoc.Object{"nested": map[string]any{"secret": "kept"}}
	for _, tc := range []struct {
		name  string
		value *jsondoc.Object
	}{
		{"absent", nil}, {"null", &null}, {"empty", &empty}, {"value", &value},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := coreprotocol.SecretObject{Value: tc.value}
			raw, err := coreprotocol.Marshal(&in)
			if err != nil {
				t.Fatal(err)
			}
			out := coreprotocol.SecretObject{Keep: true, Value: &value}
			if err = coreprotocol.Decode(raw, &out); err != nil {
				t.Fatal(err)
			}
			if (out.Value == nil) != (tc.value == nil) {
				t.Fatal("optional secret pointer presence lost")
			}
			if out.Value != nil && ((*out.Value == nil) != (*tc.value == nil) || len(*out.Value) != len(*tc.value)) {
				t.Fatal("JSON null, empty object or content collapsed")
			}
		})
	}
}
