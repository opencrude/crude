package coreprotocol_test

import (
	"bytes"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/tinylib/msgp/msgp"

	"crude/coreprotocol"
)

// A nil pointer omits a projection; a present pointer publishes an authoritative
// list, even when its slice is nil. In particular, delivered-empty must remain
// usable to clear previously installed inventory, not become "no update".
func TestOptionalPublicationCollectionsPreservePresence(t *testing.T) {
	t.Run("WorkspaceState.Registry", func(t *testing.T) {
		testOptionalPublicationCollection(t, 0,
			func(v *coreprotocol.SessionRegistry) *coreprotocol.WorkspaceState {
				return &coreprotocol.WorkspaceState{Registry: v}
			},
			func(v *coreprotocol.WorkspaceState) *coreprotocol.SessionRegistry { return v.Registry },
			coreprotocol.SessionRegistry{{ID: "session-a", ProjectName: "project"}, {ID: "session-b"}})
	})
	t.Run("WorkspaceState.Terminals", func(t *testing.T) {
		testOptionalPublicationCollection(t, 5,
			func(v *coreprotocol.TerminalInfos) *coreprotocol.WorkspaceState {
				return &coreprotocol.WorkspaceState{Terminals: v}
			},
			func(v *coreprotocol.WorkspaceState) *coreprotocol.TerminalInfos { return v.Terminals },
			coreprotocol.TerminalInfos{{ID: "terminal-a", Title: "shell", Alive: true}, {ID: "terminal-b", ExitCode: 7}})
	})
	t.Run("WorkspaceUpdate.Sessions", func(t *testing.T) {
		testOptionalPublicationCollection(t, 3,
			func(v *coreprotocol.SessionSnapshotList) *coreprotocol.WorkspaceUpdate {
				return &coreprotocol.WorkspaceUpdate{Sessions: v}
			},
			func(v *coreprotocol.WorkspaceUpdate) *coreprotocol.SessionSnapshotList { return v.Sessions },
			coreprotocol.SessionSnapshotList{{ID: "session-a", AgentID: "agent-a"}, {ID: "session-b"}})
	})
}

type publicationValue interface {
	coreprotocol.Value
	coreprotocol.Target
}

func testOptionalPublicationCollection[S ~[]E, E any, P publicationValue](t *testing.T, field int, wrap func(*S) P, list func(P) *S, populated S) {
	t.Helper()
	cases := []struct {
		name  string
		value *S
	}{
		{"absent", nil},
		{"present-nil", new(S)},
		{"present-empty", coreprotocol.Ptr(make(S, 0))},
		{"present-populated", &populated},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := wrap(tc.value)
			encoded, err := coreprotocol.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			// Inspect the actual tuple field independently of the generated decoder.
			raw := publicationTupleField(t, encoded, field)
			if tc.value == nil {
				if !bytes.Equal(raw, []byte{0xc0}) {
					t.Fatalf("absent pointer wire = %x, want MessagePack nil", raw)
				}
			} else {
				count, _, err := msgp.ReadArrayHeaderBytes(raw)
				if err != nil || int(count) != len(*tc.value) {
					t.Fatalf("present pointer wire = %x: array count %d, error %v", raw, count, err)
				}
				if len(*tc.value) == 0 && !bytes.Equal(raw, []byte{0x90}) {
					t.Fatalf("delivered-empty wire = %x, want empty array", raw)
				}
			}

			// Both fresh and stale targets must preserve the original presence and
			// content. Reusing nonempty storage exposes missed clears and shrinking.
			for _, stale := range []bool{false, true} {
				t.Run(map[bool]string{false: "fresh", true: "reused"}[stale], func(t *testing.T) {
					target := wrap(nil)
					if stale {
						target = wrap(coreprotocol.Ptr(append(slices.Clone(populated), populated...)))
					}
					if err := coreprotocol.Decode(encoded, target); err != nil {
						t.Fatal(err)
					}
					got := list(target)
					if (got == nil) != (tc.value == nil) {
						t.Fatalf("pointer presence changed: input %v, decoded %v", tc.value != nil, got != nil)
					}
					if tc.value != nil && !slices.EqualFunc(*got, *tc.value, func(a, b E) bool { return reflect.DeepEqual(a, b) }) {
						t.Fatalf("list content changed: got %#v, want %#v", *got, *tc.value)
					}
				})
			}

			// Decode may use its target as scratch on error; no malformed or partial
			// publication may be accepted as success by the public entry point.
			for cut := range len(encoded) {
				if err := coreprotocol.Decode(encoded[:cut], wrap(coreprotocol.Ptr(slices.Clone(populated)))); err == nil {
					t.Fatalf("accepted truncated publication at %d/%d", cut, len(encoded))
				}
			}
			if err := coreprotocol.Decode(append(slices.Clone(encoded), 0xc0), wrap(nil)); !errors.Is(err, coreprotocol.ErrTrailingBytes) {
				t.Fatalf("accepted trailing bytes: %v", err)
			}
			for _, malformed := range [][]byte{{0xc3}, {0xdd, 0xff, 0xff, 0xff, 0xff}} {
				// Replace the exact field offset, not an identical earlier nil/array.
				start := publicationTupleFieldOffset(t, encoded, field)
				bad := append(slices.Clone(encoded[:start]), malformed...)
				bad = append(bad, encoded[start+len(raw):]...)
				if err := coreprotocol.Decode(bad, wrap(nil)); err == nil {
					t.Fatalf("accepted malformed collection %x", malformed)
				}
			}
		})
	}
}

func publicationTupleFieldOffset(t *testing.T, encoded []byte, field int) int {
	t.Helper()
	count, rest, err := msgp.ReadArrayHeaderBytes(encoded)
	if err != nil || int(count) <= field {
		t.Fatalf("publication tuple header: count %d, error %v", count, err)
	}
	for range field {
		rest, err = msgp.Skip(rest)
		if err != nil {
			t.Fatal(err)
		}
	}
	return len(encoded) - len(rest)
}

func publicationTupleField(t *testing.T, encoded []byte, field int) []byte {
	t.Helper()
	start := publicationTupleFieldOffset(t, encoded, field)
	rest, err := msgp.Skip(encoded[start:])
	if err != nil {
		t.Fatal(err)
	}
	return encoded[start : len(encoded)-len(rest)]
}

// Guard the property-test oracles themselves: neither may silently allow the
// critical absent/present-empty distinction in either comparison direction.
func TestWireComparisonsRequirePointerPresence(t *testing.T) {
	var absent *coreprotocol.SessionRegistry
	for _, present := range []*coreprotocol.SessionRegistry{new(coreprotocol.SessionRegistry), coreprotocol.Ptr(coreprotocol.SessionRegistry{})} {
		for _, pair := range [][2]*coreprotocol.SessionRegistry{{absent, present}, {present, absent}} {
			a, b := reflect.ValueOf(pair[0]), reflect.ValueOf(pair[1])
			if exact(a, b, "Registry", true) == "" {
				t.Fatal("exact oracle equates absent with present empty")
			}
			if compare(a, b, "Registry") == "" {
				t.Fatal("JSON oracle equates absent with present empty")
			}
		}
	}
}
